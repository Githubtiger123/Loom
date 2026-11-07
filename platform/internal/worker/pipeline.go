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
	maxOpen, err := inframysql.MaxOpenConnections(r.DB)
	if err != nil {
		return err
	}
	// 每个执行线程的目标锁占用独立连接，还需为领取/续租/回填留出连接。
	if maxOpen != 0 && maxOpen < r.Concurrency+2 {
		return fmt.Errorf("数据库 max_open 至少为 pipeline-worker 并发数 + 2")
	}
	var wg sync.WaitGroup
	for range r.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				claim, err := r.Service.ClaimMLExecution(claimCtx, r.Service.PipelinePeer(), dto.ClaimMLExecutionReq{
					Cluster: r.Cluster, Actions: []string{string(service.MLPipelineSubmit), string(service.MLPipelineStop)},
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
