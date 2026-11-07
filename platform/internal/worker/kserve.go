package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/dto"
	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/service"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

func (r *Runner) RunKServe(ctx context.Context) error {
	if r.Service == nil || r.Service.KServeClient == nil || r.Service.KServeVerifier == nil || !r.Service.KServeConfig.Enabled {
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
	if maxOpen != 0 && maxOpen < r.Concurrency+syncConcurrency+2 {
		return fmt.Errorf("数据库 max_open 至少为 kserve-worker 执行并发 + 状态同步并发 + 2")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.watchKServe(ctx)
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
	<-ctx.Done()
	wg.Wait()
	return nil
}

func (r *Runner) watchKServe(ctx context.Context) {
	resourceVersion := ""
	for ctx.Err() == nil {
		stream, err := r.Kube.WatchKServe(ctx, resourceVersion)
		if err != nil {
			r.Log.Warn("KServe Watch 建立失败，周期同步继续工作")
			if !pause(ctx, 5*time.Second) {
				return
			}
			resourceVersion = ""
			continue
		}
		closed := false
		for !closed && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				stream.Stop()
				return
			case event, ok := <-stream.ResultChan():
				if !ok {
					closed = true
					continue
				}
				if event.Type == watch.Error {
					resourceVersion, closed = "", true
					continue
				}
				object, ok := event.Object.(*unstructured.Unstructured)
				if !ok {
					continue
				}
				resourceVersion = object.GetResourceVersion()
				if event.Type == watch.Bookmark {
					continue
				}
				observation := k8s.ObserveKServeObject(object)
				if event.Type == watch.Deleted {
					observation.Absent, observation.Ready = true, false
					observation.State, observation.Reason = "FAILED", "RESOURCE_NOT_FOUND"
				}
				applyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := r.Service.ApplyKServeWatchObservation(applyCtx, observation)
				cancel()
				if err != nil && ctx.Err() == nil {
					r.Log.Warn("KServe Watch 状态写回失败")
				}
			}
		}
		stream.Stop()
		if !pause(ctx, time.Second) {
			return
		}
	}
}
