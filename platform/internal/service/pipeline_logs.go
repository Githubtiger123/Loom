package service

import (
	"context"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type PipelineLogReader interface {
	ResolvePipelinePod(context.Context, k8s.PipelinePodRef) (podlog.Target, error)
	OpenPodLogs(context.Context, podlog.Target, podlog.Options) (*podlog.Stream, error)
}

func (s *Service) pipelineLogRun(ctx context.Context, actor, groupID, id uint64) (*model.PipelineRun, error) {
	return s.pipelinePodRun(ctx, actor, groupID, id, "pipeline.logs")
}

func (s *Service) pipelinePodRun(ctx context.Context, actor, groupID, id uint64, permission string) (*model.PipelineRun, error) {
	u, err := s.Store.User(ctx, actor, false)
	if err != nil {
		return nil, domainError(err)
	}
	if u.Status != "ACTIVE" || u.DeletedAt != nil {
		return nil, apperr.New(apperr.CodeForbidden, "用户已停用，不能访问在线容器")
	}
	n, err := s.PipelineRun(ctx, actor, groupID, id)
	if err != nil {
		return nil, err
	}
	if err := mlAllowed(ctx, s.Store, actor, &n.MLIdentity, "pipeline", permission); err != nil {
		return nil, err
	}
	if n.Engine == model.PipelineEngineNativeArgo {
		if n.Cluster != s.Cluster || n.DeletedAt != nil || n.ExecutionCleaned || n.ExternalID == nil {
			return nil, apperr.New(apperr.CodeConflict, "原生执行尚未绑定或已清理")
		}
		return n, nil
	}
	if n.Cluster != s.Cluster || n.DeletedAt != nil || n.ExecutionCleaned || n.ExternalID == nil || *n.ExternalID != n.InstanceID {
		return nil, apperr.New(apperr.CodeConflict, "当前执行尚未就绪或已清理，不能访问在线容器")
	}
	return n, nil
}

func (s *Service) pipelineLogSpace(ctx context.Context, n *model.PipelineRun) error {
	g, err := s.Store.Group(ctx, n.GroupID, false)
	if err != nil {
		return domainError(err)
	}
	if g.DeletedAt != nil || g.Status == "DELETED" || g.Cluster != n.Cluster || g.Name != n.Namespace ||
		g.InstanceID == "" || g.NamespaceUID == nil || g.ProfileUID == nil {
		return apperr.New(apperr.CodeConflict, "执行空间身份不可验证")
	}
	// 不是凭当前 namespace 名称放行，必须匹配该次执行记录的原空间 UID。
	e := &mlExecution{}
	if err := s.DB.WithContext(ctx).Select("group_instance_id", "namespace_uid", "profile_uid").
		Where("instance_id = ? AND target_type = ? AND group_instance_id = ? AND namespace_uid = ? AND profile_uid = ?",
			n.InstanceID, "pipeline_run", g.InstanceID, *g.NamespaceUID, *g.ProfileUID).Take(e).Error; err != nil {
		return apperr.Wrap(apperr.CodeConflict, "缺少匹配原空间身份的执行回执", err)
	}
	return s.PipelineVerifier.VerifyPipelineSpace(ctx, &MLExecutionClaim{
		MLExecutionView: &MLExecutionView{Space: &MLExecutionSpace{InstanceID: g.InstanceID, NamespaceUID: *g.NamespaceUID, ProfileUID: *g.ProfileUID}},
		Command:         MLCommand{GroupID: n.GroupID, Namespace: n.Namespace},
	})
}

func (s *Service) OpenPipelineTaskLogs(ctx context.Context, actor, groupID, id uint64, taskID string, options podlog.Options) (*PodLogSession, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if !validator.IsIdempotencyKey(taskID) {
		return nil, apperr.New(apperr.CodeInvalid, "taskId 无效，请使用 runtime 返回的节点 ID")
	}
	if s.PipelineLogs == nil || s.PipelineVerifier == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "尚未装配 Pipeline Pod 日志客户端")
	}
	duration := 15 * time.Second
	if options.Follow {
		duration = podlog.MaxStreamDuration
	}
	readCtx, cancel := context.WithTimeout(ctx, duration)
	keep := false
	defer func() {
		if !keep {
			cancel()
		}
	}()
	n, err := s.pipelineLogRun(readCtx, actor, groupID, id)
	if err != nil {
		return nil, err
	}
	if err := s.pipelineLogSpace(readCtx, n); err != nil {
		return nil, podLogReadError(err)
	}
	ref, err := s.pipelineTaskPodRef(readCtx, actor, groupID, id, n, taskID, options.Container)
	if err != nil {
		return nil, err
	}
	target, err := s.PipelineLogs.ResolvePipelinePod(readCtx, ref)
	if err != nil {
		return nil, err
	}
	ref.Pod = target.Pod // 后续复核固定该 Pod，不反复扫描，也不切换到另一次执行。
	stream, err := s.PipelineLogs.OpenPodLogs(readCtx, target, options)
	if err != nil {
		return nil, err
	}
	session := &PodLogSession{Stream: stream, Cancel: cancel, Ctx: readCtx}
	session.Check = func(checkCtx context.Context) error {
		current, err := s.pipelineLogRun(checkCtx, actor, groupID, id)
		if err != nil {
			return err
		}
		if current.InstanceID != n.InstanceID || current.Revision != n.Revision || current.Namespace != n.Namespace ||
			current.ExternalID == nil || *current.ExternalID != *n.ExternalID {
			return apperr.New(apperr.CodeConflict, "日志读取期间执行实例或版本已变化")
		}
		if err := s.pipelineLogSpace(checkCtx, current); err != nil {
			return podLogReadError(err)
		}
		fresh, err := s.PipelineLogs.ResolvePipelinePod(checkCtx, ref)
		if err != nil {
			return err
		}
		if fresh != target {
			return apperr.New(apperr.CodeConflict, "日志读取期间 Pod UID 已变化")
		}
		return stream.Check(checkCtx)
	}
	if err := session.Check(readCtx); err != nil {
		session.Close()
		return nil, err
	}
	keep = true
	return session, nil
}

// Both logs and exec use KFP's verified task mapping, never an arbitrary client Pod name.
func (s *Service) pipelineTaskPodRef(ctx context.Context, actor, groupID, id uint64, n *model.PipelineRun, taskID, container string) (k8s.PipelinePodRef, error) {
	var empty k8s.PipelinePodRef
	if n.Engine == model.PipelineEngineNativeArgo {
		return s.nativePipelineTaskPodRef(ctx, n, taskID, container)
	}
	if !validator.IsIdempotencyKey(taskID) {
		return empty, apperr.New(apperr.CodeInvalid, "taskId 无效")
	}
	runtime, err := s.PipelineRunRuntime(ctx, actor, groupID, id)
	if err != nil {
		return empty, err
	}
	pod := ""
	executionID := ""
	found := false
	for _, task := range runtime.Tasks {
		if task.ID == taskID {
			if found {
				return empty, apperr.New(apperr.CodeConflict, "KFP 返回重复节点 ID")
			}
			found = true
			pod = task.PodName
			executionID = task.ExecutionID
		}
	}
	if !found || (pod == "" && executionID == "") {
		return empty, apperr.New(apperr.CodeConflict, "节点尚未分配 Pod、命中缓存或为逻辑节点，没有可访问的在线容器")
	}
	ref := k8s.PipelinePodRef{Namespace: n.Namespace, RunID: n.InstanceID, Pod: pod, Container: container}
	if pod == "" {
		ref.ExecutionID = executionID
	}
	return ref, nil
}
