package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// Events are hints, never authority to create identities or commit state.
// Sync must re-read durable identity and use the module's guarded observation path.
type observationSource struct {
	Informer cache.SharedIndexInformer
	MapKeys  func(any) []string
}

type resourceObservation struct {
	Name          string
	Sources       []observationSource
	Sync          func(context.Context, string) error
	Sweep         func(context.Context, func(string)) error
	SweepInterval time.Duration
	Concurrency   int
	QPS           int
	BindEnqueue   func(func(string))
}

// The periodic sweep owns its cursor. Advance only after emitting a successful
// page; keep the cursor on timeout so later rounds cannot starve the tail.
func observationIDSweep(query func(context.Context) *gorm.DB, prefix string) func(context.Context, func(string)) error {
	var after uint64
	return func(ctx context.Context, emit func(string)) error {
		for {
			var ids []uint64
			if err := query(ctx).Where("id > ?", after).Order("id").Limit(100).Pluck("id", &ids).Error; err != nil {
				return err
			}
			for _, id := range ids {
				emit(fmt.Sprint(prefix, id))
				after = id
			}
			if len(ids) < 100 {
				after = 0
				return nil
			}
		}
	}
}

func observationObject(obj any) *unstructured.Unstructured {
	switch value := obj.(type) {
	case *unstructured.Unstructured:
		return value
	case cache.DeletedFinalStateUnknown:
		return observationObject(value.Obj)
	case *cache.DeletedFinalStateUnknown:
		if value != nil {
			return observationObject(value.Obj)
		}
	}
	return nil
}

func observationNamespace(obj any) []string {
	n := observationObject(obj)
	if n == nil || n.GetNamespace() == "" {
		return nil
	}
	return []string{n.GetNamespace()}
}

// runResourceObserver owns informer/queue lifecycle. Callbacks do no I/O; all
// database and API work is bounded, rate limited and retried outside callbacks.
func (r *Runner) runResourceObserver(parent context.Context, cfg resourceObservation) error {
	if cfg.Sync == nil || len(cfg.Sources) == 0 || cfg.Concurrency < 1 || cfg.Concurrency > 16 || cfg.QPS < 1 || cfg.QPS > 100 {
		return fmt.Errorf("invalid %s observer configuration", cfg.Name)
	}
	ctx, cancel := context.WithCancel(parent)
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Second, 5*time.Minute))
	var goroutines sync.WaitGroup
	defer func() { cancel(); queue.ShutDown(); goroutines.Wait() }()
	enqueue := func(key string) {
		if key != "" && ctx.Err() == nil {
			queue.Add(key)
		}
	}
	if cfg.BindEnqueue != nil {
		cfg.BindEnqueue(enqueue)
	}
	checks := make([]cache.InformerSynced, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		if source.Informer == nil || source.MapKeys == nil {
			return fmt.Errorf("invalid %s informer source", cfg.Name)
		}
		mapKeys := source.MapKeys
		push := func(obj any) {
			for _, key := range mapKeys(obj) {
				enqueue(key)
			}
		}
		if _, err := source.Informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: push, UpdateFunc: func(old, current any) { push(old); push(current) }, DeleteFunc: push,
		}); err != nil {
			return err
		}
		checks = append(checks, source.Informer.HasSynced)
		goroutines.Add(1)
		go func(informer cache.SharedIndexInformer) { defer goroutines.Done(); informer.Run(ctx.Done()) }(source.Informer)
	}
	initial, stopInitial := context.WithTimeout(ctx, time.Minute)
	synced := cache.WaitForCacheSync(initial.Done(), checks...)
	stopInitial()
	if !synced {
		if parent.Err() != nil {
			return nil
		}
		return fmt.Errorf("%s informer initial cache sync failed; check APIs and list/watch RBAC", cfg.Name)
	}
	limiter := rate.NewLimiter(rate.Limit(cfg.QPS), 1)
	for range cfg.Concurrency {
		goroutines.Add(1)
		go func() {
			defer goroutines.Done()
			for {
				key, shutdown := queue.Get()
				if shutdown {
					return
				}
				func() {
					defer queue.Done(key)
					if limiter.Wait(ctx) != nil {
						queue.Forget(key)
						return
					}
					work, finish := context.WithTimeout(ctx, 30*time.Second)
					err := cfg.Sync(work, key)
					finish()
					if err == nil || ctx.Err() != nil {
						queue.Forget(key)
						return
					}
					queue.AddRateLimited(key)
					// Do not log resource payloads, credentials or raw API errors.
					r.Log.Warn("资源状态同步未完成，队列将重试", "observer", cfg.Name)
				}()
			}
		}()
	}
	if cfg.Sweep != nil {
		if cfg.SweepInterval < time.Minute {
			cfg.SweepInterval = time.Minute
		}
		goroutines.Add(1)
		go func() {
			defer goroutines.Done()
			ticker := time.NewTicker(cfg.SweepInterval)
			defer ticker.Stop()
			for {
				sweepCtx, finish := context.WithTimeout(ctx, 30*time.Second)
				err := cfg.Sweep(sweepCtx, enqueue)
				finish()
				if err != nil && ctx.Err() == nil {
					r.Log.Warn("资源低频对账投递失败", "observer", cfg.Name)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	r.Log.Info("资源 Informer 观测已启动", "observer", cfg.Name)
	<-ctx.Done()
	return nil
}
