package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

// RunObserver 只观测已记录的空间，不重建缺失 Profile，不反向写业务角色。
// 此阶段按对象 GET 轮询，未实现 LIST/WATCH 游标，不伪装成完整资源同步器。
func (r *Runner) RunObserver(ctx context.Context) error {
	if r.Kube == nil {
		return fmt.Errorf("观测器必须配置 Kubernetes 客户端")
	}
	observerCtx, observerCancel := context.WithCancel(ctx)
	observerDone := make(chan struct{})
	go func() { defer close(observerDone); r.runNotebookObserver(observerCtx) }()
	defer func() { observerCancel(); <-observerDone }()
	var after uint64
	r.Log.Info("空间只读观测启动", "cluster", r.Cluster)
	for ctx.Err() == nil {
		ids := []uint64{}
		err := r.DB.WithContext(ctx).Model(&model.Group{}).
			Where("cluster = ? AND id > ? AND profile_uid IS NOT NULL AND status IN ?", r.Cluster, after, []string{"ACTIVE", "DEGRADED"}).
			Order("id").Limit(50).Pluck("id", &ids).Error
		if err != nil {
			r.Log.Error("读取待观测空间失败", "err", err)
			if !pause(ctx, 5*time.Second) {
				break
			}
			continue
		}
		if len(ids) == 0 {
			after = 0
			if !pause(ctx, 30*time.Second) {
				break
			}
			continue
		}
		for _, id := range ids {
			after = id
			workCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			err := r.observeSpace(workCtx, id)
			cancel()
			if err != nil && ctx.Err() == nil {
				r.Log.Warn("空间观测失败", "group", id, "err", err)
			}
		}
	}
	return nil
}

func (r *Runner) observeSpace(ctx context.Context, id uint64) error {
	st := repository.New(r.DB)
	g, err := st.Group(ctx, id, false)
	if err != nil {
		return err
	}
	input, err := profileInput(g)
	if err != nil {
		return err
	}
	obs, observeErr := r.Kube.EnsureSpace(ctx, spaceRef(g), input, false)
	return repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
		fresh, err := tx.Group(ctx, id, true)
		if err != nil {
			return err
		}
		if fresh.Revision != g.Revision || (fresh.Status != "ACTIVE" && fresh.Status != "DEGRADED") {
			return nil
		}
		if obs != nil && obs.ProfileUID != "" {
			if err := tx.RecordSpace(ctx, id, obs.ProfileUID, obs.NamespaceUID, obs.Quota); err != nil {
				return err
			}
		}
		if observeErr != nil {
			return tx.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ?", id).
				Updates(map[string]any{"status": "DEGRADED", "observed_state": "ERROR", "last_error": truncate(observeErr.Error(), 1000), "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error
		}
		// 基础设施恢复不自动掩盖成员撤权失败。
		var issues, completed int64
		receipt := tx.FixedOwnerAccessReceipt(ctx)
		if err := tx.DB.WithContext(ctx).Table("tb_group_access a").Joins("JOIN tb_user u ON u.id = a.user_id").
			Where("a.group_id = ?", id).
			Where("a.status = ? OR (a.status = ? AND NOT COALESCE(u.username = ? AND a.desired_level = ? AND a.applied_version = a.desired_version AND EXISTS (?),0))",
				"FAILED", "DRIFTED", fresh.InfrastructureOwner, "edit", receipt).Count(&issues).Error; err != nil {
			return err
		}
		if err := tx.DB.WithContext(ctx).Model(&model.Operation{}).
			Where("action = ? AND target_key = ? AND status = ?", "group.create", fmt.Sprint(id), "SUCCEEDED").Count(&completed).Error; err != nil {
			return err
		}
		unresolved := issues > 0 || completed == 0
		status := "ACTIVE"
		var reason any
		if unresolved {
			status = "DEGRADED"
			reason = "创建操作尚未完成或成员访问存在异常，请查看操作与访问状态"
		}
		return tx.DB.WithContext(ctx).Model(&model.Group{}).Where("id = ?", id).Updates(map[string]any{
			"observed_state": "READY", "observed_at": gorm.Expr("UTC_TIMESTAMP(3)"), "status": status, "last_error": reason}).Error
	})
}
