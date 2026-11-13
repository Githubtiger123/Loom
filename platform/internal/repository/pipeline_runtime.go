package repository

import (
	"context"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

// PipelineRuntimeAttempt resolves a Kubernetes-verified owner, not a caller's
// runId. Multiple matches are a binding conflict, never an arbitrary Take.
func (s *Store) PipelineRuntimeAttempt(ctx context.Context, cluster, namespace, namespaceUID, workflowName, workflowUID string) (*model.PipelineRunAttempt, error) {
	if cluster == "" || namespace == "" || namespaceUID == "" || workflowName == "" || workflowUID == "" {
		return nil, fmt.Errorf("runtime owner identity is incomplete")
	}
	var matches []model.PipelineRunAttempt
	err := s.query(ctx, false).Where("engine = ? AND cluster_id = ? AND namespace = ? AND namespace_uid = ? AND workflow_name = ? AND workflow_uid = ?",
		model.PipelineEngineNativeArgo, cluster, namespace, namespaceUID, workflowName, workflowUID).Limit(2).Find(&matches).Error
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("runtime owner has ambiguous execution bindings")
	}
	return &matches[0], nil
}
