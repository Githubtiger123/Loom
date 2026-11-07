package repository

import (
	"context"
	"errors"
	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

func (s *Store) Workspace(ctx context.Context, groupID, id uint64, lock bool) (*model.Workspace, error) {
	w := &model.Workspace{}
	err := s.query(ctx, lock).Where("group_id=? AND id=?", groupID, id).First(w).Error
	return w, err
}
func (s *Store) Workspaces(ctx context.Context, groupID, after uint64, limit int) ([]model.Workspace, error) {
	rows := []model.Workspace{}
	err := s.query(ctx, false).Where("group_id=? AND id>? AND deleted_at IS NULL", groupID, after).Order("id").Limit(limit).Find(&rows).Error
	return rows, err
}
func (s *Store) WorkspaceNotebookIDs(ctx context.Context, w *model.Workspace) ([]uint64, error) {
	ids := []uint64{}
	err := s.query(ctx, false).Raw(`SELECT id FROM tb_notebook n WHERE n.group_id=? AND
 (EXISTS (SELECT 1 FROM tb_workspace_attachment a WHERE a.workspace_id=? AND a.notebook_id=n.id)
 OR (n.cluster=? AND n.namespace=? AND (n.workspace_pvc=? OR (? IS NOT NULL AND n.workspace_uid=?)))) ORDER BY id`,
		w.GroupID, w.ID, w.Cluster, w.Namespace, w.PVCName, w.PVCUID, w.PVCUID).Scan(&ids).Error
	return ids, err
}
func (s *Store) CurrentNotebookOwner(ctx context.Context, n *model.Notebook) (*uint64, error) {
	o := &model.NotebookOwner{}
	err := s.query(ctx, false).First(o, "notebook_id=?", n.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		n.OwnerRevision = n.Revision
		return n.CreatedBy, nil
	}
	if err != nil {
		return nil, err
	}
	n.OwnerRevision = o.Revision
	return &o.OwnerUserID, nil
}

func (s *Store) HasWorkspaceOperation(ctx context.Context, id uint64) (bool, error) {
	var count int64
	err := s.query(ctx, false).Model(&model.Operation{}).Where("target_type='workspace' AND target_key=? AND status IN ?", id, []string{"PENDING", "RUNNING", "RETRYING"}).Count(&count).Error
	return count > 0, err
}

func (s *Store) FillNotebookWorkspace(ctx context.Context, n *model.Notebook) error {
	var w model.Workspace
	err := s.query(ctx, false).Model(&model.Workspace{}).Select("tb_workspace.*").
		Joins("JOIN tb_workspace_attachment a ON a.workspace_id=tb_workspace.id").
		Where("a.notebook_id=? AND tb_workspace.group_id=?", n.ID, n.GroupID).First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	n.WorkspaceID, n.WorkspaceInstanceID = w.ID, w.InstanceID
	return nil
}
