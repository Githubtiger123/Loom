package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/dto"
	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
	"gorm.io/gorm"
)

func (r *Runner) RunKServe(ctx context.Context) error {
	if r.Kube == nil || r.Service == nil || r.Service.KServeClient == nil || r.Service.KServeVerifier == nil || !r.Service.KServeConfig.Enabled {
		return fmt.Errorf("kserve-worker 需要启用 kserve 并装配 Kubernetes 客户端和空间核验器")
	}
	if r.Concurrency < 1 || r.Concurrency > 16 {
		return fmt.Errorf("kserve-worker 并发必须为 1–16")
	}
	syncConcurrency, syncQPS := r.Service.KServeConfig.SyncConcurrency, r.Service.KServeConfig.SyncQPS
	if syncConcurrency < 1 || syncConcurrency > 16 || syncQPS < 1 || syncQPS > 100 {
		return fmt.Errorf("kserve-worker 状态同步并发或限速配置无效")
	}
	maxOpen, err := inframysql.MaxOpenConnections(r.DB)
	if err != nil {
		return err
	}
	// 执行器持有目标锁专用连接，且仍需业务查询连接；observer 单 worker
	// 与分页 sweep 各最多占用一个连接，另留两个连接余量。
	if maxOpen != 0 && maxOpen < 2*r.Concurrency+syncConcurrency+1+1+2 {
		return fmt.Errorf("数据库 max_open 至少为 kserve-worker 执行并发 × 2 + 状态同步并发 + observer 1 + sweep 1 + 2")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	observerErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		observerErr <- r.observeKServe(ctx)
	}()
	syncTicks := time.NewTicker(time.Second / time.Duration(syncQPS))
	defer syncTicks.Stop()
	for range syncConcurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				select {
				case <-ctx.Done():
					return
				case <-syncTicks.C:
				}
				workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				found, err := r.Service.SyncNextKServe(workCtx)
				cancel()
				if err != nil && ctx.Err() == nil {
					r.Log.Warn("KServe 周期状态同步本轮未完成")
				}
				if (!found || err != nil) && !pause(ctx, time.Second) {
					return
				}
			}
		}()
	}
	for range r.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				claim, err := r.Service.ClaimMLExecution(claimCtx, r.Service.KServePeer(), dto.ClaimMLExecutionReq{Cluster: r.Cluster,
					Actions: []string{string(service.MLKServeCreate), string(service.MLKServeUpdate), string(service.MLKServeDeploy), string(service.MLKServeStop), string(service.MLKServeDelete)}})
				cancel()
				if err != nil || claim == nil {
					if err != nil && ctx.Err() == nil {
						r.Log.Warn("KServe 任务领取失败")
					}
					if !pause(ctx, 2*time.Second) {
						return
					}
					continue
				}
				workCtx, finish := context.WithTimeout(ctx, 45*time.Second)
				err = r.Service.ExecuteKServeClaim(workCtx, claim)
				finish()
				if err != nil && ctx.Err() == nil {
					r.Log.Warn("KServe 任务本轮未完成，保留持久状态等待恢复", "operation", claim.OperationID)
				}
			}
		}()
	}
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-observerErr:
		if ctx.Err() != nil {
			runErr = nil
		} else if runErr == nil {
			runErr = fmt.Errorf("KServe observer 意外退出")
		}
	}
	cancel()
	wg.Wait()
	return runErr
}

func (r *Runner) observeKServe(ctx context.Context) error {
	const namespacePrefix = "namespace/"
	mapNamespace := func(obj any) []string {
		n := observationObject(obj)
		if n == nil || n.GetNamespace() == "" {
			return nil
		}
		// 包括 tombstone 在内，事件只唤醒 namespace，不携带观测状态或业务身份。
		return []string{namespacePrefix + n.GetNamespace()}
	}
	var enqueue func(string)
	enqueueServices := func(ctx context.Context, namespace string, emit func(string)) error {
		return r.Service.EnqueueKServeObservations(ctx, namespace, func(id uint64) {
			emit(strconv.FormatUint(id, 10))
		})
	}
	sweep := observationIDSweep(func(ctx context.Context) *gorm.DB {
		return r.DB.WithContext(ctx).Model(&model.KServeService{}).
			Where("cluster = ? AND sync_enabled = TRUE AND desired_state = 'SERVING' AND deleted_at IS NULL AND external_id IS NOT NULL AND external_id <> ''", r.Cluster)
	}, "")
	return r.runResourceObserver(ctx, resourceObservation{
		Name: "kserve",
		Sources: []observationSource{
			{Informer: r.Kube.NewResourceInformer(k8s.KServeGVR, "app.kubernetes.io/managed-by=loom"), MapKeys: mapNamespace},
			// 下层对象不保证继承 Loom 管理标签；归属由实时 ObserveKServe 核验。
			{Informer: r.Kube.NewResourceInformer(k8s.DeploymentGVR, ""), MapKeys: mapNamespace},
			{Informer: r.Kube.NewResourceInformer(k8s.ReplicaSetGVR, ""), MapKeys: mapNamespace},
			{Informer: r.Kube.NewResourceInformer(k8s.PodGVR, ""), MapKeys: mapNamespace},
		},
		BindEnqueue: func(emit func(string)) { enqueue = emit },
		Sync: func(ctx context.Context, key string) error {
			if namespace, ok := strings.CutPrefix(key, namespacePrefix); ok {
				if namespace == "" {
					return nil
				}
				return enqueueServices(ctx, namespace, enqueue)
			}
			id, err := strconv.ParseUint(key, 10, 64)
			if err != nil || id == 0 {
				return nil
			}
			return r.Service.SyncKServeObservation(ctx, id)
		},
		Sweep:         sweep,
		SweepInterval: r.Service.KServeConfig.ResyncInterval.Std(),
		Concurrency:   1,
		QPS:           r.Service.KServeConfig.SyncQPS,
	})
}
