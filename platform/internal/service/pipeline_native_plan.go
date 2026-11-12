package service

import (
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
)

// Public preflight DTO deliberately excludes the compiled containers, argv,
// input values and future execution credentials. Compilable is not runnable.
type PipelineNativeCompatibility struct {
	VersionID     uint64 `json:"versionId"`
	Revision      uint64 `json:"revision"`
	PackageDigest string `json:"packageDigest"`
	*pipelineir.Report
}

// PipelineVersionNativeCompatibility is read-only. It neither creates an
// attempt nor changes the engine of any historical/current execution.
func (s *Service) PipelineVersionNativeCompatibility(ctx context.Context, actor, groupID, pipelineID, versionID, revision uint64) (*PipelineNativeCompatibility, error) {
	if revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供要检查的版本 revision")
	}
	v, err := s.PipelineVersion(ctx, actor, groupID, pipelineID, versionID)
	if err != nil {
		return nil, err
	}
	if v.Revision != revision {
		return nil, apperr.New(apperr.CodeConflict, "Pipeline 版本已变化，请刷新后重新检查")
	}
	var pack dto.PipelineVersionSpec
	if json.Unmarshal(v.Spec, &pack) != nil {
		return nil, apperr.New(apperr.CodeConflict, "已保存的版本包无效")
	}
	if len(pack.PipelineSpec) > 0 {
		canonical, err := canonicalPipelineIR(pack.PipelineSpec)
		if err != nil || mlHash(canonical) != pack.PackageDigest {
			return nil, apperr.New(apperr.CodeConflict, "版本包与保存的摘要不一致")
		}
	}
	_, report := pipelineir.AnalyzePython(pack.PipelineSpec)
	if report.Compilable && s.PipelineConfig.NativeEnabled {
		report.Runnable = true
		report.ExecutionReason = "CONFIGURED_NOT_RUNTIME_VERIFIED"
	}
	// Parsing does not retain authorization: recheck before returning, and
	// never label a report with a revision that changed during compilation.
	fresh, err := s.PipelineVersion(ctx, actor, groupID, pipelineID, versionID)
	if err != nil {
		return nil, err
	}
	if fresh.Revision != revision || fresh.SpecHash != v.SpecHash {
		return nil, apperr.New(apperr.CodeConflict, "Pipeline 版本已变化，请刷新后重新检查")
	}
	return &PipelineNativeCompatibility{VersionID: v.ID, Revision: v.Revision, PackageDigest: pack.PackageDigest, Report: report}, nil
}
