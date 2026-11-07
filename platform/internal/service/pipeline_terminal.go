package service

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/k8s/podexec"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	"github.com/githubtiger123/loom/platform/internal/model"
)

type PipelineTerminalClient interface {
	ResolvePipelinePod(context.Context, k8s.PipelinePodRef) (podlog.Target, error)
	ResolvePodTerminal(context.Context, podexec.Target) (podexec.Target, error)
	RunPodTerminal(context.Context, podexec.Target, string, io.Reader, io.Writer, podexec.SizeQueue) error
}

type PodTerminalSession struct {
	Target podexec.Target
	Check  func(context.Context) error
	Run    func(context.Context, io.Reader, io.Writer, podexec.SizeQueue) error
	Finish func(context.Context, string) error
}

func (s *Service) pipelineTerminalRun(ctx context.Context, actor, groupID, id uint64) (*model.PipelineRun, error) {
	n, err := s.pipelinePodRun(ctx, actor, groupID, id, "pipeline.exec")
	if err != nil {
		return nil, err
	}
	g, err := s.Store.Group(ctx, groupID, false)
	if err != nil {
		return nil, domainError(err)
	}
	if g.Status != "ACTIVE" || g.DeletedAt != nil || n.DesiredState != "RUNNING" || len(n.PendingSpec) != 0 {
		return nil, apperr.New(apperr.CodeConflict, "空间非 ACTIVE 或运行正在停止、删除、编辑，拒绝终端操作")
	}
	if err := s.pipelineLogSpace(ctx, n); err != nil {
		return nil, podLogReadError(err)
	}
	return n, nil
}

func (s *Service) PreparePipelineTerminal(ctx context.Context, actor, groupID, id uint64, taskID, container, shell string) (*PodTerminalSession, error) {
	if err := podexec.ValidateShell(shell); err != nil {
		return nil, err
	}
	if s.PipelineTerminal == nil || s.PipelineVerifier == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "尚未装配 Pipeline 终端客户端")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	n, err := s.pipelineTerminalRun(ctx, actor, groupID, id)
	if err != nil {
		return nil, err
	}
	ref, err := s.pipelineTaskPodRef(ctx, actor, groupID, id, n, taskID, container)
	if err != nil {
		return nil, err
	}
	pod, err := s.PipelineTerminal.ResolvePipelinePod(ctx, ref)
	if err != nil {
		return nil, err
	}
	ref.Pod = pod.Pod
	target, err := s.PipelineTerminal.ResolvePodTerminal(ctx, podexec.Target{Namespace: pod.Namespace, Pod: pod.Pod, PodUID: pod.PodUID, Container: pod.Container})
	if err != nil {
		return nil, err
	}
	session := &PodTerminalSession{Target: target}
	session.Check = func(ctx context.Context) error {
		current, err := s.pipelineTerminalRun(ctx, actor, groupID, id)
		if err != nil {
			return err
		}
		if current.InstanceID != n.InstanceID || current.Revision != n.Revision || current.Namespace != n.Namespace {
			return apperr.New(apperr.CodeConflict, "终端会话的执行实例或版本已变化")
		}
		fresh, err := s.PipelineTerminal.ResolvePipelinePod(ctx, ref)
		if err != nil {
			return err
		}
		if fresh != pod {
			return apperr.New(apperr.CodeConflict, "终端 Pod 身份已变化")
		}
		_, err = s.PipelineTerminal.ResolvePodTerminal(ctx, target)
		return err
	}
	if err := session.Check(ctx); err != nil {
		return nil, err
	}
	targetID := fmt.Sprintf("%d/%s/%s/%s", id, n.InstanceID, target.PodUID, target.Container)
	// Fail closed if the admission audit cannot be saved; never store shell input/output.
	if err := audit(ctx, s.Store, actor, groupID, "pipeline.exec.open", "pipeline_run", targetID, "", "ACCEPTED"); err != nil {
		return nil, err
	}
	session.Run = func(ctx context.Context, stdin io.Reader, stdout io.Writer, sizes podexec.SizeQueue) error {
		checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
		err := session.Check(checkCtx)
		checkCancel()
		if err != nil {
			return err
		}
		return s.PipelineTerminal.RunPodTerminal(ctx, target, shell, stdin, stdout, sizes)
	}
	session.Finish = func(ctx context.Context, reason string) error {
		return audit(ctx, s.Store, actor, groupID, "pipeline.exec.close", "pipeline_run", targetID, "", reason)
	}
	return session, nil
}
