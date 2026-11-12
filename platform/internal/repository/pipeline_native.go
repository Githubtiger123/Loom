package repository

import (
	"context"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/model"
)

// Node/attempt reads include group, run and instance scope. Artifacts are scoped
// to the group because they may be shared by multiple authorized executions.
// Callers still authorize the resource; repository methods do not grant access.
func (s *Store) PipelineRunAttempt(ctx context.Context, groupID, runID uint64, instanceID string, lock bool) (*model.PipelineRunAttempt, error) {
	n := &model.PipelineRunAttempt{}
	return n, s.query(ctx, lock).Where("group_id = ? AND run_id = ? AND instance_id = ?", groupID, runID, instanceID).First(n).Error
}

func (s *Store) PipelineTaskExecution(ctx context.Context, groupID, runID uint64, instanceID string, id uint64, lock bool) (*model.PipelineTaskExecution, error) {
	n := &model.PipelineTaskExecution{}
	return n, s.query(ctx, lock).Where("group_id = ? AND run_id = ? AND instance_id = ? AND id = ?", groupID, runID, instanceID, id).First(n).Error
}

func (s *Store) PipelineTaskExecutionByNodeKey(ctx context.Context, groupID, runID uint64, instanceID, nodeKey string, lock bool) (*model.PipelineTaskExecution, error) {
	n := &model.PipelineTaskExecution{}
	return n, s.query(ctx, lock).Where("group_id = ? AND run_id = ? AND instance_id = ? AND node_key = ?", groupID, runID, instanceID, nodeKey).First(n).Error
}

// Limit+1 follows the existing cursor convention. Never load an unbounded
// workflow's node JSON into memory in a single repository list operation.
func (s *Store) PipelineTaskExecutions(ctx context.Context, groupID, runID uint64, instanceID string, after uint64, limit int) ([]model.PipelineTaskExecution, error) {
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("pipeline task page limit must be between 1 and 200")
	}
	rows := []model.PipelineTaskExecution{}
	err := s.query(ctx, false).Where("group_id = ? AND run_id = ? AND instance_id = ? AND id > ?", groupID, runID, instanceID, after).
		Order("id").Limit(limit + 1).Find(&rows).Error
	return rows, err
}

func (s *Store) PipelineArtifact(ctx context.Context, groupID, id uint64, lock bool) (*model.PipelineArtifact, error) {
	n := &model.PipelineArtifact{}
	return n, s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id).First(n).Error
}

func (s *Store) PipelineArtifactByRegistrationKey(ctx context.Context, groupID uint64, key string, lock bool) (*model.PipelineArtifact, error) {
	n := &model.PipelineArtifact{}
	return n, s.query(ctx, lock).Where("group_id = ? AND registration_key = ?", groupID, key).First(n).Error
}

func (s *Store) PipelineArtifactsByProducer(ctx context.Context, groupID uint64, instanceID string, taskID, after uint64, limit int) ([]model.PipelineArtifact, error) {
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("pipeline artifact page limit must be between 1 and 200")
	}
	rows := []model.PipelineArtifact{}
	err := s.query(ctx, false).Where("group_id = ? AND producer_instance_id = ? AND producer_task_id = ? AND id > ?", groupID, instanceID, taskID, after).
		Order("id").Limit(limit + 1).Find(&rows).Error
	return rows, err
}
