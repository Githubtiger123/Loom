package repository

import (
	"context"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

// Notebook 始终同时约束空间与对象 ID；是否包含已删除历史由调用方显式决定。
func (s *Store) Notebook(ctx context.Context, groupID, id uint64, lock, includeDeleted bool) (*model.Notebook, error) {
	n := &model.Notebook{}
	q := s.query(ctx, lock).Where("group_id = ? AND id = ?", groupID, id)
	if !includeDeleted {
		q = q.Where("deleted_at IS NULL")
	}
	if err := q.First(n).Error; err != nil {
		return n, err
	}
	owner, err := s.CurrentNotebookOwner(ctx, n)
	n.OwnerUserID = owner
	if err == nil {
		err = s.FillNotebookWorkspace(ctx, n)
	}
	return n, err
}

func (s *Store) Notebooks(ctx context.Context, groupID, actor uint64, filter dto.NotebookListQuery) ([]model.Notebook, error) {
	q := s.query(ctx, false).Model(&model.Notebook{}).Where("group_id = ? AND id > ?", groupID, filter.After)
	if !filter.IncludeDeleted {
		q = q.Where("deleted_at IS NULL")
	}
	switch filter.Scope {
	case "mine":
		q = q.Where("COALESCE((SELECT owner_user_id FROM tb_notebook_owner WHERE notebook_id=tb_notebook.id),created_by) = ?", actor)
	case "unowned":
		q = q.Where("COALESCE((SELECT owner_user_id FROM tb_notebook_owner WHERE notebook_id=tb_notebook.id),created_by) IS NULL")
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.ServerType != "" {
		q = q.Where("server_type = ?", filter.ServerType)
	}
	if filter.Search != "" {
		// LOCATE 按字面搜索，避免用户的 %、_ 变成通配符。
		q = q.Where("LOCATE(?, name) > 0 OR LOCATE(?, display_name) > 0", filter.Search, filter.Search)
	}
	notebooks := []model.Notebook{}
	err := q.Order("id").Limit(filter.Limit + 1).Find(&notebooks).Error
	if err != nil {
		return nil, err
	}
	for i := range notebooks {
		owner, ownerErr := s.CurrentNotebookOwner(ctx, &notebooks[i])
		if ownerErr != nil {
			return nil, ownerErr
		}
		notebooks[i].OwnerUserID = owner
		if err := s.FillNotebookWorkspace(ctx, &notebooks[i]); err != nil {
			return nil, err
		}
	}
	return notebooks, err
}

func (s *Store) CreateNotebook(ctx context.Context, n *model.Notebook) error {
	return s.query(ctx, false).Create(n).Error
}

// 调用方已持有空间和实例行锁；maps 确保 0、空字符串和 NULL 均会写入。
func (s *Store) UpdateNotebook(ctx context.Context, groupID, id uint64, fields map[string]any) error {
	return s.query(ctx, false).Model(&model.Notebook{}).Where("group_id = ? AND id = ?", groupID, id).Updates(fields).Error
}

func (s *Store) HasNotebookOperation(ctx context.Context, id uint64) (bool, error) {
	var count int64
	err := s.query(ctx, false).Model(&model.Operation{}).
		Where("target_type = ? AND target_key = ? AND status IN ?", "notebook", strconv.FormatUint(id, 10), []string{"PENDING", "RUNNING", "RETRYING"}).Count(&count).Error
	return count > 0, err
}

// 连已删除实例的保留目录也纳入检查；仅凭相同名称不能证明可安全删除。
func (s *Store) NotebookWorkspaceReferenced(ctx context.Context, n *model.Notebook) (bool, error) {
	var count int64
	err := s.query(ctx, false).Model(&model.Notebook{}).
		Where("id <> ? AND cluster = ? AND namespace = ?", n.ID, n.Cluster, n.Namespace).
		Where("workspace_pvc = ? OR workspace_uid = ?", n.WorkspacePVC, n.WorkspaceUID).Count(&count).Error
	return count > 0, err
}
