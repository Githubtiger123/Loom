package worker

import (
	"context"
	"time"

	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

// RunMaintenance 不写 Kubernetes；授权到期和观测调度都先写持久操作。
func (r *Runner) RunMaintenance(ctx context.Context) error {
	r.Log.Info("授权到期与访问对账调度启动", "cluster", r.Cluster)
	for ctx.Err() == nil {
		workCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := r.Service.MaintainAccess(workCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			r.Log.Error("权限定时维护失败", "err", err)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 5*time.Second)
		err = repository.New(r.Service.DB).CleanupAuthRecords(cleanupCtx)
		cleanupCancel()
		if err != nil && ctx.Err() == nil {
			r.Log.Error("认证记录清理失败", "err", err)
		}
		staleCtx, staleCancel := context.WithTimeout(ctx, 5*time.Second)
		err = r.expireNotebookObservations(staleCtx)
		staleCancel()
		if err != nil && ctx.Err() == nil {
			r.Log.Error("Notebook 观测时效维护失败", "err", err)
		}
		if !pause(ctx, 15*time.Second) {
			break
		}
	}
	return nil
}

// 即使观测进程离线，也不无限保留 RUNNING 就绪投影。不更新 observed_at，不释放额度，不自动停止。
func (r *Runner) expireNotebookObservations(ctx context.Context) error {
	return r.DB.WithContext(ctx).Model(&model.Notebook{}).
		Where("cluster = ? AND deleted_at IS NULL AND observed_at < TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(3))", r.Cluster).
		Where("status IN ?", []string{"RUNNING", "STOPPED", "ERROR", "UNKNOWN"}).
		Where("NOT EXISTS (SELECT 1 FROM tb_operation o WHERE o.target_type = 'notebook' AND o.target_key = CAST(tb_notebook.id AS CHAR) AND o.status IN ('PENDING','RUNNING','RETRYING'))").
		Updates(map[string]any{"status": "UNKNOWN", "observed_state": "STALE", "status_reason": "OBSERVATION_STALE: 最近可信观测已超过两分钟"}).Error
}
