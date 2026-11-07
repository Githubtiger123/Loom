package repository

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func (s *Store) RegisteredModel(ctx context.Context, groupID, id uint64, lock bool) (*model.RegisteredModel, error) {
	n := &model.RegisteredModel{}
	return n, s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id).First(n).Error
}

func (s *Store) ModelVersion(ctx context.Context, groupID, modelID, id uint64, lock bool) (*model.ModelVersion, error) {
	n := &model.ModelVersion{}
	return n, s.query(ctx, lock).Where("group_id = ? AND model_id = ? AND id = ?", groupID, modelID, id).First(n).Error
}

func (s *Store) ModelArtifact(ctx context.Context, groupID, modelID, versionID, id uint64, lock bool) (*model.ModelArtifact, error) {
	n := &model.ModelArtifact{}
	return n, s.query(ctx, lock).Where("group_id = ? AND model_id = ? AND version_id = ? AND id = ?", groupID, modelID, versionID, id).First(n).Error
}

func (s *Store) ModelArtifactByID(ctx context.Context, groupID, id uint64, lock bool) (*model.ModelArtifact, error) {
	n := &model.ModelArtifact{}
	return n, s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id).First(n).Error
}
