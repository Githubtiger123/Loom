// Package worker 将持久操作落实到 Kubernetes，不从集群反向产生业务授权。
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/config"
	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/service"
	"github.com/google/uuid"
	"gorm.io/gorm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

var errSuperseded = errors.New("目标版本已过时")

type Runner struct {
	DB          *gorm.DB
	Kube        *k8s.Client
	Service     *service.Service
	Cluster     string
	Log         *slog.Logger
	Concurrency int
	// 仅由可信的组合根注入；不是用户提交的镜像地址，也不在这里加载镜像目录。
	NotebookImages        map[string]string
	NotebookImageResolver NotebookImageResolver
	NotebookImageCatalog  *config.NotebookImageCatalog
}

func (r *Runner) Run(ctx context.Context) error {
	if r.Kube == nil {
		return fmt.Errorf("worker 必须配置可用的 Kubernetes 客户端")
	}
	if r.Concurrency < 1 {
		r.Concurrency = 4
	}
	maxOpen, err := inframysql.MaxOpenConnections(r.DB)
	if err != nil {
		return err
	}
	if maxOpen != 0 && maxOpen < r.Concurrency+2 {
		return fmt.Errorf("数据库 max_open 至少为 worker 并发数 + 2")
	}
	r.Log.Info("持久操作执行器启动", "cluster", r.Cluster, "concurrency", r.Concurrency)
	var wg sync.WaitGroup
	for range r.Concurrency {
		wg.Add(1)
		go func() { defer wg.Done(); r.loop(ctx) }()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

func (r *Runner) loop(ctx context.Context) {
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		claim, err := repository.ClaimOperation(claimCtx, r.DB, r.Cluster, uuid.NewString())
		cancel()
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) && ctx.Err() == nil {
				r.Log.Error("领取任务失败", "err", err)
			}
			if !pause(ctx, 2*time.Second) {
				return
			}
			continue
		}
		r.execute(ctx, claim)
	}
}

func (r *Runner) execute(parent context.Context, c *repository.Claim) {
	// 单轮执行有限时长。等待 Controller 时持久化下一次检查，不占着线程休眠。
	limit := 40 * time.Second
	if c.Action == "model.pipeline.import" {
		limit = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	lock, err := repository.TargetLock(ctx, r.DB, r.Cluster, c.TargetType, c.TargetKey)
	if err != nil {
		if !errors.Is(err, repository.ErrLeaseLost) {
			r.Log.Warn("获取目标锁失败", "operation", c.ID, "err", err)
		}
		r.finish(parent, c, "PENDING", "WAIT_TARGET_LOCK", "等待同一目标的其他执行结束", 3*time.Second, false)
		return
	}
	defer lock.Close()
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := repository.New(r.DB).Renew(ctx, c); err != nil {
					cancel()
					return
				}
				if err := repository.CheckTargetLock(ctx, lock); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(done); <-heartbeatDone }()
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := repository.CheckTargetLock(ctx, lock); err != nil {
			return err
		}
		return repository.New(r.DB).Renew(ctx, c)
	}
	if err := guard(); err != nil {
		return
	}
	var runErr error
	func() {
		defer func() {
			if p := recover(); p != nil {
				runErr = fmt.Errorf("任务处理异常: %v", p)
			}
		}()
		switch c.Action {
		case "model.pipeline.import":
			runErr = r.Service.ExecuteModelImport(ctx, c, guard)
		case "group.create":
			runErr = r.createGroup(ctx, c, guard)
		case "group.disable", "group.restore", "group.delete", "group.quota.update":
			runErr = r.groupLifecycle(ctx, c, guard)
		case "workspace.delete":
			runErr = r.deleteWorkspace(ctx, c, guard)
		case "user.DISABLED", "member.roles.replace":
			runErr = r.resourcePolicy(ctx, c, guard)
		case "access.reconcile":
			runErr = r.access(ctx, c, guard)
		case "notebook.create", "notebook.start", "notebook.stop", "notebook.update", "notebook.delete":
			runErr = r.notebook(ctx, c, guard)
		default:
			runErr = &k8s.Permanent{Reason: "不支持的操作类型"}
		}
	}()
	if errors.Is(runErr, repository.ErrLeaseLost) {
		return
	}
	if errors.Is(runErr, errSuperseded) || errors.Is(runErr, repository.ErrNotebookSuperseded) {
		if c.Action == "access.reconcile" {
			g, u, err := accessIDs(c.TargetKey)
			if err == nil {
				if err := r.Service.RefreshAccessTarget(ctx, g, u); err != nil {
					r.Log.Error("保存最新访问目标失败", "operation", c.ID, "err", err)
				}
			}
		}
		r.finish(parent, c, "SUPERSEDED", "SUPERSEDED", runErr.Error(), 0, false)
		return
	}
	if runErr == nil {
		// 领域处理器已经在同一事务中提交成功结果，不能再次无条件回填。
		return
	}
	if c.Action == "model.pipeline.import" {
		appErr := apperr.From(runErr)
		// Never persist raw SDK/SQL errors (including signed URLs or payloads)
		// into public operation.lastError or audit records.
		switch appErr.Code {
		case apperr.CodeInvalid, apperr.CodeForbidden, apperr.CodeUnauthorized, apperr.CodeConflict, apperr.CodeNotFound, apperr.CodeQuotaExceeded:
			runErr = &k8s.Permanent{Reason: appErr.Message}
		default:
			var fatal *k8s.Permanent
			if !errors.As(runErr, &fatal) {
				runErr = fmt.Errorf("模型导入暂不可用，请核对存储权限、空间及服务配置")
			}
		}
	}
	var pending *k8s.Pending
	if errors.As(runErr, &pending) && time.Now().Before(c.Deadline) {
		r.finish(parent, c, "PENDING", pending.Step, pending.Reason, 5*time.Second, false)
		return
	}
	var fatal *k8s.Permanent
	permanent := errors.As(runErr, &fatal) || apierrors.IsForbidden(runErr) || apierrors.IsUnauthorized(runErr) || apierrors.IsInvalid(runErr) || apierrors.IsBadRequest(runErr)
	if errors.As(runErr, &pending) {
		permanent = true
		runErr = fmt.Errorf("等待超过 30 分钟: %w", runErr)
	}
	if permanent || c.Attempt >= 7 {
		r.finish(parent, c, "FAILED", "FAILED", runErr.Error(), 0, true)
		return
	}
	delay := time.Duration(1<<min(c.Attempt+1, 7))*time.Second + time.Duration(rand.IntN(1000))*time.Millisecond
	r.finish(parent, c, "RETRYING", c.Step, runErr.Error(), delay, true)
}

func (r *Runner) finish(parent context.Context, c *repository.Claim, status, step, message string, delay time.Duration, failed bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	message = truncate(message, 1000)
	err := repository.InTx(ctx, r.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		if err := st.Fence(ctx, c); err != nil {
			return err
		}
		if strings.HasPrefix(c.Action, "notebook.") {
			return r.finishNotebook(ctx, st, c, status, step, message, delay, failed)
		}
		if err := st.Finish(ctx, c, status, step, message, delay, failed); err != nil {
			return err
		}
		if status == "FAILED" {
			if err := r.markLifecycleFailure(ctx, st, c, message); err != nil {
				return err
			}
		}
		if c.Action == "group.create" && status == "FAILED" {
			return st.DB.WithContext(ctx).Model(&model.Group{}).Where("CAST(id AS CHAR) = ? AND status NOT IN ?", c.TargetKey, []string{"DELETING", "DELETED"}).
				Updates(map[string]any{"status": gorm.Expr("IF(profile_uid IS NULL,'FAILED','DEGRADED')"), "observed_state": "ERROR", "last_error": message}).Error
		}
		if c.Action == "access.reconcile" {
			groupID, userID, err := accessIDs(c.TargetKey)
			if err != nil {
				return nil
			}
			accessStatus := "PENDING"
			if status == "FAILED" {
				accessStatus = "FAILED"
			}
			if status != "SUPERSEDED" {
				return st.DB.WithContext(ctx).Model(&model.GroupAccess{}).Where("group_id = ? AND user_id = ? AND desired_version = ?", groupID, userID, c.TargetVersion).
					Updates(map[string]any{"status": accessStatus, "last_error": message}).Error
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, repository.ErrLeaseLost) {
		r.Log.Error("任务结果保存失败，保留租约供恢复", "operation", c.ID, "err", err)
	}
	if status == "FAILED" {
		r.Log.Error("操作失败需处理", "operation", c.ID, "action", c.Action, "reason", message)
	}
}

func accessIDs(target string) (uint64, uint64, error) {
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		return 0, 0, &k8s.Permanent{Reason: "访问目标格式无效"}
	}
	g, e1 := strconv.ParseUint(parts[0], 10, 64)
	u, e2 := strconv.ParseUint(parts[1], 10, 64)
	if e1 != nil || e2 != nil || g == 0 || u == 0 {
		return 0, 0, &k8s.Permanent{Reason: "访问目标 ID 无效"}
	}
	return g, u, nil
}
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}
