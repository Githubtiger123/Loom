package service

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

func (s *Service) activeKServeArtifact(ctx context.Context, st *repository.Store, actor, groupID, artifactID uint64) (*model.RegisteredModel, *model.ModelVersion, *model.ModelArtifact, error) {
	artifact, err := st.ModelArtifactByID(ctx, groupID, artifactID, false)
	if err != nil {
		return nil, nil, nil, domainError(err)
	}
	registered, err := st.RegisteredModel(ctx, groupID, artifact.ModelID, false)
	if err != nil {
		return nil, nil, nil, domainError(err)
	}
	if err := modelAllowed(ctx, st, actor, registered, "model.read"); err != nil {
		return nil, nil, nil, err
	}
	version, err := st.ModelVersion(ctx, groupID, artifact.ModelID, artifact.VersionID, false)
	if err != nil {
		return nil, nil, nil, domainError(err)
	}
	if registered.Status != "ACTIVE" || registered.DeletedAt != nil || version.Status != "READY" || version.DeletedAt != nil ||
		artifact.Status != "ACTIVE" || artifact.DeletedAt != nil {
		return nil, nil, nil, apperr.New(apperr.CodeConflict, "只能部署 ACTIVE 模型下 READY 版本的有效产物")
	}
	return registered, version, artifact, nil
}

func (s *Service) resolveKServeModelSpec(ctx context.Context, st *repository.Store, actor, groupID uint64, in dto.KServeSpec) (dto.KServeSpec, error) {
	if in.DeploymentType != "MODEL" || in.Model == nil || in.Model.ArtifactID == 0 {
		return in, in.Validate()
	}
	registered, version, artifact, err := s.activeKServeArtifact(ctx, st, actor, groupID, in.Model.ArtifactID)
	if err != nil {
		return dto.KServeSpec{}, err
	}
	resolved := in
	resolved.Model = &dto.KServeModelSpec{
		ArtifactID:         artifact.ID,
		RegisteredModelID:  registered.ID,
		ModelVersionID:     version.ID,
		StorageURI:         artifact.URI,
		StorageType:        artifact.StorageType,
		ModelFormat:        artifact.ModelFormat,
		ModelFormatVersion: artifact.ModelFormatVersion,
		ArtifactDigest:     artifact.Digest,
		ArtifactSizeBytes:  artifact.SizeBytes,
		Runtime:            in.Model.Runtime,
	}
	if err := resolved.Validate(); err != nil {
		return dto.KServeSpec{}, err
	}
	return resolved, nil
}

func sameOptionalSize(a, b *uint64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (s *Service) verifyKServeArtifactSnapshot(ctx context.Context, st *repository.Store, actor, groupID uint64, spec dto.KServeSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.DeploymentType != "MODEL" || spec.Model == nil || spec.Model.ArtifactID == 0 {
		return nil
	}
	registered, version, artifact, err := s.activeKServeArtifact(ctx, st, actor, groupID, spec.Model.ArtifactID)
	if err != nil {
		return err
	}
	m := spec.Model
	if m.RegisteredModelID != registered.ID || m.ModelVersionID != version.ID || m.StorageURI != artifact.URI ||
		m.StorageType != artifact.StorageType || m.ModelFormat != artifact.ModelFormat ||
		m.ModelFormatVersion != artifact.ModelFormatVersion || m.ArtifactDigest != artifact.Digest ||
		!sameOptionalSize(m.ArtifactSizeBytes, artifact.SizeBytes) {
		return apperr.New(apperr.CodeConflict, "模型产物元数据与配置修订快照不一致")
	}
	return nil
}
