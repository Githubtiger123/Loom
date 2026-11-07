package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func notebookRetryStatus(action string) string {
	switch action {
	case "notebook.create":
		return "CREATING"
	case "notebook.update":
		return "UPDATING"
	case "notebook.start":
		return "STARTING"
	case "notebook.stop":
		return "STOPPING"
	case "notebook.delete":
		return "DELETING"
	default:
		return ""
	}
}

// 调用方持有授权串行锁；禁止重试已经被新意图替代或名称重建后的旧实例命令。
func prepareNotebookRetry(ctx context.Context, st *repository.Store, original *model.Operation) error {
	status := notebookRetryStatus(original.Action)
	if original.TargetType != "notebook" || original.Status != "FAILED" || status == "" {
		return apperr.New(apperr.CodeConflict, "只能重试失败的 Notebook 操作")
	}
	var command NotebookCommand
	if err := json.Unmarshal(original.Payload, &command); err != nil {
		return err
	}
	if original.Action == "notebook.stop" || original.Action == "notebook.delete" {
		if _, err := notebookCleanupGroup(ctx, st, command.GroupID); err != nil {
			return err
		}
	} else {
		if _, err := notebookGroup(ctx, st, command.GroupID, false); err != nil {
			return err
		}
	}
	n, err := writableNotebook(ctx, st, command.GroupID, command.NotebookID, command.Revision, true)
	if err != nil {
		return err
	}
	if n.LastOperationID == nil || *n.LastOperationID != original.ID || original.TargetKey != strconv.FormatUint(n.ID, 10) ||
		!validator.IsNotebookServerType(n.ServerType) || validator.NotebookServerTypeOrDefault(command.ServerType) != n.ServerType ||
		original.TargetVersion != n.Revision || original.Cluster != n.Cluster || command.InstanceID != n.InstanceID ||
		command.DesiredState != n.DesiredState || command.Cluster != n.Cluster || command.Namespace != n.Namespace || command.Name != n.Name {
		return apperr.New(apperr.CodeConflict, "操作已过期，不能覆盖实例的最新业务意图")
	}
	if command.DeleteWorkspace {
		if command.WorkspaceUID == nil || n.WorkspaceUID == nil || *command.WorkspaceUID != *n.WorkspaceUID || command.WorkspacePVC != n.WorkspacePVC {
			return apperr.New(apperr.CodeConflict, "原删除操作中的工作目录身份已经变化")
		}
		if err := notebookWorkspaceDeletable(ctx, st, n); err != nil {
			return err
		}
	}
	if err := st.UpdateNotebook(ctx, n.GroupID, n.ID, map[string]any{"status": status, "status_reason": nil}); err != nil {
		return err
	}
	return st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", original.ID).Update("step", "WAIT_EXECUTOR").Error
}

func (s *Service) RetryNotebook(ctx context.Context, actor, groupID, id uint64, key string, in dto.RetryNotebookReq) (*model.Operation, error) {
	if _, err := uuid.Parse(in.OperationID); err != nil || in.Revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision 和有效的 operationId")
	}
	guard := func(st *repository.Store) error {
		n, err := st.Notebook(ctx, groupID, id, false, true)
		if err != nil {
			return err
		}
		original, err := st.Operation(ctx, in.OperationID)
		if err != nil {
			return err
		}
		if original.TargetType != "notebook" || original.TargetKey != strconv.FormatUint(id, 10) || notebookRetryStatus(original.Action) == "" {
			return apperr.New(apperr.CodeConflict, "操作单与实例不匹配")
		}
		return notebookAllowed(ctx, st, actor, n, original.Action)
	}
	_, err := s.mutate(ctx, actor, groupID, key, "notebook.read", "notebook.retry", "notebook", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			original, err := st.Operation(ctx, in.OperationID)
			if err != nil {
				return err
			}
			if original.TargetVersion != in.Revision {
				return apperr.New(apperr.CodeConflict, "操作版本不匹配")
			}
			if err := prepareNotebookRetry(ctx, st, original); err != nil {
				return err
			}
			if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ? AND status = ?", original.ID, "FAILED").
				Updates(map[string]any{"status": "PENDING", "attempt": 0, "next_attempt_at": gorm.Expr("UTC_TIMESTAMP(3)"),
					"deadline_at": nil, "last_error": nil, "lease_owner": nil, "lease_until": nil, "finished_at": nil}).Error; err != nil {
				return err
			}
			o.TargetKey, o.TargetVersion, o.Cluster = original.TargetKey, original.TargetVersion, original.Cluster
			o.Payload = json.RawMessage(`{}`)
			return result(o, map[string]any{"notebookId": id, "operationId": original.ID, "status": "PENDING"})
		}, guard)
	if err != nil {
		return nil, err
	}
	// 返回真正等待执行的操作，而非已完成的"申请重试"审计记录。
	return s.Operation(ctx, actor, in.OperationID)
}
