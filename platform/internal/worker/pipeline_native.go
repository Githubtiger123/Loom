package worker

import (
	"context"
	"errors"
	"time"

	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
)

// Workflow events share the same queue/lifecycle implementation as the other
// modules. Neither cached objects nor enqueue keys establish execution ownership.
func (r *Runner) runNativePipelineObserver(ctx context.Context) error {
	var after uint64 // sweep-only cursor survives a bounded round timing out
	return r.runResourceObserver(ctx, resourceObservation{
		Name: "pipeline", Concurrency: r.Service.PipelineConfig.SyncConcurrency, QPS: r.Service.PipelineConfig.SyncQPS,
		Sources: []observationSource{{Informer: r.Kube.NewPipelineWorkflowInformer(), MapKeys: func(obj any) []string {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err != nil {
				return nil
			}
			return []string{key}
		}}},
		Sync: func(ctx context.Context, key string) error {
			namespace, name, err := cache.SplitMetaNamespaceKey(key)
			if err != nil || namespace == "" || name == "" {
				return nil
			}
			err = r.Service.SyncNativePipelineWorkflow(ctx, namespace, name)
			var permanent *k8s.Permanent
			if errors.As(err, &permanent) || apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				r.Log.Warn("原生 Workflow 观测被拒绝，等待后续事件或对账", "namespace", namespace, "workflow", name)
				return nil
			}
			return err
		},
		SweepInterval: time.Minute,
		Sweep: func(ctx context.Context, enqueue func(string)) error {
			// Recover durable bindings even when no event survives a restart.
			// Never reinterpret archived or historical engine attempts.
			for {
				var rows []struct {
					ID           uint64
					Namespace    string
					WorkflowName string
				}
				err := r.DB.WithContext(ctx).Table("tb_pipeline_run r").
					Joins("JOIN tb_pipeline_run_attempt a ON a.instance_id = r.instance_id AND a.run_id = r.id AND a.group_id = r.group_id").
					Select("r.id, a.namespace, a.workflow_name").
					Where("r.cluster = ? AND r.engine = ? AND r.deleted_at IS NULL AND r.id > ? AND a.engine = ? AND a.engine_revision = ? AND a.workflow_archived_at IS NULL AND a.workflow_uid IS NOT NULL AND a.workflow_name IS NOT NULL", r.Cluster, model.PipelineEngineNativeArgo, after, model.PipelineEngineNativeArgo, kfpengine.EngineRelease).
					Order("r.id").Limit(100).Scan(&rows).Error
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					after = 0
					return nil
				}
				for _, row := range rows {
					after = row.ID
					if row.Namespace != "" && row.WorkflowName != "" {
						enqueue(row.Namespace + "/" + row.WorkflowName)
					}
				}
			}
		},
	})
}
