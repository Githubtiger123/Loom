package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

type PipelineNativeCompatibility struct {
	VersionID     uint64 `json:"versionId"`
	Revision      uint64 `json:"revision"`
	PackageDigest string `json:"packageDigest"`
	*kfpengine.Report
}

func officialRuntimeUnavailable() error {
	return apperr.New(apperr.CodeUnavailable, "官方 Pipeline 编译框架已接入，driver/launcher 的受限 Runtime API 与数据库适配尚未完成，暂不接受原生执行")
}

func nativeFinalConflict() error {
	return apperr.New(apperr.CodeConflict, "Pipeline 执行持久记录或产物身份不一致")
}

func (s *Service) officialCompileRequest(version dto.PipelineVersionSpec, values []dto.MLParameterValue, identity kfpengine.Identity) (kfpengine.Request, error) {
	canonical, err := canonicalPipelineIR(version.PipelineSpec)
	if err != nil || mlHash(canonical) != version.PackageDigest {
		return kfpengine.Request{}, apperr.New(apperr.CodeConflict, "Pipeline 版本包与摘要不一致")
	}
	parameters, err := pipelineRuntimeParameters(dto.PipelineRunSpec{PipelineVersionSpec: version, Values: values})
	if err != nil {
		return kfpengine.Request{}, err
	}
	p := s.PipelineConfig
	root := strings.TrimRight(p.OfficialPipelineRoot, "/") + fmt.Sprintf("/%d/%s/%s", identity.GroupID, identity.NamespaceUID, identity.InstanceID)
	return kfpengine.Request{PipelineIR: canonical, Parameters: parameters, Identity: identity, Options: kfpengine.Options{
		DriverImage: p.OfficialDriverImage, LauncherImage: p.OfficialLauncherImage, ServiceAccount: p.NativeServiceAccount,
		PipelineRoot: root, Parallelism: p.NativeParallelism, CacheDisabled: true,
	}}, nil
}

// Read-only compilation: does not start Python, create a Workflow or mutate a
// version. Configured images do not imply authenticated runtime readiness.
func (s *Service) PipelineVersionNativeCompatibility(ctx context.Context, actor, groupID, pipelineID, versionID, revision uint64) (*PipelineNativeCompatibility, error) {
	if revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供版本 revision")
	}
	version, err := s.PipelineVersion(ctx, actor, groupID, pipelineID, versionID)
	if err != nil {
		return nil, err
	}
	if version.Revision != revision {
		return nil, apperr.New(apperr.CodeConflict, "Pipeline 版本已变化，请刷新")
	}
	var pack dto.PipelineVersionSpec
	if json.Unmarshal(version.Spec, &pack) != nil {
		return nil, nativeFinalConflict()
	}
	report := kfpengine.Inspect(pack.PipelineSpec)
	if len(report.Issues) == 0 {
		identity := kfpengine.Identity{GroupID: groupID, RunID: 1, InstanceID: "11111111-1111-4111-8111-111111111111", Namespace: "preflight", NamespaceUID: "22222222-2222-4222-8222-222222222222"}
		request, requestErr := s.officialCompileRequest(pack, nil, identity)
		if requestErr != nil {
			return nil, requestErr
		}
		if s.PipelineConfig.OfficialDriverImage == "" || s.PipelineConfig.OfficialLauncherImage == "" || s.PipelineConfig.OfficialPipelineRoot == "" {
			report.ExecutionReason = "OFFICIAL_COMPILER_POLICY_NOT_CONFIGURED"
		} else if result, err := kfpengine.Compile(ctx, request); err != nil {
			report.Issues = append(report.Issues, kfpengine.Issue{Code: "OFFICIAL_COMPILATION_REJECTED", Path: "/"})
		} else {
			report.ValidationMode, report.Compilable, report.PlanDigest = "OFFICIAL_ARGOCOMPILER", true, result.WorkflowDigest
		}
	}
	// Runtime capabilities are not inferred from successful YAML generation.
	report.Runnable = false
	fresh, err := s.PipelineVersion(ctx, actor, groupID, pipelineID, versionID)
	if err != nil {
		return nil, err
	}
	if fresh.Revision != revision || fresh.SpecHash != version.SpecHash {
		return nil, apperr.New(apperr.CodeConflict, "Pipeline 版本已变化，请刷新")
	}
	return &PipelineNativeCompatibility{VersionID: versionID, Revision: revision, PackageDigest: pack.PackageDigest, Report: report}, nil
}

func (s *Service) validateNativeRun(version dto.PipelineVersionSpec, values []dto.MLParameterValue) error {
	// This initial refactor has no production runtime backend. In particular,
	// do not accept operations and leave them queued behind a missing adapter.
	return officialRuntimeUnavailable()
}

func (s *Service) executeNativePipelineClaim(ctx context.Context, claim *MLExecutionClaim) error {
	if claim == nil || claim.MLExecutionView == nil {
		return mlExecutionInvalid()
	}
	if !s.PipelineConfig.NativeEnabled || s.OfficialPipelineBackend == nil || s.PipelineVerifier == nil {
		return officialRuntimeUnavailable()
	}
	if err := s.OfficialPipelineBackend.Ready(ctx); err != nil {
		return officialRuntimeUnavailable()
	}
	if _, err := s.RenewMLExecution(ctx, s.PipelinePeer(), claim.OperationID, pipelineLease(claim)); err != nil {
		return err
	}
	if err := s.PipelineVerifier.VerifyPipelineSpace(ctx, claim); err != nil {
		return err
	}
	c := claim.Command
	return s.OfficialPipelineBackend.Execute(ctx, kfpengine.Operation{ID: claim.OperationID, GroupID: c.GroupID, RunID: c.ResourceID, InstanceID: c.InstanceID, Action: string(c.Action), Fence: claim.TargetFence, LeaseOwner: claim.LeaseOwner})
}

// Historical native execution is not reinterpreted with a different compiler.
// Business data remains readable; live stop/cleanup/resume needs its engine.
func nativeLifecycleAllowed(ctx context.Context, st *repository.Store, run *model.PipelineRun, action MLAction) error {
	if run.Engine != model.PipelineEngineNativeArgo {
		return nil
	}
	return officialRuntimeUnavailable()
}

func (s *Service) SyncNativePipelineWorkflow(ctx context.Context, namespace, name string) error {
	if s.OfficialPipelineBackend == nil {
		return &k8s.Permanent{Reason: "official runtime backend not installed; no legacy output interpretation is performed"}
	}
	if err := s.OfficialPipelineBackend.Ready(ctx); err != nil {
		return err
	}
	return s.OfficialPipelineBackend.Observe(ctx, namespace, name)
}
