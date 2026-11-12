package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/dto"
	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/service"
)

// RunPipeline keeps KFP execution and the opt-in native observer in the same
// existing role. The observer never enables native submission implicitly.
func (r *Runner) RunPipeline(ctx context.Context) error {
	if r.Service == nil {
		return fmt.Errorf("pipeline-worker 需要装配业务服务")
	}
	native := r.Service.PipelineConfig.NativeObserverEnabled
	kfp := r.Service.PipelineClient != nil
	executor := kfp || r.Service.PipelineConfig.NativeEnabled
	if !native && !executor {
		return fmt.Errorf("pipeline-worker 需要启用 KFP 或原生 Workflow 观测")
	}
	if native {
		if r.Kube == nil || r.Service.NativePipelineClient == nil || r.Service.PipelineVerifier == nil {
			return fmt.Errorf("原生 Workflow 观测需要 Kubernetes 客户端和空间核验器")
		}
		concurrency, qps := r.Service.PipelineConfig.SyncConcurrency, r.Service.PipelineConfig.SyncQPS
		if concurrency < 1 || concurrency > 16 || qps < 1 || qps > 100 {
			return fmt.Errorf("原生 Workflow 观测并发或限速配置无效")
		}
		maxOpen, err := inframysql.MaxOpenConnections(r.DB)
		if err != nil {
			return err
		}
		// Each observer holds the existing advisory lock on one connection and
		// can open one short SQL transaction. No new lease system is added.
		need := 2*concurrency + 2
		if executor {
			need += r.Concurrency + concurrency
		}
		if maxOpen != 0 && maxOpen < need {
			return fmt.Errorf("启用原生观测时数据库 max_open 至少为 %d", need)
		}
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 2)
	count := 0
	if executor {
		count++
		go func() { errs <- r.runKFPPipeline(workCtx) }()
	}
	if native {
		count++
		go func() { errs <- r.runNativePipelineObserver(workCtx) }()
	}
	var result error
	for range count {
		err := <-errs
		if err != nil && result == nil {
			result = err
		}
		cancel()
	}
	return result
}

// runKFPPipeline preserves the existing durable operation and polling path.
func (r *Runner) runKFPPipeline(ctx context.Context) error {
	if r.Service == nil || (r.Service.PipelineClient == nil && !r.Service.PipelineConfig.NativeEnabled) || r.Service.PipelineVerifier == nil {
		return fmt.Errorf("pipeline-worker 需要启用 pipeline 并装配官方 KFP 客户端和空间核验器")
	}
	if r.Concurrency < 1 || r.Concurrency > 16 {
		return fmt.Errorf("pipeline-worker 并发必须为 1–16")
	}
	syncConcurrency, syncQPS := r.Service.PipelineConfig.SyncConcurrency, r.Service.PipelineConfig.SyncQPS
	if syncConcurrency < 1 || syncConcurrency > 16 || syncQPS < 1 || syncQPS > 100 {
		return fmt.Errorf("pipeline-worker 状态同步并发或限速配置无效")
	}
	maxOpen, err := inframysql.MaxOpenConnections(r.DB)
	if err != nil {
		return err
	}
	// 每个执行线程的目标锁占用独立连接，还需为领取/续租/回填留出连接。
	if maxOpen != 0 && maxOpen < r.Concurrency+syncConcurrency+2 {
		return fmt.Errorf("数据库 max_open 至少为 pipeline-worker 执行并发 + 状态同步并发 + 2")
	}
	var wg sync.WaitGroup
	// 所有观测协程共享一个 ticker，限制本进程的同步领取/请求速率；不为每个 Run 建 goroutine。
	syncTicks := time.NewTicker(time.Second / time.Duration(syncQPS))
	defer syncTicks.Stop()
	for range syncConcurrency {
		if r.Service.PipelineClient == nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				select {
				case <-ctx.Done():
					return
				case <-syncTicks.C:
				}
				workCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				found, err := r.Service.SyncNextPipelineRun(workCtx)
				cancel()
				if err != nil && ctx.Err() == nil {
					r.Log.Warn("Pipeline 独立状态同步本轮未完成，等待重试")
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
				claim, err := r.Service.ClaimMLExecution(claimCtx, r.Service.PipelinePeer(), dto.ClaimMLExecutionReq{
					Cluster: r.Cluster, Actions: []string{string(service.MLPipelineSubmit), string(service.MLPipelineStop), string(service.MLPipelineDelete), string(service.MLPipelineEdit)},
				})
				cancel()
				if err != nil || claim == nil {
					if err != nil && ctx.Err() == nil {
						r.Log.Warn("KFP 任务领取失败")
					}
					if !pause(ctx, 2*time.Second) {
						return
					}
					continue
				}
				workCtx, finish := context.WithTimeout(ctx, 45*time.Second)
				err = r.Service.ExecutePipelineClaim(workCtx, claim)
				finish()
				if err != nil && ctx.Err() == nil {
					r.Log.Warn("KFP 任务本轮未完成，保留持久状态等待恢复", "operation", claim.OperationID)
				}
			}
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}
