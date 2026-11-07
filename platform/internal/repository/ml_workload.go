package repository

import (
	"context"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

func (s *Store) Pipeline(ctx context.Context, groupID, id uint64, lock bool) (*model.Pipeline, error) {
	p := &model.Pipeline{}
	return p, s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id).First(p).Error
}

func (s *Store) PipelineVersion(ctx context.Context, groupID, pipelineID, id uint64) (*model.PipelineVersion, error) {
	v := &model.PipelineVersion{}
	return v, s.query(ctx, false).Where("group_id = ? AND pipeline_id = ? AND id = ?", groupID, pipelineID, id).First(v).Error
}

func (s *Store) PipelineRun(ctx context.Context, groupID, id uint64, lock bool) (*model.PipelineRun, error) {
	r := &model.PipelineRun{}
	return r, s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id).First(r).Error
}

func (s *Store) TrainerJob(ctx context.Context, groupID, id uint64, lock bool) (*model.TrainerJob, error) {
	j := &model.TrainerJob{}
	return j, s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id).First(j).Error
}

func (s *Store) KServeService(ctx context.Context, groupID, id uint64, lock bool) (*model.KServeService, error) {
	k := &model.KServeService{}
	return k, s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id).First(k).Error
}

func (s *Store) KServeRevision(ctx context.Context, groupID, serviceID, id uint64) (*model.KServeRevision, error) {
	r := &model.KServeRevision{}
	return r, s.query(ctx, false).Where("group_id = ? AND service_id = ? AND id = ?", groupID, serviceID, id).First(r).Error
}

// MLList always scopes the resource to its group. Model selection comes only from
// the service's fixed switch, never a request-provided table name.
func (s *Store) MLList(ctx context.Context, target any, groupID, actor uint64, q dto.MLListQuery) *gorm.DB {
	db := s.query(ctx, false).Model(target).Where("group_id = ? AND id > ?", groupID, q.After)
	if !q.IncludeDeleted {
		db = db.Where("deleted_at IS NULL")
	}
	if q.Scope == "mine" {
		db = db.Where("created_by = ?", actor)
	} else if q.Scope == "unowned" {
		db = db.Where("created_by IS NULL")
	}
	return db.Order("id").Limit(q.Limit + 1)
}

// Compare-and-swap remains mandatory even when the service already holds a row lock.
func (s *Store) UpdateML(ctx context.Context, target any, groupID, id, revision uint64, fields map[string]any) error {
	r := s.query(ctx, false).Model(target).Where("group_id = ? AND id = ? AND revision = ?", groupID, id, revision).Updates(fields)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) HasMLOperation(ctx context.Context, targetType string, id uint64) (bool, error) {
	var count int64
	err := s.query(ctx, false).Model(&model.Operation{}).Where("target_type = ? AND target_key = ? AND status IN ?", targetType, strconv.FormatUint(id, 10), []string{"PENDING", "RUNNING", "RETRYING"}).Count(&count).Error
	return count != 0, err
}

// SupersedeMLOperations invalidates prior leases as well as queued work. External
// adapters must hold the target lock and check the new revision before/after I/O.
func (s *Store) SupersedeMLOperations(ctx context.Context, targetType string, id uint64) error {
	return s.query(ctx, false).Model(&model.Operation{}).Where("target_type = ? AND target_key = ? AND status IN ?", targetType, strconv.FormatUint(id, 10), []string{"PENDING", "RUNNING", "RETRYING"}).Updates(map[string]any{
		"status": "SUPERSEDED", "step": "SUPERSEDED", "lease_owner": nil, "lease_until": nil, "finished_at": gorm.Expr("UTC_TIMESTAMP(3)"),
	}).Error
}

func (s *Store) MLHistory(ctx context.Context, groupID uint64, targetType string, id, after uint64, limit int) ([]model.MLWorkloadHistory, error) {
	rows := []model.MLWorkloadHistory{}
	err := s.query(ctx, false).Where("group_id = ? AND target_type = ? AND target_id = ? AND id > ?", groupID, targetType, id, after).Order("id").Limit(limit + 1).Find(&rows).Error
	return rows, err
}
