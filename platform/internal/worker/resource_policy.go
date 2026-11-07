package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/service"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// resourcePolicy 不产生任何外部副作用，也不重发/重试子操作。只有指定停止操作
// 全部成功且当前撤权目标已确认，才在租约 fence 事务中完成父操作。
// FAILED 子操作/撤权失败返回可重试错误；SUPERSEDED 绝不视为停止成功。
func (r *Runner) resourcePolicy(ctx context.Context, c *repository.Claim, guard func() error) error {
	command, err := resourcePolicyCommand(c)
	if err != nil {
		return err
	}
	if c.Cluster != r.Cluster {
		return &k8s.Permanent{Reason: "资源策略父操作与协调执行器 cluster 不匹配"}
	}
	if err := guard(); err != nil {
		return err
	}
	return repository.InTx(ctx, r.DB, func(st *repository.Store) error {
		if err := st.LockAuthorization(ctx); err != nil {
			return err
		}
		if err := st.Fence(ctx, c); err != nil {
			return err
		}
		u, err := st.User(ctx, command.UserID, true)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errSuperseded
		}
		if err != nil {
			return err
		}
		if c.Action == "user.DISABLED" {
			if u.Status != "DISABLED" || u.SessionVersion != command.UserVersion {
				return errSuperseded
			}
		} else {
			if u.AuthorizationVersion != command.UserVersion || u.Status == "DELETING" || u.Status == "DELETED" {
				return errSuperseded
			}
			member, err := st.ActiveMember(ctx, command.GroupID, command.UserID)
			if err != nil {
				return err
			}
			if member {
				return errSuperseded
			}
		}

		// 查询当前目标集合，不能只核对旧 applied_version，或遗漏后来新增的访问行。
		accesses := []model.GroupAccess{}
		q := st.DB.WithContext(ctx).Where("user_id=?", command.UserID)
		if command.GroupID != 0 {
			q = q.Where("group_id=?", command.GroupID)
		}
		if err := q.Order("group_id").Find(&accesses).Error; err != nil {
			return err
		}
		if len(accesses) != len(command.AccessTargets) {
			return errSuperseded
		}
		expected := map[uint64]uint64{}
		for _, a := range command.AccessTargets {
			expected[a.GroupID] = a.DesiredVersion
		}
		accessPending := false
		warnings := []map[string]any{}
		var accessFailure error
		for _, a := range accesses {
			if expected[a.GroupID] != a.DesiredVersion || a.DesiredLevel != "none" {
				return errSuperseded
			}
			if a.Status == "DRIFTED" && a.AppliedVersion == a.DesiredVersion && a.ObservedAt != nil {
				accepted, err := resourcePolicyOwnerWarning(ctx, st, u, &a)
				if err != nil {
					return err
				}
				if accepted {
					warnings = append(warnings, map[string]any{"code": "INFRASTRUCTURE_OWNER_ACCESS_RETAINED", "groupId": a.GroupID, "userId": u.ID, "message": infrastructureOwnerRetainedWarning})
					continue
				}
			}
			if a.Status == "FAILED" || a.Status == "DRIFTED" || a.ObservedLevel == "conflict" {
				accessFailure = fmt.Errorf("空间 %d 用户 %d 撤权失败或存在额外授权；请修复并重试访问操作，父操作未完成", a.GroupID, command.UserID)
				continue
			}
			if a.Status != "READY" || a.ObservedLevel != "none" || a.AppliedVersion != a.DesiredVersion || a.ObservedAt == nil {
				accessPending = true
			}
		}

		stopPending := false
		var stopFailure error
		for _, id := range command.StopOperationIDs {
			child, err := st.Operation(ctx, id)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &k8s.Permanent{Reason: "资源策略引用的停止子操作不存在：" + id}
			}
			if err != nil {
				return err
			}
			if child.TargetType != "notebook" || child.Action != "notebook.stop" || child.TargetVersion == 0 {
				return &k8s.Permanent{Reason: "资源策略子操作不是有效的 notebook.stop：" + id}
			}
			var notebook service.NotebookCommand
			if json.Unmarshal(child.Payload, &notebook) != nil || notebook.NotebookID == 0 ||
				strconv.FormatUint(notebook.NotebookID, 10) != child.TargetKey || notebook.Revision != child.TargetVersion ||
				notebook.DesiredState != "STOPPED" || notebook.DeleteWorkspace || notebook.Cluster != child.Cluster ||
				(command.GroupID != 0 && notebook.GroupID != command.GroupID) {
				return &k8s.Permanent{Reason: "资源策略停止子操作身份或保留数据契约无效：" + id}
			}
			switch child.Status {
			case "SUCCEEDED":
				// 成功回执对应这份不可变版本化停止命令；不从当前 Notebook 状态反推旧命令成功。
			case "SUPERSEDED":
				return errSuperseded
			case "FAILED":
				stopFailure = fmt.Errorf("停止子操作 %s 已失败；请先重试子操作，再重试父操作，数据继续保留", id)
			case "PENDING", "RUNNING", "RETRYING":
				stopPending = true
			default:
				return &k8s.Permanent{Reason: "停止子操作状态无效：" + id}
			}
		}
		if stopFailure != nil {
			return stopFailure
		}
		if accessFailure != nil {
			return accessFailure
		}
		if stopPending {
			return &k8s.Pending{Step: "WAIT_POLICY_NOTEBOOK_STOPS", Reason: "等待持久停止子操作完成；不自动启动、取消或释放数据"}
		}
		if accessPending {
			return &k8s.Pending{Step: "WAIT_POLICY_ACCESS_REVOKED", Reason: "等待当前撤权目标 none 的 applied_version 与实际观测确认"}
		}

		value := map[string]any{}
		if len(c.Result) != 0 {
			if err := json.Unmarshal(c.Result, &value); err != nil {
				return err
			}
		}
		if value == nil {
			value = map[string]any{}
		}
		value["userId"], value["resourcePolicy"], value["dataPolicy"] = command.UserID, "SUCCEEDED", "RETAIN"
		value["accessCleanupPending"], value["accessPending"] = false, false
		value["notebookStopOperations"], value["userVersion"] = command.StopOperationIDs, command.UserVersion
		value["warnings"] = warnings
		value["fullyRevoked"] = len(warnings) == 0
		value["infrastructureOwnerAccessRetained"] = len(warnings) != 0
		if command.GroupID != 0 {
			value["groupId"] = command.GroupID
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id=?", c.ID).Update("result", string(data)).Error; err != nil {
			return err
		}
		if len(warnings) != 0 {
			return st.Finish(ctx, c, "SUCCEEDED", "RESOURCE_POLICY_CONFIRMED_WITH_WARNING", infrastructureOwnerRetainedWarning, 0, false)
		}
		return st.Finish(ctx, c, "SUCCEEDED", "RESOURCE_POLICY_CONFIRMED", "", 0, false)
	})
}

const infrastructureOwnerRetainedWarning = "该身份是固定基础设施 Owner；业务撤权不会移除其系统管理员访问"

// 不能仅以 username==Owner 放行 DRIFTED。旧适配器会用 Owner 文本覆盖任意额外
// RB/AP/CRB warning，因此必须有新版观测器提供的结构化“仅系统权限”证明。
// 回执来自相同目标版本的最新持久 access.reconcile 操作，不接受旧版本或运行中回执。
func resourcePolicyOwnerWarning(ctx context.Context, st *repository.Store, u *model.User, a *model.GroupAccess) (bool, error) {
	if a.Status != "DRIFTED" || a.DesiredLevel != "none" || a.ObservedLevel != "conflict" || a.AppliedVersion != a.DesiredVersion || a.ObservedAt == nil || a.LastError == nil || *a.LastError != infrastructureOwnerRetainedWarning {
		return false, nil
	}
	g, err := st.Group(ctx, a.GroupID, false)
	if err != nil {
		return false, err
	}
	if g.InfrastructureOwner == "" || u.Username != g.InfrastructureOwner {
		return false, nil
	}
	var operation model.Operation
	err = st.DB.WithContext(ctx).Where("action=? AND target_type=? AND target_key=? AND target_version=?", "access.reconcile", "access", fmt.Sprintf("%d/%d", a.GroupID, u.ID), a.DesiredVersion).
		Order("id DESC").First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if operation.Status != "FAILED" || operation.Step != "EXTERNAL_ACCESS_REMAINS" || operation.Cluster != g.Cluster || operation.FinishedAt == nil || operation.LastError == nil || *operation.LastError != infrastructureOwnerRetainedWarning {
		return false, nil
	}
	var receipt struct {
		GroupID                 uint64 `json:"groupId"`
		UserID                  uint64 `json:"userId"`
		DesiredLevel            string `json:"desiredLevel"`
		ObservedLevel           string `json:"observedLevel"`
		AccessStatus            string `json:"accessStatus"`
		Warning                 string `json:"warning"`
		WarningCode             string `json:"warningCode"`
		InfrastructureOwnerOnly bool   `json:"infrastructureOwnerOnly"`
	}
	if json.Unmarshal(operation.Result, &receipt) != nil {
		return false, nil
	}
	return receipt.GroupID == a.GroupID && receipt.UserID == u.ID && receipt.DesiredLevel == "none" && receipt.ObservedLevel == "conflict" && receipt.AccessStatus == "DRIFTED" &&
		receipt.Warning == infrastructureOwnerRetainedWarning && receipt.WarningCode == "INFRASTRUCTURE_OWNER_ACCESS_RETAINED" && receipt.InfrastructureOwnerOnly, nil
}

func resourcePolicyCommand(c *repository.Claim) (*service.ResourcePolicyCommand, error) {
	invalid := func() (*service.ResourcePolicyCommand, error) {
		return nil, &k8s.Permanent{Reason: "资源策略父操作契约无效或缺少版本 fence；请使用新幂等键重新受理"}
	}
	var command service.ResourcePolicyCommand
	if c.TargetType != "user" || json.Unmarshal(c.Payload, &command) != nil || command.SchemaVersion != 1 || command.UserID == 0 || command.UserVersion == 0 ||
		command.UserVersion != c.TargetVersion || command.DataPolicy != "RETAIN" {
		return invalid()
	}
	switch c.Action {
	case "user.DISABLED":
		if command.GroupID != 0 || c.TargetKey != strconv.FormatUint(command.UserID, 10) {
			return invalid()
		}
	case "member.roles.replace":
		if command.GroupID == 0 || c.TargetKey != fmt.Sprintf("%d/%d", command.GroupID, command.UserID) || len(command.GroupIDs) != 0 || len(command.AccessTargets) != 1 {
			return invalid()
		}
	default:
		return invalid()
	}
	groups := map[uint64]bool{}
	for _, a := range command.AccessTargets {
		if a.GroupID == 0 || a.DesiredVersion == 0 || groups[a.GroupID] || (command.GroupID != 0 && command.GroupID != a.GroupID) {
			return invalid()
		}
		groups[a.GroupID] = true
	}
	if command.GroupID == 0 {
		if len(command.GroupIDs) != len(groups) {
			return invalid()
		}
		seen := map[uint64]bool{}
		for _, id := range command.GroupIDs {
			if !groups[id] || seen[id] {
				return invalid()
			}
			seen[id] = true
		}
	}
	seen := map[string]bool{}
	for _, id := range command.StopOperationIDs {
		if _, err := uuid.Parse(id); err != nil || id == c.ID || seen[id] {
			return invalid()
		}
		seen[id] = true
	}
	return &command, nil
}
