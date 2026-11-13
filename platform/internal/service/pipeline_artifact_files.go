package service

import (
	"context"
	"strings"
	"time"

	kfpengine "github.com/githubtiger123/loom/engine/kfp"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/artifactstore"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineviz"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type pipelineFileReader interface {
	InventoryOfficial(context.Context, string, string) (*artifactstore.OfficialInventory, error)
	ReadOfficialFile(context.Context, string, string, string, int64) (*artifactstore.Object, error)
}

type PipelineArtifactFiles struct {
	InstanceID     string                       `json:"instanceId"`
	Revision       uint64                       `json:"revision"`
	ArtifactID     string                       `json:"artifactId"`
	Directory      bool                         `json:"directory"`
	ManifestDigest string                       `json:"manifestDigest"`
	Files          []artifactstore.OfficialFile `json:"files"`
}

// Browser supplies only run/artifact identity. Resolve published membership,
// frozen root, exact inventory and authorization on every request, not a URL.
func (s *Service) pipelineFileInventory(ctx context.Context, actor, gid, rid uint64, instance, artifactID string) (*model.PipelineRun, pipelineFileReader, string, *artifactstore.OfficialInventory, error) {
	if !validator.IsCanonicalUUID(instance) || !validator.IsMetadataID(artifactID) {
		return nil, nil, "", nil, apperr.New(apperr.CodeInvalid, "请提供有效的运行实例和产物 ID")
	}
	scope, err := s.readVisualizationScope(ctx, actor, gid, rid, "", instance)
	if err != nil {
		return nil, nil, "", nil, err
	}
	a, member := scope.artifacts[artifactID]
	if !member || a.StorageURI == "" || a.State == "DELETED" || a.State == "MARKED_FOR_DELETION" || a.State == "ABANDONED" {
		return nil, nil, "", nil, apperr.New(apperr.CodeNotFound, "当前运行没有可读取的此产物")
	}
	run := scope.data.run
	if run.Engine != model.PipelineEngineNativeArgo {
		return nil, nil, "", nil, apperr.New(apperr.CodeInvalid, "文件浏览仅支持官方原生执行产物；外部 KFP 可使用原预览或下载入口")
	}
	attempt, err := s.Store.PipelineRunAttempt(ctx, gid, rid, instance, false)
	if err != nil {
		return nil, nil, "", nil, domainError(err)
	}
	if attempt.EngineRevision == nil || *attempt.EngineRevision != kfpengine.EngineRelease {
		return nil, nil, "", nil, apperr.New(apperr.CodeInvalid, "旧版归档不支持逐文件浏览，请使用原下载入口")
	}
	reader, ok := s.PipelineArtifacts.(pipelineFileReader)
	if !ok {
		return nil, nil, "", nil, apperr.New(apperr.CodeUnavailable, "产物文件读取未配置")
	}
	root, uri, err := s.officialArtifactLocation(ctx, run, attempt, artifactID)
	if err != nil {
		return nil, nil, "", nil, err
	}
	inv, err := reader.InventoryOfficial(ctx, root, uri)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if len(inv.Files) > 4096 {
		return nil, nil, "", nil, apperr.New(apperr.CodeQuotaExceeded, "文件清单超过上限")
	}
	for _, f := range inv.Files {
		if !validator.IsArtifactKey(f.Name) || strings.Count(f.Name, "/") >= 64 || f.Size < 0 || f.Size > 9007199254740991 || f.ETag == "" {
			return nil, nil, "", nil, apperr.New(apperr.CodeQuotaExceeded, "文件清单无效或目录超过 64 层")
		}
	}
	return run, reader, root, inv, nil
}

func (s *Service) PipelineArtifactFiles(ctx context.Context, actor, gid, rid uint64, instance, artifactID string) (*PipelineArtifactFiles, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	run, _, _, inv, err := s.pipelineFileInventory(ctx, actor, gid, rid, instance, artifactID)
	if err != nil {
		return nil, err
	}
	if err := s.checkPipelineDAGRead(ctx, actor, run); err != nil {
		return nil, err
	}
	// URI deliberately excluded from the public response.
	return &PipelineArtifactFiles{InstanceID: instance, Revision: run.Revision, ArtifactID: artifactID, Directory: inv.Directory, ManifestDigest: inv.Digest(), Files: inv.Files}, nil
}

func (s *Service) PipelineArtifactFileContent(ctx context.Context, actor, gid, rid uint64, instance, artifactID, name, digest, etag string, download bool) (*PipelineArtifactResult, error) {
	if !validator.IsArtifactKey(name) || !validator.IsSHA256(digest) || etag == "" || len(etag) > 256 {
		return nil, apperr.New(apperr.CodeInvalid, "请使用文件清单返回的相对路径、摘要和 ETag")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	run, reader, root, inv, err := s.pipelineFileInventory(ctx, actor, gid, rid, instance, artifactID)
	if err != nil {
		return nil, err
	}
	if inv.Digest() != digest {
		return nil, apperr.New(apperr.CodeConflict, "目录清单已变化，请刷新后重新选择文件")
	}
	var file *artifactstore.OfficialFile
	for i := range inv.Files {
		if inv.Files[i].Name == name {
			file = &inv.Files[i]
			break
		}
	}
	if file == nil {
		return nil, apperr.New(apperr.CodeNotFound, "该产物没有此文件")
	}
	if file.ETag != etag {
		return nil, apperr.New(apperr.CodeConflict, "文件版本已变化，请刷新目录")
	}
	limit := artifactstore.MaxPreviewBytes
	if download {
		limit = artifactstore.MaxDownloadBytes
	}
	if file.Size > limit {
		return nil, apperr.New(apperr.CodeQuotaExceeded, "文件超过读取上限：预览 2 MiB，下载 32 MiB")
	}
	uri := inv.URI
	if inv.Directory {
		uri = strings.TrimRight(uri, "/") + "/" + file.Name
	}
	obj, err := reader.ReadOfficialFile(ctx, root, uri, etag, limit)
	if err != nil {
		return nil, err
	}
	if obj.ETag != etag || int64(len(obj.Data)) != file.Size {
		return nil, apperr.New(apperr.CodeConflict, "文件内容已变化")
	}
	result := &PipelineArtifactResult{InstanceID: instance, Revision: run.Revision, ArtifactID: artifactID}
	if download {
		result.Object = obj
	} else {
		// Use the selected filename, not the directory's Model/Dataset type.
		result.Preview, err = pipelineviz.ParsePreview(mlmd.Artifact{URI: name}, obj)
		if err != nil {
			return nil, err
		}
	}
	fresh, err := reader.InventoryOfficial(ctx, root, inv.URI)
	if err != nil {
		return nil, err
	}
	if fresh.Digest() != digest {
		return nil, apperr.New(apperr.CodeConflict, "目录在读取时已变化，请刷新")
	}
	if err := s.checkPipelineDAGRead(ctx, actor, run); err != nil {
		return nil, err
	}
	return result, nil
}
