package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/service"
	"gorm.io/gorm"
)

func (r *Runner) deleteWorkspace(ctx context.Context, c *repository.Claim, guard func() error) error {
	var cmd service.WorkspaceDeleteCommand
	if err := json.Unmarshal(c.Payload, &cmd); err != nil || cmd.WorkspaceID == 0 || cmd.Revision != c.TargetVersion || c.TargetKey != fmt.Sprint(cmd.WorkspaceID) {
		return &k8s.Permanent{Reason: "工作目录删除操作契约无效"}
	}
	read := func(st *repository.Store, lock bool) (*model.Workspace, *model.Group, error) {
		g, err := st.Group(ctx, cmd.GroupID, lock)
		if err != nil {
			return nil, nil, err
		}
		w, err := st.Workspace(ctx, cmd.GroupID, cmd.WorkspaceID, lock)
		if err != nil {
			return nil, nil, err
		}
		if w.Cluster != r.Cluster || w.Namespace != cmd.Namespace || w.InstanceID != cmd.InstanceID ||
			w.Revision != cmd.Revision || w.Status != "DELETING" || w.Retained || w.Ownership != "EXCLUSIVE" ||
			g.InstanceID != cmd.GroupInstanceID || !sameLifecycleUID(g.NamespaceUID, cmd.NamespaceUID) ||
			w.PVCName != cmd.PVCName || !sameLifecycleUID(w.PVCUID, cmd.PVCUID) {
			return nil, nil, errSuperseded
		}
		ids, err := st.WorkspaceNotebookIDs(ctx, w)
		if err != nil {
			return nil, nil, err
		}
		if len(ids) > 0 {
			return nil, nil, &k8s.Permanent{Reason: "目录仍存在实例引用，拒绝删除"}
		}
		return w, g, nil
	}
	w, g, err := read(repository.New(r.DB), false)
	if err != nil {
		return err
	}
	check := func() error {
		if err := guard(); err != nil {
			return err
		}
		_, _, err := read(repository.New(r.DB), false)
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if err := r.Kube.DeleteRegisteredWorkspace(ctx, k8s.WorkspaceRef{Space: spaceRef(g), InstanceID: w.InstanceID, PVCName: w.PVCName, PVCUID: *w.PVCUID}, check); err != nil {
		return err
	}
	return repository.InTx(ctx, r.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		if err := st.Fence(ctx, c); err != nil {
			return err
		}
		if _, _, err := read(st, true); err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(&model.Workspace{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status": "DELETED", "deleted_at": gorm.Expr("UTC_TIMESTAMP(3)"), "observed_at": gorm.Expr("UTC_TIMESTAMP(3)"),
		}).Error; err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"workspaceId": w.ID, "revision": w.Revision, "status": "DELETED"})
		if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", c.ID).Update("result", string(data)).Error; err != nil {
			return err
		}
		return st.Finish(ctx, c, "SUCCEEDED", "COMPLETED", "", 0, false)
	})
}
