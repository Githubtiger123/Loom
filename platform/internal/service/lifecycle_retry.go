package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

// 人工重试复用原操作，不能将旧空间实例或已重新关联的目录再次交给执行器。
func prepareLifecycleRetry(ctx context.Context, st *repository.Store, o *model.Operation) error {
	conflict := func() error {
		return apperr.New(apperr.CodeConflict, "生命周期目标已变化，不能重试历史操作")
	}
	switch o.Action {
	case "group.disable", "group.restore", "group.delete", "group.quota.update":
		var command GroupLifecycleCommand
		if json.Unmarshal(o.Payload, &command) != nil || command.GroupID == 0 || o.TargetKey != fmt.Sprint(command.GroupID) || command.Revision != o.TargetVersion {
			return conflict()
		}
		g, err := st.Group(ctx, command.GroupID, true)
		if err != nil {
			return err
		}
		if g.Revision != command.Revision || g.InstanceID != command.InstanceID || g.Cluster != command.Cluster || g.Name != command.Namespace || g.DeletedAt != nil {
			return conflict()
		}
		expected := map[string]string{"group.disable": "DISABLING", "group.restore": "RESTORING", "group.delete": "DELETING"}
		if status, ok := expected[o.Action]; ok && g.Status != status {
			return conflict()
		}
		if o.Action == "group.quota.update" && g.Status != "ACTIVE" && g.Status != "DEGRADED" && g.Status != "DISABLED" {
			return conflict()
		}
	case "workspace.delete":
		var command WorkspaceDeleteCommand
		if json.Unmarshal(o.Payload, &command) != nil || command.WorkspaceID == 0 || o.TargetKey != fmt.Sprint(command.WorkspaceID) || command.Revision != o.TargetVersion {
			return conflict()
		}
		g, err := st.Group(ctx, command.GroupID, true)
		if err != nil {
			return err
		}
		w, err := st.Workspace(ctx, command.GroupID, command.WorkspaceID, true)
		if err != nil {
			return err
		}
		if g.InstanceID != command.GroupInstanceID || w.InstanceID != command.InstanceID || w.Cluster != command.Cluster || w.Revision != command.Revision || w.Status != "DELETING" || w.Retained || w.DeletedAt != nil {
			return conflict()
		}
	}
	return nil
}
