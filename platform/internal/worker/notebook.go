package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/service"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
)

// NotebookImageResolver 仅供可信组合根注入，不能由请求或 requested_spec 实现。
type NotebookImageResolver interface {
	ResolveNotebookImage(context.Context, string) (string, error)
}

func notebookRef(n *model.Notebook) k8s.NotebookRef {
	return k8s.NotebookRef{ID: n.ID, GroupID: n.GroupID, Revision: n.Revision, InstanceID: n.InstanceID,
		Namespace: n.Namespace, Name: n.Name, ServerType: n.ServerType, UID: notebookString(n.UID), WorkspacePVC: n.WorkspacePVC, WorkspaceUID: notebookString(n.WorkspaceUID), WorkspaceInstanceID: n.WorkspaceInstanceID}
}
func notebookString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func notebookID(id uint64) string { return strconv.FormatUint(id, 10) }

func (r *Runner) notebookTarget(ctx context.Context, st *repository.Store, c *repository.Claim) (*model.Notebook, *service.NotebookCommand, error) {
	var command service.NotebookCommand
	if err := json.Unmarshal(c.Payload, &command); err != nil {
		return nil, nil, &k8s.Permanent{Reason: "Notebook 命令无效"}
	}
	n, err := st.Notebook(ctx, command.GroupID, command.NotebookID, false, true)
	if err != nil {
		return nil, nil, err
	}
	if c.TargetType != "notebook" || c.TargetKey != strconv.FormatUint(n.ID, 10) || c.TargetVersion != n.Revision ||
		!validator.IsNotebookServerType(n.ServerType) || validator.NotebookServerTypeOrDefault(command.ServerType) != n.ServerType ||
		command.Revision != n.Revision || command.InstanceID != n.InstanceID || command.Cluster != n.Cluster || c.Cluster != n.Cluster || n.Cluster != r.Cluster ||
		command.Namespace != n.Namespace || command.Name != n.Name || command.DesiredState != n.DesiredState || n.DeletedAt != nil ||
		n.LastOperationID == nil || *n.LastOperationID != c.ID || (command.UID != nil && notebookString(command.UID) != notebookString(n.UID)) ||
		(command.WorkspaceUID != nil && notebookString(command.WorkspaceUID) != notebookString(n.WorkspaceUID)) ||
		(command.WorkspacePVC != "" && command.WorkspacePVC != n.WorkspacePVC) ||
		(command.WorkspaceID != 0 && (command.WorkspaceID != n.WorkspaceID || command.WorkspaceInstanceID != n.WorkspaceInstanceID)) ||
		(command.WorkspaceInstanceID != "" && command.WorkspaceInstanceID != n.WorkspaceInstanceID) {
		return nil, nil, errSuperseded
	}
	validAction := (c.Action == "notebook.create" || c.Action == "notebook.start") && n.DesiredState == "RUNNING" ||
		c.Action == "notebook.stop" && n.DesiredState == "STOPPED" || c.Action == "notebook.update" && (n.DesiredState == "RUNNING" || n.DesiredState == "STOPPED") ||
		c.Action == "notebook.delete" && n.DesiredState == "DELETED"
	if !validAction || (command.DeleteWorkspace && c.Action != "notebook.delete") {
		return nil, nil, &k8s.Permanent{Reason: "Notebook 操作与期望状态不匹配"}
	}
	return n, &command, nil
}

func (r *Runner) notebook(ctx context.Context, c *repository.Claim, guard func() error) error {
	st := repository.New(r.DB)
	n, command, err := r.notebookTarget(ctx, st, c)
	if err != nil {
		return err
	}
	workspace, err := st.AttachedNotebookWorkspace(ctx, n)
	if err != nil {
		return &k8s.Permanent{Reason: "WORKSPACE_IDENTITY_CONFLICT: " + err.Error()}
	}
	if command.WorkspaceID != 0 && (workspace == nil || command.WorkspaceID != workspace.ID || command.WorkspaceInstanceID != workspace.InstanceID) {
		return errSuperseded
	}
	executionRef := func() k8s.NotebookRef {
		ref := notebookRef(n)
		if workspace != nil {
			ref.WorkspaceInstanceID = workspace.InstanceID
		}
		return ref
	}
	current := func() error {
		if err := guard(); err != nil {
			return err
		}
		fresh, _, err := r.notebookTarget(ctx, st, c)
		if err != nil {
			return err
		}
		if fresh.ServerType != n.ServerType || notebookString(fresh.UID) != notebookString(n.UID) || notebookString(fresh.WorkspaceUID) != notebookString(n.WorkspaceUID) || fresh.WorkspacePVC != n.WorkspacePVC {
			return errSuperseded
		}
		attached, err := st.AttachedNotebookWorkspace(ctx, fresh)
		if err != nil {
			return &k8s.Permanent{Reason: "WORKSPACE_IDENTITY_CONFLICT: " + err.Error()}
		}
		if workspace != nil && (attached == nil || attached.ID != workspace.ID || attached.Revision != workspace.Revision) {
			return errSuperseded
		}
		g, err := st.Group(ctx, n.GroupID, false)
		if err != nil {
			return err
		}
		if g.Cluster != n.Cluster || g.Name != n.Namespace {
			return &k8s.Permanent{Reason: "Notebook 空间身份不匹配"}
		}
		if (n.DesiredState == "RUNNING" || c.Action == "notebook.update") && g.Status != "ACTIVE" {
			return &k8s.Permanent{Reason: "空间非 ACTIVE，不允许创建、启动或更新实例"}
		}
		exists, err := r.Kube.AccessNamespace(ctx, spaceRef(g))
		if err != nil {
			return err
		}
		if !exists && n.DesiredState == "RUNNING" {
			return &k8s.Permanent{Reason: "NOTEBOOK_NAMESPACE_MISSING: 空间 namespace 已消失"}
		}
		return nil
	}
	checkpoint := func(fields map[string]any) error {
		return repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
			if err := tx.LockAuthorization(ctx); err != nil {
				return err
			}
			if _, err := tx.Group(ctx, n.GroupID, true); err != nil {
				return err
			}
			if _, err := tx.NotebookExecutionFence(ctx, c, n); err != nil {
				return err
			}
			return tx.UpdateNotebook(ctx, n.GroupID, n.ID, fields)
		})
	}
	if err := current(); err != nil {
		return err
	}
	if c.Action == "notebook.stop" {
		obs, err := r.Kube.StopNotebook(ctx, executionRef(), current)
		if obs != nil {
			complete := err == nil && obs.State == "STOPPED"
			if saveErr := r.saveNotebookObservation(ctx, c, n, obs, complete); saveErr != nil {
				return saveErr
			}
			if complete {
				return nil
			}
		}
		if err != nil {
			return err
		}
		return &k8s.Pending{Step: "WAIT_NOTEBOOK_STOPPED", Reason: "等待实际 Pod 停止"}
	}
	if c.Action == "notebook.delete" {
		var previous k8s.NotebookObservation
		if len(n.ObservedSpec) > 0 && string(n.ObservedSpec) != "null" {
			if err := json.Unmarshal(n.ObservedSpec, &previous); err != nil {
				return &k8s.Permanent{Reason: "Notebook 历史观测格式无效，不能安全确认删除"}
			}
		}
		err := r.Kube.DeleteNotebook(ctx, executionRef(), &previous, current, func(obs *k8s.NotebookObservation) error {
			if err := r.saveNotebookObservation(ctx, c, n, obs, false); err != nil {
				return err
			}
			n.UID = &obs.UID
			return nil
		})
		if err != nil {
			return err
		}
		if command.DeleteWorkspace {
			if n.WorkspaceOwnership != "EXCLUSIVE" || command.WorkspaceUID == nil || *command.WorkspaceUID != notebookString(n.WorkspaceUID) || command.WorkspacePVC != n.WorkspacePVC {
				return &k8s.Permanent{Reason: "删除命令未绑定 Loom 独占工作目录 UID"}
			}
			if shared, err := st.NotebookWorkspaceReferenced(ctx, n); err != nil {
				return err
			} else if shared {
				return &k8s.Permanent{Reason: "工作目录被其他业务实例引用"}
			}
			if err := r.Kube.DeleteNotebookWorkspace(ctx, executionRef(), current); err != nil {
				return err
			}
		}
		return r.saveNotebookObservation(ctx, c, n, &k8s.NotebookObservation{UID: notebookString(n.UID), Revision: n.Revision, State: "DELETED"}, true)
	}
	var spec dto.CreateNotebookReq
	if err := json.Unmarshal(n.RequestedSpec, &spec); err != nil {
		return &k8s.Permanent{Reason: "Notebook 业务规格无效"}
	}
	if err := spec.Validate(); err != nil {
		return &k8s.Permanent{Reason: "Notebook 业务规格校验失败"}
	}
	if spec.Name != n.Name || spec.ImageID != n.ImageID || spec.ServerType != n.ServerType {
		return &k8s.Permanent{Reason: "Notebook 业务规格与身份不一致"}
	}
	// 新实例由 API 固定快照；之后的目录改动、禁用、删除不隐式更换已有实例镜像。
	image := n.Image
	if n.Image == "" {
		// 仅兼容旧的、尚未解析过地址的记录；首次解析仍需核对类型和启用状态。
		if r.NotebookImageCatalog != nil {
			entry, ok := r.NotebookImageCatalog.Lookup(n.ImageID)
			if !ok || !entry.Enabled || entry.ServerType != n.ServerType {
				return &k8s.Permanent{Reason: "IMAGE_NOT_RESOLVED: imageId 不存在、已禁用或类型不匹配"}
			}
			image = entry.Image
		} else if r.NotebookImageResolver != nil {
			image, err = r.NotebookImageResolver.ResolveNotebookImage(ctx, n.ImageID)
			if err != nil {
				return &k8s.Permanent{Reason: "IMAGE_NOT_RESOLVED: 受信任解析器未能解析 imageId"}
			}
		} else {
			image = r.NotebookImages[n.ImageID]
		}
	}
	if !validator.IsNotebookImageReference(image) {
		return &k8s.Permanent{Reason: "IMAGE_NOT_RESOLVED: 缺少有效的受信任镜像地址"}
	}
	if n.Image == "" {
		if err := checkpoint(map[string]any{"image": image}); err != nil {
			return err
		}
		n.Image = image
	}
	if n.WorkspacePVC == "" {
		if n.UID != nil {
			return &k8s.Permanent{Reason: "WORKSPACE_NOT_ATTACHED: 已有实例解除目录关联后，必须显式关联目录才能启动"}
		}
		// 名称绑定实例 UUID；名称复用时不会接管旧实例保留的数据。
		pvcName := "loom-ws-" + n.InstanceID
		if err := checkpoint(map[string]any{"workspace_pvc": pvcName}); err != nil {
			return err
		}
		n.WorkspacePVC = pvcName
	}
	pvc, err := r.Kube.EnsureNotebookWorkspace(ctx, executionRef(), n.WorkspaceSizeBytes, current)
	if pvc != nil {
		uid := string(pvc.UID)
		if err := repository.InTx(ctx, r.DB, func(tx *repository.Store) error {
			if err := tx.LockAuthorization(ctx); err != nil {
				return err
			}
			if _, err := tx.Group(ctx, n.GroupID, true); err != nil {
				return err
			}
			if _, err := tx.NotebookExecutionFence(ctx, c, n); err != nil {
				return err
			}
			if err := tx.RegisterNotebookWorkspace(ctx, n, uid, pvc.Annotations["loom.ml/instance-id"]); err != nil {
				return err
			}
			return tx.UpdateNotebook(ctx, n.GroupID, n.ID, map[string]any{"workspace_uid": uid, "workspace_ownership": "EXCLUSIVE"})
		}); err != nil {
			return err
		}
		n.WorkspaceUID, n.WorkspaceOwnership = &uid, "EXCLUSIVE"
		// 新建目录的 attachment 在上面的同一事务内才产生；刷新非持久化字段后再执行后续 fence。
		if err := st.FillNotebookWorkspace(ctx, n); err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}
	in := k8s.NotebookInput{Namespace: n.Namespace, Name: n.Name, Group: n.Namespace, Image: n.Image, ImageID: n.ImageID, ServerType: n.ServerType,
		CPU: spec.CPU, Memory: spec.Memory, GPU: spec.GPU, WorkspacePVC: n.WorkspacePVC, WorkspaceSize: spec.WorkspaceSize,
		DisplayName: spec.DisplayName, Description: spec.Description, Labels: spec.Labels}
	if n.CreatedBy != nil {
		in.Creator = strconv.FormatUint(*n.CreatedBy, 10)
	}
	for _, e := range spec.Env {
		in.Env = append(in.Env, k8s.EnvVar{Name: e.Name, Value: e.Value})
	}
	obs, runErr := r.Kube.ReconcileNotebook(ctx, executionRef(), in, n.DesiredState, current)
	if obs != nil && obs.UID != "" {
		complete := runErr == nil && obs.Revision == n.Revision && obs.State == n.DesiredState
		if err := r.saveNotebookObservation(ctx, c, n, obs, complete); err != nil {
			return err
		}
		n.UID = &obs.UID
		if complete {
			return nil
		}
	}
	if runErr != nil {
		return runErr
	}
	if obs != nil && obs.State == "ERROR" {
		return &k8s.Permanent{Reason: "NOTEBOOK_RUNTIME_FAILED: " + obs.Reason}
	}
	reason := "等待 notebook-controller 落实目标版本"
	if obs != nil && obs.Reason != "" {
		reason = obs.Reason
	}
	return &k8s.Pending{Step: "WAIT_NOTEBOOK_" + n.DesiredState, Reason: reason}
}

func (r *Runner) saveNotebookObservation(ctx context.Context, c *repository.Claim, n *model.Notebook, obs *k8s.NotebookObservation, complete bool) error {
	return repository.InTx(ctx, r.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		if _, err := st.Group(ctx, n.GroupID, true); err != nil {
			return err
		}
		fresh, err := st.NotebookExecutionFence(ctx, c, n)
		if err != nil {
			return err
		}
		data, err := json.Marshal(obs)
		if err != nil {
			return err
		}
		fields := map[string]any{"observed_spec": string(data), "observed_state": obs.State, "observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}
		if obs.UID != "" {
			fields["uid"] = obs.UID
		}
		if complete {
			fields["status"], fields["status_reason"], fields["applied_revision"] = obs.State, nil, n.Revision
			switch obs.State {
			case "RUNNING":
				if fresh.StartedAt == nil || fresh.ObservedState != "RUNNING" {
					fields["started_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
				}
			case "STOPPED":
				if fresh.StoppedAt == nil || fresh.ObservedState != "STOPPED" {
					fields["stopped_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
				}
			case "DELETED":
				fields["deleted_at"] = gorm.Expr("UTC_TIMESTAMP(3)")
			}
		}
		if err := st.UpdateNotebook(ctx, n.GroupID, n.ID, fields); err != nil {
			return err
		}
		if !complete {
			return nil
		}
		if obs.State == "STOPPED" || obs.State == "DELETED" {
			if err := st.ReleaseNotebookBudget(ctx, n.ID, n.Revision); err != nil {
				return err
			}
		}
		if obs.State == "DELETED" {
			var command service.NotebookCommand
			if err := json.Unmarshal(c.Payload, &command); err != nil {
				return err
			}
			if err := st.CompleteNotebookWorkspace(ctx, n, command.DeleteWorkspace); err != nil {
				return err
			}
		}
		if obs.State == "RUNNING" && obs.BudgetAccounted {
			if err := st.AccountNotebookBudget(ctx, c.ID); err != nil {
				return err
			}
		}
		result, err := json.Marshal(map[string]any{"notebookId": n.ID, "groupId": n.GroupID, "revision": n.Revision, "appliedRevision": n.Revision,
			"serverType": n.ServerType, "desiredState": n.DesiredState, "status": obs.State, "observedState": obs.State, "executionPending": false})
		if err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", c.ID).Update("result", string(result)).Error; err != nil {
			return err
		}
		return st.Finish(ctx, c, "SUCCEEDED", "COMPLETED", "", 0, false)
	})
}

// finishNotebook 与操作单在同一事务提交失败/等待结果，且不覆盖更新版本或另一实例。
func (r *Runner) finishNotebook(ctx context.Context, st *repository.Store, c *repository.Claim, status, step, message string, delay time.Duration, failed bool) error {
	n, _, err := r.notebookTarget(ctx, st, c)
	if errors.Is(err, errSuperseded) || errors.Is(err, gorm.ErrRecordNotFound) {
		return st.Finish(ctx, c, "SUPERSEDED", "SUPERSEDED", "Notebook 目标已被替代", 0, false)
	}
	if err != nil {
		// 无法解析的命令也要结束，不能永久占据队列；不据此写任何领域对象。
		var fatal *k8s.Permanent
		if errors.As(err, &fatal) {
			return st.Finish(ctx, c, status, step, message, delay, failed)
		}
		return err
	}
	if _, err := st.Group(ctx, n.GroupID, true); err != nil {
		return err
	}
	if _, err := st.NotebookExecutionFence(ctx, c, n); err != nil {
		return err
	}
	fields := map[string]any{"status_reason": message}
	if status == "FAILED" {
		fields["status"] = "FAILED"
	}
	if status != "SUPERSEDED" {
		if err := st.UpdateNotebook(ctx, n.GroupID, n.ID, fields); err != nil {
			return err
		}
	}
	projectedStatus := n.Status
	if status == "FAILED" {
		projectedStatus = "FAILED"
	}
	result, err := json.Marshal(map[string]any{"notebookId": n.ID, "revision": n.Revision, "desiredState": n.DesiredState, "status": projectedStatus, "observedState": n.ObservedState, "appliedRevision": n.AppliedRevision, "executionPending": status != "FAILED" && status != "SUPERSEDED", "reason": message})
	if err != nil {
		return err
	}
	if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ?", c.ID).Update("result", string(result)).Error; err != nil {
		return err
	}
	return st.Finish(ctx, c, status, step, message, delay, failed)
}
