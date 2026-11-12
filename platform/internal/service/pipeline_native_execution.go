package service

import (
	"context"
	"errors"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

func (s *Service) nativeWorkflowPolicy() k8s.NativePipelineWorkflowPolicy {
	p := s.PipelineConfig
	return k8s.NativePipelineWorkflowPolicy{ServiceAccount: p.NativeServiceAccount, Parallelism: p.NativeParallelism, CPU: p.NativeCPU, Memory: p.NativeMemory,
		ArtifactEndpoint: p.NativeArtifactEndpoint, ArtifactBucket: p.NativeArtifactBucket, ArtifactRegion: p.NativeArtifactRegion, ArtifactSecret: p.NativeArtifactSecret, ArtifactInsecure: p.NativeArtifactInsecure, LogArchive: s.NativeLogArchivePolicy}
}

func (s *Service) validateNativeRun(version dto.PipelineVersionSpec, values []dto.MLParameterValue) error {
	if !s.PipelineConfig.NativeEnabled || s.NativePipelineClient == nil || s.PipelineVerifier == nil {
		return apperr.New(apperr.CodeUnavailable, "原生 Pipeline 执行尚未启用")
	}
	plan, report := pipelineir.AnalyzePython(version.PipelineSpec)
	if !report.Compilable {
		return apperr.New(apperr.CodeInvalid, "IR 超出原生 Pipeline 支持范围，请查询 native-compatibility 返回的具体位置与原因")
	}
	parameters, err := pipelineRuntimeParameters(dto.PipelineRunSpec{PipelineVersionSpec: version, Values: values})
	if err != nil {
		return err
	}
	// Pure render preflight: catch the Workflow/object-size and operator-policy
	// limits before accepting a durable operation. No synthetic identity is sent.
	policy := s.nativeWorkflowPolicy()
	policy.Parameters = parameters
	_, _, err = k8s.BuildNativePipelineWorkflow(k8s.PipelineWorkflowRef{GroupID: 1, RunID: 1, InstanceID: "11111111-1111-4111-8111-111111111111", Namespace: "preflight", NamespaceUID: "22222222-2222-4222-8222-222222222222", Name: "loom-11111111-1111-4111-8111-111111111111", PlanHash: plan.Digest()}, plan, policy)
	if err != nil {
		return apperr.New(apperr.CodeInvalid, "原生 Workflow 超过大小限制或执行/存储策略未完整配置")
	}
	return nil
}

// Dispatch from the same durable operation queue, not a second scheduler.
// Each stage re-reads the persisted command and checks the existing live lease.
func (s *Service) executeNativePipelineClaim(ctx context.Context, claim *MLExecutionClaim) error {
	if !s.PipelineConfig.NativeEnabled {
		return apperr.New(apperr.CodeUnavailable, "原生执行未启用")
	}
	if claim.Command.Action != MLPipelineSubmit {
		return s.executeNativeLifecycleClaim(ctx, claim)
	}
	a, err := s.Store.PipelineRunAttempt(ctx, claim.Command.GroupID, claim.Command.ResourceID, claim.InstanceID, false)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if a == nil || a.WorkflowUID == nil {
		if _, err = s.StageNativePipelineSubmission(ctx, claim, s.nativeWorkflowPolicy()); err != nil {
			return s.deferNativeClaim(ctx, claim, err)
		}
	}
	if _, err = s.ResumeNativePipelineSubmission(ctx, claim); err != nil {
		return s.deferNativeClaim(ctx, claim, err)
	}
	err = s.SyncNativePipelineWorkflow(ctx, claim.Command.Namespace, "loom-"+claim.InstanceID)
	if err != nil {
		return s.deferNativeClaim(ctx, claim, err)
	}
	a, err = s.Store.PipelineRunAttempt(ctx, claim.Command.GroupID, claim.Command.ResourceID, claim.InstanceID, false)
	if err != nil {
		return err
	}
	if nativeHasTerminalArchive(a) {
		if err = s.verifyNativeLogArchive(ctx, a); err != nil {
			return s.deferNativeClaim(ctx, claim, err)
		}
		_, err = s.FinalizeNativePipelineSubmission(ctx, claim)
		return err
	}
	return s.deferNativeClaim(ctx, claim, nil)
}

func (s *Service) deferNativeClaim(ctx context.Context, claim *MLExecutionClaim, cause error) error {
	err := repository.InTx(ctx, s.DB, func(st *repository.Store) error {
		o, c, n, e, err := mlLockExecution(ctx, st, claim.OperationID)
		if err != nil {
			return err
		}
		if err := mlLiveExecution(ctx, st, o, c, n, e, s.PipelinePeer(), pipelineLease(claim)); err != nil {
			return err
		}
		run, ok := n.row.(*model.PipelineRun)
		if !ok || run.Engine != model.PipelineEngineNativeArgo {
			return mlExecutionConflict()
		}
		var reason any
		if cause != nil {
			reason = "NATIVE_EXECUTION_RETRY"
			var permanent *k8s.Permanent
			if errors.As(cause, &permanent) {
				reason = "NATIVE_EVIDENCE_CONFLICT"
			}
		}
		r := mlLiveExecutionQuery(ctx, st, o.ID, claim.LeaseOwner).Updates(map[string]any{"status": "RETRYING", "step": "WAIT_NATIVE_STATE", "last_error": reason,
			"lease_owner": nil, "lease_until": nil, "next_attempt_at": gorm.Expr("TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(3))", int((5*time.Second)/time.Second))})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return mlExecutionConflict()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return cause
}
