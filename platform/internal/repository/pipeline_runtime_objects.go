package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (s *Store) RuntimeObject(ctx context.Context, scope runtimeapi.Scope, id int64, kind string) (*model.PipelineRuntimeObject, error) {
	row := &model.PipelineRuntimeObject{}
	return row, s.query(ctx, true).Where("id = ? AND group_id = ? AND run_id = ? AND instance_id = ? AND kind = ?", id, scope.GroupID, scope.RunID, scope.InstanceID, kind).First(row).Error
}
func (s *Store) RuntimeObjectByKey(ctx context.Context, scope runtimeapi.Scope, kind, key string) (*model.PipelineRuntimeObject, error) {
	row := &model.PipelineRuntimeObject{}
	return row, s.query(ctx, true).Where("group_id = ? AND run_id = ? AND instance_id = ? AND kind = ? AND object_key = ?", scope.GroupID, scope.RunID, scope.InstanceID, kind, key).First(row).Error
}
func (s *Store) RuntimeObjects(ctx context.Context, scope runtimeapi.Scope, kind string) ([]model.PipelineRuntimeObject, error) {
	var rows []model.PipelineRuntimeObject
	err := s.query(ctx, false).Where("group_id = ? AND run_id = ? AND instance_id = ? AND kind = ?", scope.GroupID, scope.RunID, scope.InstanceID, kind).Order("id").Limit(4001).Find(&rows).Error
	if err == nil && len(rows) > 4000 {
		return nil, fmt.Errorf("runtime object limit exceeded")
	}
	return rows, err
}
func (s *Store) SaveRuntimeObject(ctx context.Context, row *model.PipelineRuntimeObject, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > runtimeapi.MaxMessageBytes/2 {
		return fmt.Errorf("runtime object exceeds message limit")
	}
	row.Value = raw
	if row.ID == 0 {
		var count int64
		query := s.DB.WithContext(ctx).Model(&model.PipelineRuntimeObject{}).Where("instance_id = ?", row.InstanceID)
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count >= 16000 {
			return fmt.Errorf("runtime object quota exceeded")
		}
		if row.Kind == "execution" {
			if err := query.Where("kind = ?", "execution").Count(&count).Error; err != nil {
				return err
			}
			if count >= 4000 {
				return fmt.Errorf("runtime execution quota exceeded")
			}
		}
		return s.DB.WithContext(ctx).Create(row).Error
	}
	r := s.DB.WithContext(ctx).Model(&model.PipelineRuntimeObject{}).Where("id = ? AND group_id = ? AND run_id = ? AND instance_id = ?", row.ID, row.GroupID, row.RunID, row.InstanceID).Updates(map[string]any{"value": raw, "updated_at": s.DB.NowFunc()})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		var count int64
		if err := s.DB.WithContext(ctx).Model(&model.PipelineRuntimeObject{}).Where("id = ? AND group_id = ? AND instance_id = ?", row.ID, row.GroupID, row.InstanceID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("runtime object write conflict")
		}
	}
	return nil
}
