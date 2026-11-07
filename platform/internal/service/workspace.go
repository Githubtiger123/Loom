package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorkspaceDeleteCommand struct {
	WorkspaceID     uint64  `json:"workspaceId"`
	GroupID         uint64  `json:"groupId"`
	InstanceID      string  `json:"instanceId"`
	GroupInstanceID string  `json:"groupInstanceId"`
	Cluster         string  `json:"cluster"`
	Namespace       string  `json:"namespace"`
	NamespaceUID    *string `json:"namespaceUID"`
	PVCName         string  `json:"pvcName"`
	PVCUID          *string `json:"pvcUID"`
	Revision        uint64  `json:"revision"`
}

func workspaceAllowed(ctx context.Context, st *repository.Store, actor uint64, w *model.Workspace, permission string) error {
	c := rbac.Checker{Store: st}
	if err := c.Require(ctx, actor, w.GroupID, permission); err != nil {
		return err
	}
	if w.OwnerUserID != nil && *w.OwnerUserID == actor {
		return nil
	}
	return c.Require(ctx, actor, w.GroupID, "workspace.manage.any")
}
func workspaceGuard(ctx context.Context, actor, groupID, id uint64, permission string) mutationGuard {
	return func(st *repository.Store) error {
		w, err := st.Workspace(ctx, groupID, id, false)
		if err != nil {
			return err
		}
		return workspaceAllowed(ctx, st, actor, w, permission)
	}
}
func writableWorkspace(ctx context.Context, st *repository.Store, groupID, id, revision uint64) (*model.Workspace, error) {
	w, err := st.Workspace(ctx, groupID, id, true)
	if err != nil {
		return nil, err
	}
	if revision == 0 || w.Revision != revision || revision == ^uint64(0) || w.DeletedAt != nil || w.Status == "DELETING" || w.Status == "DELETED" {
		return nil, apperr.New(apperr.CodeConflict, "目录版本已变化或不可修改")
	}
	busy, err := st.HasWorkspaceOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if busy {
		return nil, apperr.New(apperr.CodeConflict, "目录存在未完成操作")
	}
	return w, nil
}
func workspaceReady(w *model.Workspace) bool {
	return w.PVCUID != nil && *w.PVCUID != "" && w.PVCName != "" && w.Ownership == "EXCLUSIVE" && w.ObservedAt != nil && w.DeletedAt == nil && (w.Status == "AVAILABLE" || w.Status == "BOUND")
}
func (s *Service) Workspaces(ctx context.Context, actor, groupID, after uint64, limit int) (*dto.WorkspacePage, error) {
	if limit < 1 || limit > 200 {
		return nil, apperr.New(apperr.CodeInvalid, "limit 必须在 1–200 之间")
	}
	if err := s.Require(ctx, actor, groupID, "workspace.read"); err != nil {
		return nil, err
	}
	// mine 列表不能先分页再过滤，否则可能漏掉用户自己的后续目录。
	anyAllowed, err := s.Store.HasPermission(ctx, actor, groupID, "workspace.manage.any")
	if err != nil {
		return nil, err
	}
	admin, err := s.Store.IsAdmin(ctx, actor)
	if err != nil {
		return nil, err
	}
	q := s.DB.WithContext(ctx).Where("group_id=? AND id>? AND deleted_at IS NULL", groupID, after)
	if !anyAllowed && !admin {
		q = q.Where("owner_user_id=?", actor)
	}
	rows := []model.Workspace{}
	if err := q.Order("id").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	page := &dto.WorkspacePage{Items: []dto.WorkspaceView{}}
	if len(rows) > limit {
		next := rows[limit-1].ID
		page.NextAfter = &next
		rows = rows[:limit]
	}
	for _, w := range rows {
		ids, err := s.Store.WorkspaceNotebookIDs(ctx, &w)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, dto.WorkspaceView{Workspace: w, NotebookIDs: ids})
	}
	return page, nil
}
func (s *Service) Workspace(ctx context.Context, actor, groupID, id uint64) (*dto.WorkspaceView, error) {
	w, err := s.Store.Workspace(ctx, groupID, id, false)
	if err != nil {
		return nil, domainError(err)
	}
	if err := workspaceAllowed(ctx, s.Store, actor, w, "workspace.read"); err != nil {
		return nil, err
	}
	ids, err := s.Store.WorkspaceNotebookIDs(ctx, w)
	if err != nil {
		return nil, err
	}
	return &dto.WorkspaceView{Workspace: *w, NotebookIDs: ids}, nil
}

func (s *Service) DeleteWorkspace(ctx context.Context, actor, groupID, id uint64, key string, in dto.DeleteWorkspaceReq) (*model.Operation, error) {
	if err := in.LifecycleReq.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "workspace.delete", "workspace.delete", "workspace", fmt.Sprint(id), in, func(st *repository.Store, o *model.Operation) error {
		g, err := st.Group(ctx, groupID, true)
		if err != nil {
			return err
		}
		if g.Status != "ACTIVE" && g.Status != "DISABLED" && g.Status != "DEGRADED" {
			return apperr.New(apperr.CodeConflict, "空间当前不可删除目录")
		}
		if g.NamespaceUID == nil || *g.NamespaceUID == "" {
			return apperr.New(apperr.CodeConflict, "空间 namespace 身份尚未确认")
		}
		w, err := writableWorkspace(ctx, st, groupID, id, in.Revision)
		if err != nil {
			return err
		}
		if !workspaceReady(w) || w.Cluster != g.Cluster || w.Namespace != g.Name {
			return apperr.New(apperr.CodeConflict, "未知、共享或身份不匹配的目录不可删除")
		}
		if in.ConfirmName != w.PVCName {
			return apperr.New(apperr.CodeInvalid, "删除目录必须填写完全匹配的 PVC 名称")
		}
		if w.Retained && !in.ReleaseRetention {
			return apperr.New(apperr.CodeConflict, "目录受数据保留保护，需显式解除并确认数据删除")
		}
		ids, err := st.WorkspaceNotebookIDs(ctx, w)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			return apperr.New(apperr.CodeConflict, "目录仍有关联或历史 Notebook 引用，需先安全解除关联")
		}
		w.Revision++
		if err := st.DB.WithContext(ctx).Model(&model.Workspace{}).Where("id=?", id).Updates(map[string]any{"status": "DELETING", "retained": false, "revision": w.Revision}).Error; err != nil {
			return err
		}
		command := WorkspaceDeleteCommand{WorkspaceID: id, GroupID: groupID, InstanceID: w.InstanceID, GroupInstanceID: g.InstanceID, Cluster: g.Cluster, Namespace: g.Name, NamespaceUID: g.NamespaceUID, PVCName: w.PVCName, PVCUID: w.PVCUID, Revision: w.Revision}
		o.Payload, err = json.Marshal(command)
		if err != nil {
			return err
		}
		o.Status, o.Step, o.Cluster, o.TargetVersion = "PENDING", "WAIT_EXECUTOR", g.Cluster, w.Revision
		return result(o, map[string]any{"workspaceId": id, "revision": w.Revision, "status": "DELETING", "reason": in.Reason})
	}, workspaceGuard(ctx, actor, groupID, id, "workspace.delete"))
}

func (s *Service) TransferWorkspaceOwner(ctx context.Context, actor, groupID, id uint64, key string, in dto.TransferResourceReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "workspace.transfer", "workspace.owner.transfer", "workspace", fmt.Sprint(id), in, func(st *repository.Store, o *model.Operation) error {
		if _, err := notebookGroup(ctx, st, groupID, false); err != nil {
			return err
		}
		if err := requireActiveMember(ctx, st, groupID, in.UserID); err != nil {
			return err
		}
		w, err := writableWorkspace(ctx, st, groupID, id, in.Revision)
		if err != nil {
			return err
		}
		if !workspaceReady(w) {
			return apperr.New(apperr.CodeConflict, "仅可转移归属已确认的独占目录")
		}
		ids, err := st.WorkspaceNotebookIDs(ctx, w)
		if err != nil {
			return err
		}
		for _, nid := range ids {
			n, err := st.Notebook(ctx, groupID, nid, true, true)
			if err != nil {
				return err
			}
			owner, err := st.CurrentNotebookOwner(ctx, n)
			if err != nil {
				return err
			}
			if owner == nil || *owner != in.UserID || !confirmedStopped(n) {
				return apperr.New(apperr.CodeConflict, "关联实例需已停止且先交接给相同目标用户，或先安全解除关联")
			}
		}
		old := w.OwnerUserID
		if old == nil || *old != in.UserID {
			w.Revision++
			if err := st.DB.WithContext(ctx).Model(&model.Workspace{}).Where("id=?", id).Updates(map[string]any{"owner_user_id": in.UserID, "revision": w.Revision}).Error; err != nil {
				return err
			}
		}
		o.TargetVersion, o.Cluster = w.Revision, w.Cluster
		return result(o, map[string]any{"workspaceId": id, "previousOwnerUserId": old, "ownerUserId": in.UserID, "revision": w.Revision, "reason": in.Reason})
	}, workspaceGuard(ctx, actor, groupID, id, "workspace.transfer"))
}

// AttachWorkspaceForNotebook 是 createNotebook 的事务 hook。调用前 n.ID 已分配，
// 但未提交；调用方必须随后将修改后的 n PVC 字段持久化。不可在提交后补关联。
func (s *Service) AttachWorkspaceForNotebook(ctx context.Context, st *repository.Store, actor, workspaceID, workspaceRevision uint64, n *model.Notebook) error {
	g, err := st.Group(ctx, n.GroupID, true)
	if err != nil {
		return err
	}
	if g.Status != "ACTIVE" || n.ID == 0 || n.Cluster != g.Cluster || n.Namespace != g.Name {
		return apperr.New(apperr.CodeConflict, "实例/空间不可关联目录")
	}
	w, err := writableWorkspace(ctx, st, n.GroupID, workspaceID, workspaceRevision)
	if err != nil {
		return err
	}
	if err := workspaceAllowed(ctx, st, actor, w, "workspace.attach"); err != nil {
		return err
	}
	owner, err := st.CurrentNotebookOwner(ctx, n)
	if err != nil {
		return err
	}
	if owner == nil {
		return apperr.New(apperr.CodeConflict, "实例缺少有效负责人")
	}
	if err := requireActiveMember(ctx, st, n.GroupID, *owner); err != nil {
		return err
	}
	if !workspaceReady(w) || w.Cluster != g.Cluster || w.Namespace != g.Name || w.OwnerUserID == nil || *w.OwnerUserID != *owner || w.CapacityBytes < n.WorkspaceSizeBytes {
		return apperr.New(apperr.CodeConflict, "目录归属、容量或独占状态不满足复用条件")
	}
	if n.WorkspacePVC != "" && n.WorkspacePVC != w.PVCName {
		return apperr.New(apperr.CodeConflict, "实例已有其他工作目录，不能隐式替换数据")
	}
	ids, err := st.WorkspaceNotebookIDs(ctx, w)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id != n.ID {
			return apperr.New(apperr.CodeConflict, "独占目录仍被其他实例引用")
		}
	}
	if err := st.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.WorkspaceAttachment{WorkspaceID: w.ID, NotebookID: n.ID}).Error; err != nil {
		return err
	}
	var attachment model.WorkspaceAttachment
	if err := st.DB.WithContext(ctx).Where("notebook_id=?", n.ID).First(&attachment).Error; err != nil {
		return err
	}
	if attachment.WorkspaceID != w.ID {
		return apperr.New(apperr.CodeConflict, "实例已关联其他目录，不能覆盖")
	}
	if err := st.DB.WithContext(ctx).Model(&model.Workspace{}).Where("id=?", w.ID).Updates(map[string]any{"status": "BOUND", "revision": w.Revision + 1}).Error; err != nil {
		return err
	}
	n.WorkspacePVC, n.WorkspaceUID, n.WorkspaceOwnership, n.WorkspaceSizeBytes = w.PVCName, w.PVCUID, "EXCLUSIVE", w.CapacityBytes
	n.WorkspaceID, n.WorkspaceInstanceID = w.ID, w.InstanceID
	return nil
}

func (s *Service) AttachWorkspace(ctx context.Context, actor, groupID, id uint64, key string, in dto.WorkspaceActionReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "workspace.attach", "workspace.attach", "workspace", fmt.Sprint(id), in, func(st *repository.Store, o *model.Operation) error {
		if _, err := notebookGroup(ctx, st, groupID, false); err != nil {
			return err
		}
		n, err := writableNotebook(ctx, st, groupID, in.NotebookID, in.NotebookRevision, false)
		if err != nil {
			return err
		}
		if err := notebookCurrentOwnerAllowed(ctx, st, actor, n, "notebook.update"); err != nil {
			return err
		}
		if !confirmedStopped(n) {
			return apperr.New(apperr.CodeConflict, "关联目录前实例必须已确认停止")
		}
		if err := s.AttachWorkspaceForNotebook(ctx, st, actor, id, in.Revision, n); err != nil {
			return err
		}
		if err := st.UpdateNotebook(ctx, groupID, n.ID, map[string]any{"workspace_pvc": n.WorkspacePVC, "workspace_uid": n.WorkspaceUID, "workspace_ownership": n.WorkspaceOwnership, "workspace_size_bytes": n.WorkspaceSizeBytes}); err != nil {
			return err
		}
		o.Cluster = n.Cluster
		return result(o, map[string]any{"workspaceId": id, "notebookId": n.ID, "revision": in.Revision + 1, "notebookRevision": n.Revision, "runtimeApply": "ON_NEXT_START"})
	}, workspaceGuard(ctx, actor, groupID, id, "workspace.attach"))
}

// 解除关联仅改变业务引用，绝不删除 PVC；已删除实例也需观测确认消失。
func (s *Service) DetachWorkspace(ctx context.Context, actor, groupID, id uint64, key string, in dto.WorkspaceActionReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "workspace.attach", "workspace.detach", "workspace", fmt.Sprint(id), in, func(st *repository.Store, o *model.Operation) error {
		g, err := st.Group(ctx, groupID, true)
		if err != nil {
			return err
		}
		if g.Status != "ACTIVE" && g.Status != "DISABLED" && g.Status != "DEGRADED" {
			return apperr.New(apperr.CodeConflict, "空间当前不可解除目录关联")
		}
		w, err := writableWorkspace(ctx, st, groupID, id, in.Revision)
		if err != nil {
			return err
		}
		n, err := st.Notebook(ctx, groupID, in.NotebookID, true, true)
		if err != nil {
			return err
		}
		if err := notebookCurrentOwnerAllowed(ctx, st, actor, n, "notebook.update"); err != nil {
			return err
		}
		if n.Revision != in.NotebookRevision || n.Revision == ^uint64(0) {
			return apperr.New(apperr.CodeConflict, "实例版本已变化")
		}
		busy, err := st.HasNotebookOperation(ctx, n.ID)
		if err != nil {
			return err
		}
		gone := n.DeletedAt != nil && n.DesiredState == "DELETED" && n.ObservedState == "DELETED" && n.AppliedRevision == n.Revision && recentNotebookObservation(n)
		if busy || (!confirmedStopped(n) && !gone) {
			return apperr.New(apperr.CodeConflict, "需确认实例停止/消失且无待执行操作后才能解除关联")
		}
		if n.WorkspacePVC != w.PVCName || n.WorkspaceUID == nil || w.PVCUID == nil || *n.WorkspaceUID != *w.PVCUID {
			return apperr.New(apperr.CodeConflict, "实例与目录身份不匹配")
		}
		if err := st.DB.WithContext(ctx).Where("workspace_id=? AND notebook_id=?", id, n.ID).Delete(&model.WorkspaceAttachment{}).Error; err != nil {
			return err
		}
		if err := st.UpdateNotebook(ctx, groupID, n.ID, map[string]any{"workspace_pvc": "", "workspace_uid": nil, "workspace_ownership": "UNKNOWN"}); err != nil {
			return err
		}
		ids, err := st.WorkspaceNotebookIDs(ctx, w)
		if err != nil {
			return err
		}
		status := "AVAILABLE"
		if len(ids) > 0 {
			status = "BOUND"
		}
		if err := st.DB.WithContext(ctx).Model(&model.Workspace{}).Where("id=?", id).Updates(map[string]any{"revision": w.Revision + 1, "status": status, "retained": true}).Error; err != nil {
			return err
		}
		o.Cluster = g.Cluster
		return result(o, map[string]any{"workspaceId": id, "notebookId": n.ID, "revision": w.Revision + 1, "notebookRevision": n.Revision, "dataPolicy": "RETAIN", "previousPVC": w.PVCName, "previousPVCUID": w.PVCUID, "runtimeApply": "ON_NEXT_START"})
	}, workspaceGuard(ctx, actor, groupID, id, "workspace.attach"))
}

// RegisterNotebookWorkspace 是可信观测器 hook，不是接管任意 PVC 的 API。
// 调用方先验证 Loom 管理标记、namespace/PVC UID、独占归属及容量，持有空间锁。
// used_bytes 保持 NULL；PVC capacity 不是实际文件系统已用量。
func (s *Service) RegisterNotebookWorkspace(ctx context.Context, st *repository.Store, n *model.Notebook) error {
	if n.WorkspaceUID == nil || *n.WorkspaceUID == "" || n.WorkspacePVC == "" || n.WorkspaceOwnership != "EXCLUSIVE" || n.ObservedAt == nil || n.WorkspaceSizeBytes <= 0 {
		return apperr.New(apperr.CodeConflict, "缺少可信独占目录观测")
	}
	g, err := st.Group(ctx, n.GroupID, true)
	if err != nil {
		return err
	}
	if g.Status == "DELETING" || g.Status == "DELETED" || n.Cluster != g.Cluster || n.Namespace != g.Name {
		return apperr.New(apperr.CodeConflict, "空间不可登记目录")
	}
	nativeInstanceID := n.WorkspaceInstanceID
	if nativeInstanceID == "" {
		nativeInstanceID = n.InstanceID
	}
	return st.RegisterNotebookWorkspace(ctx, n, *n.WorkspaceUID, nativeInstanceID)
}

// 工作目录独立转移后，不能凭 Notebook 负责人身份读取另一个人的数据。
func validateNotebookDataOwner(ctx context.Context, st *repository.Store, n *model.Notebook, owner uint64) error {
	if n.WorkspacePVC == "" {
		return nil
	}
	var w model.Workspace
	err := st.DB.WithContext(ctx).Where("group_id=? AND cluster=? AND namespace=? AND pvc_name=? AND deleted_at IS NULL", n.GroupID, n.Cluster, n.Namespace, n.WorkspacePVC).First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if n.CreatedBy != nil && *n.CreatedBy == owner {
			return nil
		}
		return apperr.New(apperr.CodeConflict, "历史目录未登记，转移实例后需先确认并交接数据归属")
	}
	if err != nil {
		return err
	}
	if !workspaceReady(&w) || w.OwnerUserID == nil || *w.OwnerUserID != owner || n.WorkspaceUID == nil || w.PVCUID == nil || *n.WorkspaceUID != *w.PVCUID {
		return apperr.New(apperr.CodeConflict, "工作目录负责人或 PVC 身份与实例不匹配，请先交接目录")
	}
	return nil
}

func protectNotebookWorkspace(ctx context.Context, st *repository.Store, n *model.Notebook) error {
	var w model.Workspace
	err := st.DB.WithContext(ctx).Where("group_id=? AND cluster=? AND namespace=? AND pvc_name=? AND deleted_at IS NULL", n.GroupID, n.Cluster, n.Namespace, n.WorkspacePVC).First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if w.Retained || w.Status == "DELETING" || w.Ownership != "EXCLUSIVE" {
		return apperr.New(apperr.CodeConflict, "目录受保留保护或不可删除，请保留数据删除实例，再通过工作目录接口显式处置")
	}
	owner, err := st.CurrentNotebookOwner(ctx, n)
	if err != nil {
		return err
	}
	if owner == nil || w.OwnerUserID == nil || *owner != *w.OwnerUserID {
		return apperr.New(apperr.CodeConflict, "实例交接不等于数据交接，禁止随实例删除其他负责人的目录")
	}
	return nil
}
