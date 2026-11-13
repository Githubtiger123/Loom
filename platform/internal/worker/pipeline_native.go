package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// Informer is the event source, WorkQueue coalesces keys and retries transient
// failures. Both are process-local; neither establishes execution ownership.
func (r *Runner) runNativePipelineObserver(ctx context.Context) error {
	informer := r.Kube.NewPipelineWorkflowInformer()
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Second, 5*time.Minute))
	defer queue.ShutDown()
	enqueue := func(obj any) {
		key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
		if err == nil {
			queue.Add(key)
		}
	}
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: enqueue, UpdateFunc: func(_, current any) { enqueue(current) }, DeleteFunc: enqueue,
	}); err != nil {
		return err
	}
	var watches sync.WaitGroup
	watches.Add(1)
	go func() { defer watches.Done(); informer.Run(ctx.Done()) }()
	// Always wait for informer shutdown, including startup cancellation.
	defer watches.Wait()
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("原生 Workflow Informer 初始缓存同步失败")
	}
	limiter := rate.NewLimiter(rate.Limit(r.Service.PipelineConfig.SyncQPS), 1)
	var workers sync.WaitGroup
	for range r.Service.PipelineConfig.SyncConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				key, shutdown := queue.Get()
				if shutdown {
					return
				}
				func() {
					defer queue.Done(key)
					if err := limiter.Wait(ctx); err != nil {
						queue.Forget(key)
						return
					}
					namespace, name, err := cache.SplitMetaNamespaceKey(key)
					if err != nil || namespace == "" || name == "" {
						queue.Forget(key)
						return
					}
					workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					err = r.Service.SyncNativePipelineWorkflow(workCtx, namespace, name)
					cancel()
					if err == nil || ctx.Err() != nil {
						queue.Forget(key)
						return
					}
					var permanent *k8s.Permanent
					if errors.As(err, &permanent) || apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
						// A later informer resync can try again after an operator
						// repairs identity or permissions. Never log payloads/tokens.
						queue.Forget(key)
						r.Log.Warn("原生 Workflow 观测被拒绝，等待配置修复", "namespace", namespace, "workflow", name)
						return
					}
					queue.AddRateLimited(key)
					r.Log.Warn("原生 Workflow 状态写回未完成，队列将重试", "namespace", namespace, "workflow", name)
				}()
			}
		}()
	}
	r.Log.Info("官方 Workflow Informer 观测已启动", "cluster", r.Cluster, "submissionEnabled", r.Service.PipelineConfig.NativeEnabled)
	<-ctx.Done()
	queue.ShutDown()
	workers.Wait()
	return nil
}
