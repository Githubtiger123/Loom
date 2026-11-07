package repository

import (
	"context"
	"errors"

	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrNotebookSuperseded = errors.New("Notebook 实例或目标版本已变化")

// NotebookExecutionFence 必须在事务中调用：先操作租约、再实例行，禁止旧执行器回填新意图。
func (s *Store) NotebookExecutionFence(ctx context.Context, c *Claim, expected *model.Notebook) (*model.Notebook, error) {
	if err := s.Fence(ctx, c); err != nil {
		return nil, err
	}
	n, err := s.Notebook(ctx, expected.GroupID, expected.ID, true, true)
	if err != nil {
		return nil, err
	}
	if n.InstanceID != expected.InstanceID || n.Cluster != expected.Cluster || n.Namespace != expected.Namespace || n.Name != expected.Name || n.ServerType != expected.ServerType ||
		n.Revision != c.TargetVersion || n.Revision != expected.Revision || n.DesiredState != expected.DesiredState ||
		n.LastOperationID == nil || *n.LastOperationID != c.ID || !sameNotebookUID(n.UID, expected.UID) ||
		n.WorkspacePVC != expected.WorkspacePVC || !sameNotebookUID(n.WorkspaceUID, expected.WorkspaceUID) ||
		n.WorkspaceID != expected.WorkspaceID || n.WorkspaceInstanceID != expected.WorkspaceInstanceID {
		return nil, ErrNotebookSuperseded
	}
	return n, nil
}

func sameNotebookUID(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// RegisterNotebookWorkspace 在实例 fence 事务内调用；唯一键冲突不能用 upsert 接管别的资产。
func (s *Store) RegisterNotebookWorkspace(ctx context.Context, n *model.Notebook, pvcUID, nativeInstanceID string) error {
	w := &model.Workspace{}
	err := s.query(ctx, true).Where("cluster = ? AND namespace = ? AND pvc_name = ? AND deleted_at IS NULL", n.Cluster, n.Namespace, n.WorkspacePVC).First(w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		owner, err := s.CurrentNotebookOwner(ctx, n)
		if err != nil {
			return err
		}
		if nativeInstanceID != n.InstanceID {
			return errors.New("未登记工作目录不能据新 Notebook 引用接管")
		}
		w = &model.Workspace{InstanceID: nativeInstanceID, GroupID: n.GroupID, Cluster: n.Cluster, Namespace: n.Namespace, PVCName: n.WorkspacePVC,
			PVCUID: &pvcUID, OwnerUserID: owner, CapacityBytes: n.WorkspaceSizeBytes, Ownership: "EXCLUSIVE", Retained: true, Status: "BOUND", Revision: 1}
		if err := s.query(ctx, false).Create(w).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if w.InstanceID != nativeInstanceID || w.GroupID != n.GroupID || w.PVCUID == nil || *w.PVCUID != pvcUID || w.Ownership != "EXCLUSIVE" || w.Status == "DELETING" {
		return errors.New("工作目录资产身份不匹配或正在删除")
	}
	if err := s.query(ctx, false).Model(w).Updates(map[string]any{"status": "BOUND", "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}).Error; err != nil {
		return err
	}
	attachment := &model.WorkspaceAttachment{WorkspaceID: w.ID, NotebookID: n.ID}
	if err := s.query(ctx, false).Clauses(clause.OnConflict{DoNothing: true}).Create(attachment).Error; err != nil {
		return err
	}
	var actual model.WorkspaceAttachment
	if err := s.query(ctx, false).Where("notebook_id = ?", n.ID).First(&actual).Error; err != nil {
		return err
	}
	if actual.WorkspaceID != w.ID {
		return errors.New("Notebook 已关联其他目录，不能覆盖 attachment")
	}
	return nil
}

// AttachedNotebookWorkspace 验证业务关联后才允许执行器用原生资产身份挂载复用 PVC。
func (s *Store) AttachedNotebookWorkspace(ctx context.Context, n *model.Notebook) (*model.Workspace, error) {
	var a model.WorkspaceAttachment
	err := s.query(ctx, false).Where("notebook_id = ?", n.ID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w, err := s.Workspace(ctx, n.GroupID, a.WorkspaceID, false)
	if err != nil {
		return nil, err
	}
	if w.DeletedAt != nil || w.Status == "DELETING" || w.Status == "DELETED" || w.Cluster != n.Cluster || w.Namespace != n.Namespace || w.PVCName != n.WorkspacePVC || w.PVCUID == nil || *w.PVCUID != notebookStringUID(n.WorkspaceUID) || w.Ownership != "EXCLUSIVE" {
		return nil, errors.New("工作目录关联身份不匹配或已不可用")
	}
	return w, nil
}

func notebookStringUID(uid *string) string {
	if uid == nil {
		return ""
	}
	return *uid
}

// CompleteNotebookWorkspace 仅在 Notebook/Pod 删除已确认后调用；默认保留资产，显式删 PVC 才删除资产。
func (s *Store) CompleteNotebookWorkspace(ctx context.Context, n *model.Notebook, deleted bool) error {
	var w model.Workspace
	err := s.query(ctx, true).Where("cluster = ? AND namespace = ? AND pvc_name = ? AND pvc_uid = ? AND deleted_at IS NULL", n.Cluster, n.Namespace, n.WorkspacePVC, n.WorkspaceUID).First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.query(ctx, false).Where("workspace_id = ? AND notebook_id = ?", w.ID, n.ID).Delete(&model.WorkspaceAttachment{}).Error; err != nil {
		return err
	}
	fields := map[string]any{"status": "AVAILABLE", "retained": true, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}
	if deleted {
		fields["status"], fields["deleted_at"], fields["retained"] = "DELETED", gorm.Expr("UTC_TIMESTAMP(3)"), false
	}
	return s.query(ctx, false).Model(&w).Updates(fields).Error
}
