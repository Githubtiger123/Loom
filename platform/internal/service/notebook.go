package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NotebookCommand 是业务到执行层的契约，不包含明文环境变量或任意 PodSpec。
// 执行层必须重新读取 requested_spec，核对版本、实例标识和 UID 后才执行。
type NotebookCommand struct {
	NotebookID          uint64  `json:"notebookId"`
	GroupID             uint64  `json:"groupId"`
	InstanceID          string  `json:"instanceId"`
	Cluster             string  `json:"cluster"`
	Namespace           string  `json:"namespace"`
	Name                string  `json:"name"`
	ServerType          string  `json:"serverType"`
	UID                 *string `json:"uid"`
	Revision            uint64  `json:"revision"`
	DesiredState        string  `json:"desiredState"`
	DeleteWorkspace     bool    `json:"deleteWorkspace"`
	WorkspacePVC        string  `json:"workspacePVC,omitempty"`
	WorkspaceUID        *string `json:"workspaceUID,omitempty"`
	WorkspaceID         uint64  `json:"workspaceId,omitempty"`
	WorkspaceInstanceID string  `json:"workspaceInstanceId,omitempty"`
}

func notebookCommand(n *model.Notebook, deleteWorkspace bool) NotebookCommand {
	return NotebookCommand{NotebookID: n.ID, GroupID: n.GroupID, InstanceID: n.InstanceID,
		Cluster: n.Cluster, Namespace: n.Namespace, Name: n.Name, ServerType: n.ServerType, UID: n.UID,
		Revision: n.Revision, DesiredState: n.DesiredState, DeleteWorkspace: deleteWorkspace,
		WorkspacePVC: n.WorkspacePVC, WorkspaceUID: n.WorkspaceUID, WorkspaceID: n.WorkspaceID, WorkspaceInstanceID: n.WorkspaceInstanceID}
}

func notebookState(n *model.Notebook) dto.NotebookState {
	return dto.NotebookState{Status: n.Status, StatusReason: n.StatusReason,
		DesiredState: n.DesiredState, ObservedState: n.ObservedState, Revision: n.Revision,
		AppliedRevision: n.AppliedRevision, OperationID: n.LastOperationID, ObservedAt: n.ObservedAt,
		LastActiveAt: n.LastActiveAt, StartedAt: n.StartedAt, StoppedAt: n.StoppedAt}
}

func recentNotebookObservation(n *model.Notebook) bool {
	return n.UID != nil && *n.UID != "" && n.ObservedAt != nil &&
		time.Since(*n.ObservedAt) >= -time.Minute && time.Since(*n.ObservedAt) <= 2*time.Minute
}

func notebookView(n *model.Notebook) dto.NotebookView {
	return dto.NotebookView{ID: n.ID, InstanceID: n.InstanceID, GroupID: n.GroupID,
		Name: n.Name, DisplayName: n.DisplayName, Description: n.Description, CreatedBy: n.CreatedBy, OwnerUserID: n.OwnerUserID, OwnerRevision: n.OwnerRevision,
		ImageID: n.ImageID, ServerType: n.ServerType, CPUMilli: n.CPUMilli, MemoryBytes: n.MemoryBytes, GPU: n.GPUCount,
		WorkspaceSizeBytes: n.WorkspaceSizeBytes, WorkspaceID: n.WorkspaceID, WorkspaceOwnership: n.WorkspaceOwnership, IdleStopMinutes: n.IdleStopMinutes,
		NotebookState: notebookState(n), CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, DeletedAt: n.DeletedAt}
}

// notebookAllowed 将模块权限与对象可见性组合；管理员仍经同一权限检查器。
func notebookAllowed(ctx context.Context, st *repository.Store, actor uint64, n *model.Notebook, permission string) error {
	return notebookCurrentOwnerAllowed(ctx, st, actor, n, permission)
}

func notebookGuard(ctx context.Context, actor, groupID, id uint64, permission string) mutationGuard {
	return func(st *repository.Store) error {
		n, err := st.Notebook(ctx, groupID, id, false, true)
		if err != nil {
			return err
		}
		return notebookAllowed(ctx, st, actor, n, permission)
	}
}

func (s *Service) readNotebook(ctx context.Context, actor, groupID, id uint64, permission string) (*model.Notebook, error) {
	if err := s.Require(ctx, actor, groupID, permission); err != nil {
		return nil, err
	}
	n, err := s.Store.Notebook(ctx, groupID, id, false, true)
	if err != nil {
		return nil, domainError(err)
	}
	if err := notebookAllowed(ctx, s.Store, actor, n, permission); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Service) Notebooks(ctx context.Context, actor, groupID uint64, filter dto.NotebookListQuery) (*dto.NotebookPage, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if err := s.Require(ctx, actor, groupID, "notebook.read"); err != nil {
		return nil, err
	}
	if filter.Scope != "mine" {
		if err := s.Require(ctx, actor, groupID, "notebook.read.any"); err != nil {
			return nil, err
		}
	}
	if _, err := s.Store.Group(ctx, groupID, false); err != nil {
		return nil, domainError(err)
	}
	rows, err := s.Store.Notebooks(ctx, groupID, actor, filter)
	if err != nil {
		return nil, err
	}
	page := &dto.NotebookPage{Items: []dto.NotebookView{}}
	if len(rows) > filter.Limit {
		next := rows[filter.Limit-1].ID
		page.NextAfter = &next
		rows = rows[:filter.Limit]
	}
	for i := range rows {
		page.Items = append(page.Items, notebookView(&rows[i]))
	}
	return page, nil
}

func (s *Service) Notebook(ctx context.Context, actor, groupID, id uint64) (*dto.NotebookDetail, error) {
	n, err := s.readNotebook(ctx, actor, groupID, id, "notebook.read")
	if err != nil {
		return nil, err
	}
	var spec dto.CreateNotebookReq
	if err := json.Unmarshal(n.RequestedSpec, &spec); err != nil {
		return nil, err
	}
	detail := &dto.NotebookDetail{NotebookView: notebookView(n), Env: []dto.NotebookEnvView{}, Labels: spec.Labels}
	if detail.Labels == nil {
		detail.Labels = map[string]string{}
	}
	for _, e := range spec.Env {
		detail.Env = append(detail.Env, dto.NotebookEnvView{Name: e.Name, HasValue: e.Value != "", SecretName: e.SecretName, SecretKey: e.SecretKey})
	}
	return detail, nil
}

func (s *Service) NotebookStatus(ctx context.Context, actor, groupID, id uint64) (*dto.NotebookState, error) {
	n, err := s.readNotebook(ctx, actor, groupID, id, "notebook.read")
	if err != nil {
		return nil, err
	}
	state := notebookState(n)
	return &state, nil
}

func notebookGroup(ctx context.Context, st *repository.Store, groupID uint64, allowDegraded bool) (*model.Group, error) {
	g, err := st.Group(ctx, groupID, true)
	if err != nil {
		return nil, err
	}
	if g.DeletedAt != nil || (g.Status != "ACTIVE" && !(allowDegraded && g.Status == "DEGRADED")) {
		return nil, apperr.New(apperr.CodeConflict, "空间尚未就绪或已不可操作")
	}
	return g, nil
}

func (s *Service) CreateNotebook(ctx context.Context, actor, groupID uint64, key string, in dto.CreateNotebookReq) (*model.Operation, error) {
	if s.NotebookImages == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "Notebook 镜像目录尚未配置")
	}
	entry, exists := s.NotebookImages.Lookup(strings.TrimSpace(in.ImageID))
	if !exists {
		return nil, apperr.New(apperr.CodeInvalid, "imageId 不在 Notebook 镜像目录中")
	}
	if strings.TrimSpace(in.ServerType) == "" {
		in.ServerType = entry.ServerType
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if in.ServerType != entry.ServerType {
		return nil, apperr.New(apperr.CodeInvalid, "serverType 与所选镜像类型不匹配")
	}
	snapshot, err := s.prepareNotebookMutationBudget(ctx, actor, groupID, key, "notebook.create")
	if err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, groupID, key, "notebook.create", "notebook.create", "notebook",
		fmt.Sprintf("%d/%s", groupID, in.Name), in, func(st *repository.Store, o *model.Operation) error {
			// 在幂等回放之后判断启用状态，不使已受理请求因目录禁用而失效。
			if !entry.Enabled {
				return apperr.New(apperr.CodeInvalid, "所选镜像已禁用，不允许新建实例")
			}
			g, err := notebookGroup(ctx, st, groupID, false)
			if err != nil {
				return err
			}
			spec, err := json.Marshal(in)
			if err != nil {
				return err
			}
			cpu, _ := validator.QuantityUnits(in.CPU, 1000)
			memory, _ := validator.QuantityUnits(in.Memory, 1)
			workspace, _ := validator.QuantityUnits(in.WorkspaceSize, 1)
			n := &model.Notebook{InstanceID: uuid.NewString(), Cluster: g.Cluster, Namespace: g.Name,
				GroupID: g.ID, Name: in.Name, CreatedBy: &actor, DisplayName: in.DisplayName,
				Description: in.Description, ImageID: in.ImageID, Image: entry.Image, ServerType: in.ServerType, CPUMilli: cpu, MemoryBytes: memory,
				GPUCount: uint(in.GPU), WorkspaceSizeBytes: workspace, IdleStopMinutes: uint(in.IdleStopMinutes),
				RequestedSpec: spec, DesiredState: "RUNNING", Status: "CREATING", ObservedState: "UNKNOWN",
				Revision: 1, LastOperationID: &o.ID}
			// 固定可信镜像配置快照；PVC/CR 身份仍以执行层实际分配和观测为准。
			if err := st.CreateNotebook(ctx, n); err != nil {
				return err
			}
			if in.WorkspaceID != 0 {
				if err := s.AttachWorkspaceForNotebook(ctx, st, actor, in.WorkspaceID, in.WorkspaceRevision, n); err != nil {
					return err
				}
				if err := st.UpdateNotebook(ctx, groupID, n.ID, map[string]any{"workspace_pvc": n.WorkspacePVC, "workspace_uid": n.WorkspaceUID, "workspace_ownership": n.WorkspaceOwnership, "workspace_size_bytes": n.WorkspaceSizeBytes}); err != nil {
					return err
				}
			}
			if err := s.ReserveNotebookBudget(ctx, st, snapshot, n, o.ID); err != nil {
				return err
			}
			return queueNotebook(o, n, false)
		})
}

// 同一实例只接受一个未完成写操作，避免启停/删除互相覆盖。权限 guard 在幂等回放前执行，
// 状态和 revision 检查则在回放之后执行，因此同一请求重发不会被自己的新版本拒绝。
func writableNotebook(ctx context.Context, st *repository.Store, groupID, id, revision uint64, allowDeleting bool) (*model.Notebook, error) {
	n, err := st.Notebook(ctx, groupID, id, true, false)
	if err != nil {
		return nil, err
	}
	if revision == 0 || revision != n.Revision {
		return nil, apperr.New(apperr.CodeConflict, "实例版本已变化，请刷新后重试")
	}
	if n.Revision == ^uint64(0) {
		return nil, apperr.New(apperr.CodeConflict, "实例版本已达到上限")
	}
	if n.DesiredState == "DELETED" && !allowDeleting {
		return nil, apperr.New(apperr.CodeConflict, "实例正在删除，不可修改或启停")
	}
	busy, err := st.HasNotebookOperation(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	if busy {
		return nil, apperr.New(apperr.CodeConflict, "实例有未完成操作，请查询操作单或等待执行完成")
	}
	return n, nil
}

func queueNotebook(o *model.Operation, n *model.Notebook, deleteWorkspace bool) error {
	o.Cluster, o.TargetKey, o.TargetVersion = n.Cluster, strconv.FormatUint(n.ID, 10), n.Revision
	o.Status, o.Step = "PENDING", "WAIT_EXECUTOR"
	command, err := json.Marshal(notebookCommand(n, deleteWorkspace))
	if err != nil {
		return err
	}
	o.Payload = command
	return result(o, map[string]any{"notebookId": n.ID, "groupId": n.GroupID,
		"revision": n.Revision, "serverType": n.ServerType, "desiredState": n.DesiredState, "status": n.Status,
		"observedState": n.ObservedState, "appliedRevision": n.AppliedRevision,
		"deleteWorkspace": deleteWorkspace, "executionPending": true})
}

func saveNotebookIntent(ctx context.Context, st *repository.Store, o *model.Operation, n *model.Notebook, fields map[string]any, deleteWorkspace bool) error {
	n.Revision++
	n.LastOperationID, n.StatusReason = &o.ID, nil
	fields["revision"], fields["last_operation_id"] = n.Revision, o.ID
	fields["status"], fields["desired_state"], fields["status_reason"] = n.Status, n.DesiredState, nil
	if err := st.UpdateNotebook(ctx, n.GroupID, n.ID, fields); err != nil {
		return err
	}
	return queueNotebook(o, n, deleteWorkspace)
}

func (s *Service) UpdateNotebook(ctx context.Context, actor, groupID, id uint64, key string, in dto.UpdateNotebookReq) (*model.Operation, error) {
	if in.Revision == 0 || !in.HasChanges() {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision 和至少一个修改字段")
	}
	return s.mutate(ctx, actor, groupID, key, "notebook.update", "notebook.update", "notebook",
		fmt.Sprintf("%d/%d", groupID, id), in, func(st *repository.Store, o *model.Operation) error {
			if _, err := notebookGroup(ctx, st, groupID, false); err != nil {
				return err
			}
			n, err := writableNotebook(ctx, st, groupID, id, in.Revision, false)
			if err != nil {
				return err
			}
			if in.ChangesRuntime() && (n.DesiredState != "STOPPED" || n.ObservedState != "STOPPED" || n.AppliedRevision != n.Revision || n.Status != "STOPPED") {
				return apperr.New(apperr.CodeConflict, "修改资源、环境变量或标签前，请先等待实例停止生效")
			}
			var spec dto.CreateNotebookReq
			if err := json.Unmarshal(n.RequestedSpec, &spec); err != nil {
				return err
			}
			if validator.NotebookServerTypeOrDefault(spec.ServerType) != n.ServerType {
				return apperr.New(apperr.CodeConflict, "实例类型与已保存规格不一致，不可修改")
			}
			in.Apply(&spec)
			if err := spec.Validate(); err != nil {
				return err
			}
			requested, err := json.Marshal(spec)
			if err != nil {
				return err
			}
			n.DisplayName, n.Description, n.IdleStopMinutes = spec.DisplayName, spec.Description, uint(spec.IdleStopMinutes)
			n.CPUMilli, _ = validator.QuantityUnits(spec.CPU, 1000)
			n.MemoryBytes, _ = validator.QuantityUnits(spec.Memory, 1)
			n.GPUCount, n.RequestedSpec, n.Status = uint(spec.GPU), requested, "UPDATING"
			return saveNotebookIntent(ctx, st, o, n, map[string]any{"display_name": n.DisplayName,
				"description": n.Description, "idle_stop_minutes": n.IdleStopMinutes, "cpu_milli": n.CPUMilli,
				"memory_bytes": n.MemoryBytes, "gpu_count": n.GPUCount, "requested_spec": string(requested)}, false)
		}, notebookGuard(ctx, actor, groupID, id, "notebook.update"))
}

func (s *Service) StartNotebook(ctx context.Context, actor, groupID, id uint64, key string, in dto.NotebookActionReq) (*model.Operation, error) {
	return s.changeNotebookState(ctx, actor, groupID, id, key, in, "start", "RUNNING", "STARTING")
}

func (s *Service) StopNotebook(ctx context.Context, actor, groupID, id uint64, key string, in dto.NotebookActionReq) (*model.Operation, error) {
	return s.changeNotebookState(ctx, actor, groupID, id, key, in, "stop", "STOPPED", "STOPPING")
}

func (s *Service) changeNotebookState(ctx context.Context, actor, groupID, id uint64, key string, in dto.NotebookActionReq, action, desired, status string) (*model.Operation, error) {
	if in.Revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision")
	}
	permission := "notebook." + action
	var snapshot *QuotaSnapshot
	if action == "start" {
		var err error
		snapshot, err = s.prepareNotebookMutationBudget(ctx, actor, groupID, key, permission)
		if err != nil {
			return nil, err
		}
	}
	return s.mutate(ctx, actor, groupID, key, permission, permission, "notebook", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			if action == "stop" {
				if _, err := notebookCleanupGroup(ctx, st, groupID); err != nil {
					return err
				}
			} else {
				if _, err := notebookGroup(ctx, st, groupID, false); err != nil {
					return err
				}
			}
			n, err := writableNotebook(ctx, st, groupID, id, in.Revision, false)
			if err != nil {
				return err
			}
			if n.DesiredState == desired && n.ObservedState == desired && n.Status == desired && n.AppliedRevision == n.Revision && recentNotebookObservation(n) {
				o.Cluster, o.TargetKey, o.TargetVersion = n.Cluster, strconv.FormatUint(n.ID, 10), n.Revision
				o.Payload = json.RawMessage(`{}`)
				return result(o, map[string]any{"notebookId": n.ID, "revision": n.Revision, "status": n.Status, "changed": false})
			}
			n.DesiredState, n.Status = desired, status
			if err := saveNotebookIntent(ctx, st, o, n, map[string]any{}, false); err != nil {
				return err
			}
			if action == "start" {
				return s.ReserveNotebookBudget(ctx, st, snapshot, n, o.ID)
			}
			return nil
		}, notebookGuard(ctx, actor, groupID, id, permission))
}

func (s *Service) DeleteNotebook(ctx context.Context, actor, groupID, id uint64, key string, in dto.DeleteNotebookReq) (*model.Operation, error) {
	if in.Revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供 revision")
	}
	return s.mutate(ctx, actor, groupID, key, "notebook.delete", "notebook.delete", "notebook", fmt.Sprintf("%d/%d", groupID, id), in,
		func(st *repository.Store, o *model.Operation) error {
			if _, err := notebookCleanupGroup(ctx, st, groupID); err != nil {
				return err
			}
			n, err := writableNotebook(ctx, st, groupID, id, in.Revision, false)
			if err != nil {
				return err
			}
			if in.DeleteWorkspace {
				if in.ConfirmName != n.Name {
					return apperr.New(apperr.CodeInvalid, "删除工作目录需填写完全匹配的实例名称")
				}
				if err := notebookWorkspaceDeletable(ctx, st, n); err != nil {
					return err
				}
			}
			n.DesiredState, n.Status = "DELETED", "DELETING"
			// 不在此处设置 deleted_at；执行层确认对象消失后才能释放名称。
			return saveNotebookIntent(ctx, st, o, n, map[string]any{}, in.DeleteWorkspace)
		}, notebookGuard(ctx, actor, groupID, id, "notebook.delete"))
}

func notebookWorkspaceDeletable(ctx context.Context, st *repository.Store, n *model.Notebook) error {
	if err := protectNotebookWorkspace(ctx, st, n); err != nil {
		return err
	}
	if n.WorkspaceOwnership != "EXCLUSIVE" {
		return apperr.New(apperr.CodeConflict, "共享或归属未确认的工作目录不可随实例删除")
	}
	if n.WorkspacePVC == "" || n.WorkspaceUID == nil || *n.WorkspaceUID == "" {
		return apperr.New(apperr.CodeConflict, "工作目录身份尚未确认，不能申请删除数据")
	}
	shared, err := st.NotebookWorkspaceReferenced(ctx, n)
	if err != nil {
		return err
	}
	if shared {
		return apperr.New(apperr.CodeConflict, "其他实例也引用了此工作目录，不能删除数据")
	}
	return nil
}

func (s *Service) NotebookAccess(ctx context.Context, actor, groupID, id uint64) (*dto.NotebookAccess, error) {
	n, err := s.readNotebook(ctx, actor, groupID, id, "notebook.connect")
	if err != nil {
		return nil, err
	}
	if !validator.IsNotebookServerType(n.ServerType) {
		return nil, apperr.New(apperr.CodeConflict, "实例类型无效")
	}
	access := &dto.NotebookAccess{ServerType: n.ServerType, ObservedAt: n.ObservedAt, Reason: "实例尚未就绪"}
	g, err := s.Store.Group(ctx, groupID, false)
	if err != nil {
		return nil, domainError(err)
	}
	if g.Status != "ACTIVE" || g.DeletedAt != nil {
		access.Reason = "空间尚未就绪"
		return access, nil
	}
	if n.DeletedAt != nil || n.DesiredState != "RUNNING" || n.Status != "RUNNING" || n.ObservedState != "RUNNING" || n.AppliedRevision != n.Revision {
		return access, nil
	}
	if !recentNotebookObservation(n) {
		access.Reason = "缺少近期执行层观测"
		return access, nil
	}
	u, err := s.Store.User(ctx, actor, false)
	if err != nil {
		return nil, err
	}
	// 固定基础设施 Owner 原生拥有空间访问，但仍必须通过前面的业务权限判断。
	if u.Username != g.InfrastructureOwner {
		a, err := s.Store.Access(ctx, groupID, actor, false)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err != nil || a.Status != "READY" || a.DesiredLevel == "none" || a.ObservedLevel != a.DesiredLevel || a.AppliedVersion != a.DesiredVersion || a.ObservedAt == nil || time.Since(*a.ObservedAt) > 2*time.Minute {
			access.Reason = "空间原生访问授权尚未就绪"
			return access, nil
		}
	}
	// 仅返回相对路径；不接受客户端提供的 Host 或回跳 URL，也不代发用户凭据。
	if !validator.IsDNS1123Label(n.Namespace) || !validator.IsDNS1123Label(n.Name) {
		return nil, apperr.New(apperr.CodeConflict, "实例访问路径无效")
	}
	access.Ready, access.Reason, access.URL = true, "READY", fmt.Sprintf("/notebook/%s/%s/", n.Namespace, n.Name)
	return access, nil
}
