package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

func modelAllowed(ctx context.Context, st *repository.Store, actor uint64, n *model.RegisteredModel, permission string) error {
	if err := (rbac.Checker{Store: st}).Require(ctx, actor, n.GroupID, permission); err != nil {
		return err
	}
	if n.CreatedBy != nil && *n.CreatedBy == actor {
		return nil
	}
	return (rbac.Checker{Store: st}).Require(ctx, actor, n.GroupID, "model.read.any")
}

func modelGuard(ctx context.Context, actor, groupID, id uint64, permission string) mutationGuard {
	return func(st *repository.Store) error {
		n, err := st.RegisteredModel(ctx, groupID, id, false)
		if err != nil {
			return err
		}
		return modelAllowed(ctx, st, actor, n, permission)
	}
}

func activeRegisteredModel(n *model.RegisteredModel, revision uint64) error {
	if n.DeletedAt != nil || n.Status != "ACTIVE" {
		return apperr.New(apperr.CodeConflict, "模型已归档")
	}
	if revision == 0 || n.Revision != revision || revision == ^uint64(0) {
		return apperr.New(apperr.CodeConflict, "模型 revision 已变化")
	}
	return nil
}

func activeModelVersion(n *model.ModelVersion, revision uint64) error {
	if n.DeletedAt != nil || n.Status == "ARCHIVED" {
		return apperr.New(apperr.CodeConflict, "模型版本已归档")
	}
	if revision == 0 || n.Revision != revision || revision == ^uint64(0) {
		return apperr.New(apperr.CodeConflict, "模型版本 revision 已变化")
	}
	return nil
}

func (s *Service) Models(ctx context.Context, actor, groupID uint64, q dto.MLListQuery) (*dto.MLPage[model.RegisteredModel], error) {
	if err := s.mlListAllowed(ctx, actor, groupID, "model", &q); err != nil {
		return nil, err
	}
	rows := []model.RegisteredModel{}
	if err := s.Store.MLList(ctx, &model.RegisteredModel{}, groupID, actor, q).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.RegisteredModel) uint64 { return n.ID }), nil
}

func (s *Service) Model(ctx context.Context, actor, groupID, id uint64) (*model.RegisteredModel, error) {
	n, err := s.Store.RegisteredModel(ctx, groupID, id, false)
	if err != nil {
		return nil, domainError(err)
	}
	if err := modelAllowed(ctx, s.Store, actor, n, "model.read"); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Service) ModelVersions(ctx context.Context, actor, groupID, modelID uint64, q dto.MLListQuery) (*dto.MLPage[model.ModelVersion], error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.Model(ctx, actor, groupID, modelID); err != nil {
		return nil, err
	}
	rows := []model.ModelVersion{}
	db := s.DB.WithContext(ctx).Where("group_id = ? AND model_id = ? AND id > ?", groupID, modelID, q.After)
	if !q.IncludeDeleted {
		db = db.Where("deleted_at IS NULL")
	}
	if err := db.Order("id").Limit(q.Limit + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.ModelVersion) uint64 { return n.ID }), nil
}

func (s *Service) ModelVersion(ctx context.Context, actor, groupID, modelID, id uint64) (*model.ModelVersion, error) {
	if _, err := s.Model(ctx, actor, groupID, modelID); err != nil {
		return nil, err
	}
	n, err := s.Store.ModelVersion(ctx, groupID, modelID, id, false)
	return n, domainError(err)
}

func (s *Service) ModelArtifacts(ctx context.Context, actor, groupID, modelID, versionID uint64, q dto.MLListQuery) (*dto.MLPage[model.ModelArtifact], error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.ModelVersion(ctx, actor, groupID, modelID, versionID); err != nil {
		return nil, err
	}
	rows := []model.ModelArtifact{}
	db := s.DB.WithContext(ctx).Where("group_id = ? AND model_id = ? AND version_id = ? AND id > ?", groupID, modelID, versionID, q.After)
	if !q.IncludeDeleted {
		db = db.Where("deleted_at IS NULL")
	}
	if err := db.Order("id").Limit(q.Limit + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.ModelArtifact) uint64 { return n.ID }), nil
}

func (s *Service) ModelArtifact(ctx context.Context, actor, groupID, modelID, versionID, id uint64) (*model.ModelArtifact, error) {
	if _, err := s.ModelVersion(ctx, actor, groupID, modelID, versionID); err != nil {
		return nil, err
	}
	n, err := s.Store.ModelArtifact(ctx, groupID, modelID, versionID, id, false)
	return n, domainError(err)
}

func (s *Service) CreateModel(ctx context.Context, actor, groupID uint64, key string, in dto.CreateModelReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.create", "model.create", "model", fmt.Sprintf("%d/%s", groupID, in.Name), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			n := &model.RegisteredModel{GroupID: groupID, Name: in.Name, DisplayName: in.DisplayName,
				Description: in.Description, CreatedBy: &actor, Revision: 1, Status: "ACTIVE"}
			if err := st.DB.WithContext(ctx).Create(n).Error; err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(n.ID, 10), n.Revision
			return result(o, n)
		})
}

func (s *Service) UpdateModel(ctx context.Context, actor, groupID, id uint64, key string, in dto.UpdateModelReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.update", "model.update", "model", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			n, err := st.RegisteredModel(ctx, groupID, id, true)
			if err != nil {
				return err
			}
			if err := activeRegisteredModel(n, in.Revision); err != nil {
				return err
			}
			fields := map[string]any{"revision": n.Revision + 1}
			if in.DisplayName != nil {
				fields["display_name"] = *in.DisplayName
				n.DisplayName = *in.DisplayName
			}
			if in.Description != nil {
				fields["description"] = *in.Description
				n.Description = *in.Description
			}
			if err := st.UpdateML(ctx, n, groupID, id, n.Revision, fields); err != nil {
				return err
			}
			n.Revision++
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(id, 10), n.Revision
			return result(o, n)
		}, modelGuard(ctx, actor, groupID, id, "model.update"))
}

func (s *Service) ArchiveModel(ctx context.Context, actor, groupID, id uint64, key string, in dto.ArchiveModelObjectReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.archive", "model.archive", "model", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, true)
			if err != nil {
				return err
			}
			n, err := st.RegisteredModel(ctx, groupID, id, true)
			if err != nil {
				return err
			}
			if err := activeRegisteredModel(n, in.Revision); err != nil {
				return err
			}
			now := time.Now().UTC()
			artifacts := st.DB.WithContext(ctx).Model(&model.ModelArtifact{}).
				Where("group_id = ? AND model_id = ? AND deleted_at IS NULL", groupID, id).
				Updates(map[string]any{"status": "ARCHIVED", "deleted_at": now, "revision": gorm.Expr("revision + 1")})
			if artifacts.Error != nil {
				return artifacts.Error
			}
			versions := st.DB.WithContext(ctx).Model(&model.ModelVersion{}).
				Where("group_id = ? AND model_id = ? AND deleted_at IS NULL", groupID, id).
				Updates(map[string]any{"status": "ARCHIVED", "deleted_at": now, "revision": gorm.Expr("revision + 1")})
			if versions.Error != nil {
				return versions.Error
			}
			if err := st.UpdateML(ctx, n, groupID, id, n.Revision,
				map[string]any{"status": "ARCHIVED", "deleted_at": now, "revision": n.Revision + 1}); err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(id, 10), n.Revision+1
			return result(o, map[string]any{"modelId": id, "revision": n.Revision + 1, "status": "ARCHIVED",
				"archivedVersions": versions.RowsAffected, "archivedArtifacts": artifacts.RowsAffected})
		}, modelGuard(ctx, actor, groupID, id, "model.archive"))
}

func (s *Service) CreateModelVersion(ctx context.Context, actor, groupID, modelID uint64, key string, in dto.CreateModelVersionReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.version.create", "model.version.create", "model_version", fmt.Sprintf("%d/%d/%s", groupID, modelID, in.Name), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			parent, err := st.RegisteredModel(ctx, groupID, modelID, true)
			if err != nil {
				return err
			}
			if err := activeRegisteredModel(parent, in.Revision); err != nil {
				return err
			}
			if in.SourcePipelineRunID != nil {
				run, err := st.PipelineRun(ctx, groupID, *in.SourcePipelineRunID, false)
				if err != nil {
					return err
				}
				if err := mlAllowed(ctx, st, actor, &run.MLIdentity, "pipeline", "pipeline.read"); err != nil {
					return err
				}
			}
			n := &model.ModelVersion{GroupID: groupID, ModelID: modelID, Name: in.Name, Description: in.Description,
				SourcePipelineRunID: in.SourcePipelineRunID, CreatedBy: &actor, Revision: 1, Status: "DRAFT"}
			if err := st.DB.WithContext(ctx).Create(n).Error; err != nil {
				return err
			}
			if err := st.UpdateML(ctx, parent, groupID, modelID, parent.Revision, map[string]any{"revision": parent.Revision + 1}); err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(n.ID, 10), n.Revision
			return result(o, map[string]any{"version": n, "modelRevision": parent.Revision + 1})
		}, modelGuard(ctx, actor, groupID, modelID, "model.version.create"))
}

func (s *Service) UpdateModelVersion(ctx context.Context, actor, groupID, modelID, id uint64, key string, in dto.UpdateModelVersionReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.version.update", "model.version.update", "model_version", fmt.Sprintf("%d/%d/%d", groupID, modelID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			parent, err := st.RegisteredModel(ctx, groupID, modelID, true)
			if err != nil {
				return err
			}
			if parent.DeletedAt != nil || parent.Status != "ACTIVE" {
				return apperr.New(apperr.CodeConflict, "模型已归档")
			}
			n, err := st.ModelVersion(ctx, groupID, modelID, id, true)
			if err != nil {
				return err
			}
			if err := activeModelVersion(n, in.Revision); err != nil {
				return err
			}
			if in.Status != nil && *in.Status == "READY" {
				var count int64
				if err := st.DB.WithContext(ctx).Model(&model.ModelArtifact{}).
					Where("group_id = ? AND model_id = ? AND version_id = ? AND status = 'ACTIVE' AND deleted_at IS NULL", groupID, modelID, id).
					Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					return apperr.New(apperr.CodeConflict, "版本至少需要一个有效产物才能标记 READY")
				}
			}
			fields := map[string]any{"revision": n.Revision + 1}
			if in.Description != nil {
				fields["description"] = *in.Description
				n.Description = *in.Description
			}
			if in.Status != nil {
				fields["status"] = *in.Status
				n.Status = *in.Status
			}
			if err := st.UpdateML(ctx, n, groupID, id, n.Revision, fields); err != nil {
				return err
			}
			n.Revision++
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(id, 10), n.Revision
			return result(o, n)
		}, modelGuard(ctx, actor, groupID, modelID, "model.version.update"))
}

func (s *Service) ArchiveModelVersion(ctx context.Context, actor, groupID, modelID, id uint64, key string, in dto.ArchiveModelObjectReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.archive", "model.version.archive", "model_version", fmt.Sprintf("%d/%d/%d", groupID, modelID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, true)
			if err != nil {
				return err
			}
			if _, err := st.RegisteredModel(ctx, groupID, modelID, true); err != nil {
				return err
			}
			n, err := st.ModelVersion(ctx, groupID, modelID, id, true)
			if err != nil {
				return err
			}
			if err := activeModelVersion(n, in.Revision); err != nil {
				return err
			}
			now := time.Now().UTC()
			artifacts := st.DB.WithContext(ctx).Model(&model.ModelArtifact{}).
				Where("group_id = ? AND model_id = ? AND version_id = ? AND deleted_at IS NULL", groupID, modelID, id).
				Updates(map[string]any{"status": "ARCHIVED", "deleted_at": now, "revision": gorm.Expr("revision + 1")})
			if artifacts.Error != nil {
				return artifacts.Error
			}
			if err := st.UpdateML(ctx, n, groupID, id, n.Revision,
				map[string]any{"status": "ARCHIVED", "deleted_at": now, "revision": n.Revision + 1}); err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(id, 10), n.Revision+1
			return result(o, map[string]any{"versionId": id, "revision": n.Revision + 1,
				"status": "ARCHIVED", "archivedArtifacts": artifacts.RowsAffected})
		}, modelGuard(ctx, actor, groupID, modelID, "model.archive"))
}

func (s *Service) CreateModelArtifact(ctx context.Context, actor, groupID, modelID, versionID uint64, key string, in dto.CreateModelArtifactReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.artifact.create", "model.artifact.create", "model_artifact", fmt.Sprintf("%d/%d/%d/%s", groupID, modelID, versionID, in.Name), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			parent, err := st.RegisteredModel(ctx, groupID, modelID, true)
			if err != nil {
				return err
			}
			if parent.DeletedAt != nil || parent.Status != "ACTIVE" {
				return apperr.New(apperr.CodeConflict, "模型已归档")
			}
			version, err := st.ModelVersion(ctx, groupID, modelID, versionID, true)
			if err != nil {
				return err
			}
			if err := activeModelVersion(version, in.Revision); err != nil {
				return err
			}
			n := &model.ModelArtifact{GroupID: groupID, ModelID: modelID, VersionID: versionID, Name: in.Name,
				URI: in.URI, ModelFormat: in.ModelFormat, ModelFormatVersion: in.ModelFormatVersion, Digest: in.Digest,
				SizeBytes: in.SizeBytes, StorageType: in.StorageType, CreatedBy: &actor, Revision: 1, Status: "ACTIVE"}
			if err := st.DB.WithContext(ctx).Create(n).Error; err != nil {
				return err
			}
			if err := st.UpdateML(ctx, version, groupID, versionID, version.Revision, map[string]any{"revision": version.Revision + 1}); err != nil {
				return err
			}
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(n.ID, 10), n.Revision
			return result(o, map[string]any{"artifact": n, "versionRevision": version.Revision + 1})
		}, modelGuard(ctx, actor, groupID, modelID, "model.artifact.create"))
}

func (s *Service) ArchiveModelArtifact(ctx context.Context, actor, groupID, modelID, versionID, id uint64, key string, in dto.ArchiveModelObjectReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "model.archive", "model.artifact.archive", "model_artifact", fmt.Sprintf("%d/%d/%d/%d", groupID, modelID, versionID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			g, err := mlGroup(ctx, st, groupID, true)
			if err != nil {
				return err
			}
			if _, err := st.RegisteredModel(ctx, groupID, modelID, true); err != nil {
				return err
			}
			version, err := st.ModelVersion(ctx, groupID, modelID, versionID, true)
			if err != nil {
				return err
			}
			n, err := st.ModelArtifact(ctx, groupID, modelID, versionID, id, true)
			if err != nil {
				return err
			}
			if n.DeletedAt != nil || n.Status != "ACTIVE" || n.Revision != in.Revision || n.Revision == ^uint64(0) {
				return apperr.New(apperr.CodeConflict, "模型产物已归档或 revision 已变化")
			}
			now := time.Now().UTC()
			if err := st.UpdateML(ctx, n, groupID, id, n.Revision,
				map[string]any{"status": "ARCHIVED", "deleted_at": now, "revision": n.Revision + 1}); err != nil {
				return err
			}
			var remaining int64
			if err := st.DB.WithContext(ctx).Model(&model.ModelArtifact{}).
				Where("group_id = ? AND model_id = ? AND version_id = ? AND status = 'ACTIVE' AND deleted_at IS NULL", groupID, modelID, versionID).
				Count(&remaining).Error; err != nil {
				return err
			}
			versionRevision := version.Revision
			versionStatus := version.Status
			if remaining == 0 && version.Status == "READY" && version.DeletedAt == nil {
				if err := st.UpdateML(ctx, version, groupID, versionID, version.Revision,
					map[string]any{"status": "DRAFT", "revision": version.Revision + 1}); err != nil {
					return err
				}
				versionRevision++
				versionStatus = "DRAFT"
			}
			o.Cluster, o.TargetKey, o.TargetVersion = g.Cluster, strconv.FormatUint(id, 10), n.Revision+1
			return result(o, map[string]any{"artifactId": id, "revision": n.Revision + 1,
				"status": "ARCHIVED", "versionStatus": versionStatus,
				"versionRevision": versionRevision})
		}, modelGuard(ctx, actor, groupID, modelID, "model.archive"))
}
