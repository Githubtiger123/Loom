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

// RunPipeline 使用与外部桥相同的持久领取/授权/fence，实际调用官方 KFP v2 API。
func (r *Runner) RunPipeline(ctx context.Context) error {
	if r.Service == nil || r.Service.PipelineClient == nil || r.Service.PipelineVerifier == nil {
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
