package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

// GroupLifecycleCommand 是 group.disable/restore/delete 与 group.quota.update 的版本化执行契约。
// 删除必须重新完整检查原生资源；业务 DB 为空不代表 namespace 为空。
type GroupLifecycleCommand struct {
	GroupID      uint64     `json:"groupId"`
	InstanceID   string     `json:"instanceId"`
	Cluster      string     `json:"cluster"`
	Namespace    string     `json:"namespace"`
	ProfileUID   *string    `json:"profileUID"`
	NamespaceUID *string    `json:"namespaceUID"`
	Revision     uint64     `json:"revision"`
	DesiredState string     `json:"desiredState,omitempty"`
	RequireEmpty bool       `json:"requireEmpty,omitempty"`
	Quota        *dto.Quota `json:"quota,omitempty"`
}

// user.DISABLED / 移除成员的 member.roles.replace：父操作只聚合，不执行隐式资源转移。
// 用户恢复、成员重新加入或版本变化后父操作过期；旧停止子任务仍独立收敛，绝不发起 start。
type ResourcePolicyCommand struct {
	SchemaVersion    int                          `json:"schemaVersion"`
	UserID           uint64                       `json:"userId"`
	UserVersion      uint64                       `json:"userVersion"`
	GroupID          uint64                       `json:"groupId,omitempty"`
	GroupIDs         []uint64                     `json:"groupIds,omitempty"`
	StopOperationIDs []string                     `json:"stopOperationIds"`
	AccessTargets    []ResourcePolicyAccessTarget `json:"accessTargets"`
	DataPolicy       string                       `json:"dataPolicy"`
}

type ResourcePolicyAccessTarget struct {
	GroupID        uint64 `json:"groupId"`
	DesiredVersion uint64 `json:"desiredVersion"`
}

// queueResourcePolicy 与身份状态/撤权/停止意图同事务提交。user.DISABLED 用
// session_version，member.roles.replace 用 authorization_version，避免恢复再禁用、
// 重加再移除后旧父操作错误地完成新策略。跨集群子操作仍由各自 cluster 消费。
func (s *Service) queueResourcePolicy(ctx context.Context, st *repository.Store, o *model.Operation, userID, groupID uint64, stopIDs []string, cluster string) error {
	u, err := st.User(ctx, userID, false)
	if err != nil {
		return err
	}
	command := ResourcePolicyCommand{SchemaVersion: 1, UserID: userID, GroupID: groupID, StopOperationIDs: stopIDs, DataPolicy: "RETAIN", AccessTargets: []ResourcePolicyAccessTarget{}}
	command.UserVersion = u.SessionVersion
	if groupID != 0 {
		command.UserVersion = u.AuthorizationVersion
	}
	accesses := []model.GroupAccess{}
	q := st.DB.WithContext(ctx).Where("user_id=?", userID)
	if groupID != 0 {
		q = q.Where("group_id=?", groupID)
	}
	if err := q.Order("group_id").Find(&accesses).Error; err != nil {
		return err
	}
	for _, a := range accesses {
		// 账号禁用还须覆盖历史撤权未完成、但已无有效绑定的访问记录。
		if a.DesiredLevel != "none" {
			if err := refreshAccess(ctx, st, a.GroupID, userID); err != nil {
				return err
			}
			current, err := st.Access(ctx, a.GroupID, userID, false)
			if err != nil {
				return err
			}
			a = *current
		}
		if a.DesiredLevel != "none" {
			return apperr.New(apperr.CodeConflict, "资源策略要求撤权目标为 none")
		}
		command.AccessTargets = append(command.AccessTargets, ResourcePolicyAccessTarget{GroupID: a.GroupID, DesiredVersion: a.DesiredVersion})
		if groupID == 0 {
			command.GroupIDs = append(command.GroupIDs, a.GroupID)
		}
	}
	if groupID != 0 && len(command.AccessTargets) != 1 {
		return apperr.New(apperr.CodeConflict, "成员撤权目标缺失")
	}
	if cluster == "" {
		cluster = s.Cluster
	}
	if cluster == "" {
		cluster = "default"
	}
	o.Status, o.Step, o.Cluster, o.TargetVersion = "PENDING", "WAIT_RESOURCE_POLICY", cluster, command.UserVersion
	o.Payload, err = json.Marshal(command)
	return err
}

func groupLifecycleCommand(g *model.Group) GroupLifecycleCommand {
	return GroupLifecycleCommand{GroupID: g.ID, InstanceID: g.InstanceID, Cluster: g.Cluster, Namespace: g.Name,
		ProfileUID: g.ProfileUID, NamespaceUID: g.NamespaceUID, Revision: g.Revision}
}

func writableLifecycleGroup(ctx context.Context, st *repository.Store, id, revision uint64) (*model.Group, error) {
	g, err := st.Group(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if revision == 0 || g.Revision != revision || revision == ^uint64(0) || g.DeletedAt != nil {
		return nil, apperr.New(apperr.CodeConflict, "空间版本已变化或已删除")
	}
	busy, err := st.HasGroupOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if busy {
		return nil, apperr.New(apperr.CodeConflict, "空间存在未完成操作")
	}
	return g, nil
}

func (s *Service) DisableGroup(ctx context.Context, actor, id uint64, key string, in dto.LifecycleReq) (*model.Operation, error) {
	return s.changeGroupLifecycle(ctx, actor, id, key, in, "disable")
}
func (s *Service) RestoreGroup(ctx context.Context, actor, id uint64, key string, in dto.LifecycleReq) (*model.Operation, error) {
	return s.changeGroupLifecycle(ctx, actor, id, key, in, "restore")
}
func (s *Service) DeleteGroup(ctx context.Context, actor, id uint64, key string, in dto.LifecycleReq) (*model.Operation, error) {
	return s.changeGroupLifecycle(ctx, actor, id, key, in, "delete")
}
func (s *Service) changeGroupLifecycle(ctx context.Context, actor, id uint64, key string, in dto.LifecycleReq, action string) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.mutate(ctx, actor, id, key, "group."+action, "group."+action, "group", fmt.Sprint(id), in, func(st *repository.Store, o *model.Operation) error {
		g, err := writableLifecycleGroup(ctx, st, id, in.Revision)
		if err != nil {
			return err
		}
		if g.ProfileUID == nil || *g.ProfileUID == "" || g.NamespaceUID == nil || *g.NamespaceUID == "" {
			return apperr.New(apperr.CodeConflict, "缺少已确认的 Profile/namespace 身份，需先完成观测")
		}
		status, desired := "DISABLING", "DISABLED"
		switch action {
		case "disable":
			if g.Status != "ACTIVE" && g.Status != "DEGRADED" {
				return apperr.New(apperr.CodeConflict, "仅可停用已就绪或降级空间")
			}
			if err := requireQuietML(ctx, st, id, 0); err != nil {
				return err
			}
		case "restore":
			if g.Status != "DISABLED" {
				return apperr.New(apperr.CodeConflict, "仅可恢复已停用空间")
			}
			if err := ensureOwner(ctx, st, id); err != nil {
				return err
			}
			status, desired = "RESTORING", "ACTIVE"
		case "delete":
			if g.Status != "DISABLED" && g.Status != "ACTIVE" && g.Status != "DEGRADED" {
				return apperr.New(apperr.CodeConflict, "空间当前不可删除")
			}
			if in.ConfirmName != g.Name {
				return apperr.New(apperr.CodeInvalid, "删除必须填写完全匹配的空间名")
			}
			deps, err := st.GroupDependencies(ctx, id)
			if err != nil {
				return err
			}
			if !deps.Empty() {
				return apperr.New(apperr.CodeConflict, fmt.Sprintf("空间非空：Notebook=%d，目录=%d，历史数据引用=%d，额度占用=%d，Pipeline=%d，运行=%d，训练=%d，推理=%d；需先显式处置", deps.Notebooks, deps.Workspaces, deps.LegacyWorkspaces, deps.Reservations, deps.Pipelines, deps.PipelineRuns, deps.TrainerJobs, deps.KServeServices))
			}
			status, desired = "DELETING", "DELETED"
		}
		g.Status, g.Revision = status, g.Revision+1
		if err := st.DB.WithContext(ctx).Model(&model.Group{}).Where("id=?", id).Updates(map[string]any{"status": status, "revision": g.Revision, "last_error": nil}).Error; err != nil {
			return err
		}
		stopIDs := []string{}
		if action == "disable" {
			stopIDs, err = stopLifecycleNotebooks(ctx, st, actor, id, 0)
			if err != nil {
				return err
			}
		}
		// 包括历史已撤销用户，确保迟到的授权写入也被清理。
		var ids []uint64
		if err := st.DB.WithContext(ctx).Raw(`SELECT user_id FROM tb_role_binding WHERE scope_type='group' AND scope_id=? UNION SELECT user_id FROM tb_group_access WHERE group_id=? ORDER BY user_id`, id, id).Scan(&ids).Error; err != nil {
			return err
		}
		for _, userID := range ids {
			if err := st.BumpUserVersion(ctx, userID); err != nil {
				return err
			}
			if err := refreshAccess(ctx, st, id, userID); err != nil {
				return err
			}
		}
		command := groupLifecycleCommand(g)
		command.DesiredState, command.RequireEmpty = desired, action == "delete"
		o.Payload, err = json.Marshal(command)
		if err != nil {
			return err
		}
		o.Status, o.Step, o.Cluster, o.TargetVersion = "PENDING", "WAIT_EXECUTOR", g.Cluster, g.Revision
		return result(o, map[string]any{"groupId": id, "status": status, "revision": g.Revision, "notebookStopOperations": stopIDs, "dataPolicy": "RETAIN", "reason": in.Reason})
	})
}

// 停止可以覆盖旧 create/start/update 意图，旧任务必须按 revision fence 淘汰。
// 已明确申请删除的实例保留原删除流程，绝不将其改回 STOPPED。
func stopLifecycleNotebooks(ctx context.Context, st *repository.Store, actor, groupID, userID uint64) ([]string, error) {
	notebooks, err := st.LifecycleNotebooks(ctx, groupID, userID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for i := range notebooks {
		n := &notebooks[i]
		if n.DesiredState == "DELETED" {
			continue
		}
		if n.DesiredState == "STOPPED" && n.Status == "STOPPED" && n.ObservedState == "STOPPED" && n.AppliedRevision == n.Revision && recentNotebookObservation(n) {
			continue
		}
		if n.Revision == ^uint64(0) {
			return nil, apperr.New(apperr.CodeConflict, "实例版本达到上限")
		}
		// 同一事务锁空间，避免外部业务请求与资源策略交错。
		if _, err := st.Group(ctx, n.GroupID, true); err != nil {
			return nil, err
		}
		n.DesiredState, n.Status = "STOPPED", "STOPPING"
		o := &model.Operation{ID: uuid.NewString(), ActorID: actor, Action: "notebook.stop", TargetType: "notebook"}
		if err := saveNotebookIntent(ctx, st, o, n, map[string]any{}, false); err != nil {
			return nil, err
		}
		if err := st.InsertOperation(ctx, o, "policy-"+uuid.NewString(), fmt.Sprintf("%064d", n.Revision)); err != nil {
			return nil, err
		}
		if err := audit(ctx, st, actor, n.GroupID, "notebook.policy.stop", "notebook", fmt.Sprint(n.ID), o.ID, "ACCEPTED"); err != nil {
			return nil, err
		}
		ids = append(ids, o.ID)
	}
	return ids, nil
}

// TransferNotebookOwner 只改变当前业务负责人；不改历史创建人、不移动空间/PVC。
func (s *Service) TransferNotebookOwner(ctx context.Context, actor, groupID, id uint64, key string, in dto.TransferResourceReq) (*model.Operation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	guard := func(st *repository.Store) error {
		n, err := st.Notebook(ctx, groupID, id, false, true)
		if err != nil {
			return err
		}
		owner, err := st.CurrentNotebookOwner(ctx, n)
		if err != nil {
			return err
		}
		if owner != nil && *owner == actor {
			return nil
		}
		return (rbac.Checker{Store: st}).Require(ctx, actor, groupID, "notebook.transfer.any")
	}
	return s.mutate(ctx, actor, groupID, key, "notebook.transfer", "notebook.owner.transfer", "notebook", fmt.Sprint(id), in, func(st *repository.Store, o *model.Operation) error {
		if _, err := notebookGroup(ctx, st, groupID, false); err != nil {
			return err
		}
		if err := requireActiveMember(ctx, st, groupID, in.UserID); err != nil {
			return err
		}
		n, err := st.Notebook(ctx, groupID, id, true, false)
		if err != nil {
			return err
		}
		if _, err := writableNotebook(ctx, st, groupID, id, n.Revision, false); err != nil {
			return err
		}
		if !confirmedStopped(n) {
			return apperr.New(apperr.CodeConflict, "实例需已确认停止后再转移")
		}
		old, err := st.CurrentNotebookOwner(ctx, n)
		if err != nil {
			return err
		}
		if in.Revision != n.OwnerRevision || n.OwnerRevision == ^uint64(0) {
			return apperr.New(apperr.CodeConflict, "负责人版本已变化，请使用 ownerRevision 重试")
		}
		if old != nil && *old == in.UserID {
			o.TargetKey = fmt.Sprint(id)
			return result(o, map[string]any{"notebookId": id, "ownerUserId": in.UserID, "changed": false})
		}
		owner := &model.NotebookOwner{NotebookID: id, OwnerUserID: in.UserID, Revision: n.OwnerRevision + 1}
		if err := st.DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "notebook_id"}}, DoUpdates: clause.AssignmentColumns([]string{"owner_user_id", "revision"})}).Create(owner).Error; err != nil {
			return err
		}
		o.TargetKey, o.TargetVersion, o.Cluster = fmt.Sprint(id), n.OwnerRevision+1, n.Cluster
		return result(o, map[string]any{"notebookId": id, "previousOwnerUserId": old, "ownerUserId": in.UserID, "ownerRevision": n.OwnerRevision + 1, "dataOwnerChanged": false, "reason": in.Reason})
	}, guard)
}
func requireActiveMember(ctx context.Context, st *repository.Store, groupID, userID uint64) error {
	u, err := st.User(ctx, userID, true)
	if err != nil {
		return err
	}
	member, err := st.ActiveMember(ctx, groupID, userID)
	if err != nil {
		return err
	}
	if u.Status != "ACTIVE" || !member {
		return apperr.New(apperr.CodeConflict, "目标必须是该空间的 ACTIVE 有效成员")
	}
	return nil
}
func confirmedStopped(n *model.Notebook) bool {
	return n.DeletedAt == nil && n.DesiredState == "STOPPED" && n.Status == "STOPPED" && n.ObservedState == "STOPPED" && n.AppliedRevision == n.Revision && recentNotebookObservation(n)
}

func notebookCurrentOwnerAllowed(ctx context.Context, st *repository.Store, actor uint64, n *model.Notebook, permission string) error {
	checker := rbac.Checker{Store: st}
	if err := checker.Require(ctx, actor, n.GroupID, permission); err != nil {
		return err
	}
	owner, err := st.CurrentNotebookOwner(ctx, n)
	if err != nil {
		return err
	}
	if owner != nil && *owner == actor {
		return nil
	}
	return checker.Require(ctx, actor, n.GroupID, "notebook.read.any")
}

// 暂不由身份模块擅自取消训练/推理；已有 live ML 时原事务整体拒绝，
// 用户需通过各模块 stop API 等待确认后再禁用/移除，不会谎报资源已经全部停止。
func requireQuietML(ctx context.Context, st *repository.Store, groupID, userID uint64) error {
	count, err := st.LiveMLDependencies(ctx, groupID, userID)
	if err != nil {
		return err
	}
	if count > 0 {
		return apperr.New(apperr.CodeConflict, fmt.Sprintf("仍有 %d 个运行中、待执行或停止未确认的 ML 资源，请先通过 Pipeline/Trainer/KServe 停止并确认后重试", count))
	}
	return nil
}
