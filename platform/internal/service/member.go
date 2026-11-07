package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/google/uuid"
)

type RoleGrant struct {
	RoleID    uint64     `json:"roleId"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
type ReplaceRoles struct {
	Roles []RoleGrant `json:"roles"`
}

func (s *Service) ReplaceRoles(ctx context.Context, actor, userID, groupID uint64, key string, in ReplaceRoles) (*model.Operation, error) {
	permission, scope := "role.manage", "global"
	if groupID > 0 {
		permission, scope = "group.member.manage", "group"
	}
	if len(in.Roles) > 32 {
		return nil, apperr.New(apperr.CodeInvalid, "单个用户最多绑定 32 个角色")
	}
	return s.mutate(ctx, actor, groupID, key, permission, "member.roles.replace", "user", fmt.Sprintf("%d/%d", groupID, userID), in, func(st *repository.Store, o *model.Operation) error {
		u, err := st.User(ctx, userID, true)
		if err != nil {
			return err
		}
		if len(in.Roles) > 0 && u.Status != "ACTIVE" {
			return apperr.New(apperr.CodeConflict, "只能授权给启用的用户")
		}
		if groupID > 0 && len(in.Roles) == 0 {
			if err := requireQuietML(ctx, st, groupID, userID); err != nil {
				return err
			}
		}
		if groupID > 0 {
			g, err := st.Group(ctx, groupID, true)
			if err != nil {
				return err
			}
			if g.Status != "ACTIVE" && g.Status != "CREATING" && g.Status != "DEGRADED" && !(g.Status == "DISABLED" && len(in.Roles) == 0) {
				return apperr.New(apperr.CodeConflict, "空间当前状态不允许修改成员")
			}
		}
		seen := map[uint64]bool{}
		for _, grant := range in.Roles {
			if seen[grant.RoleID] {
				return apperr.New(apperr.CodeInvalid, "角色不能重复")
			}
			seen[grant.RoleID] = true
			r, err := st.Role(ctx, grant.RoleID, false)
			if err != nil {
				return err
			}
			if r.Scope != scope || !r.Enabled {
				return apperr.New(apperr.CodeInvalid, "角色范围不匹配或已停用")
			}
			if grant.ExpiresAt != nil && (!grant.ExpiresAt.After(time.Now()) || r.Code == "group_owner" || r.Code == "platform_admin") {
				return apperr.New(apperr.CodeInvalid, "有效期必须在未来，Owner 和平台管理员不允许设置到期时间")
			}
		}
		if err := st.RevokeBindings(ctx, actor, userID, groupID, scope, nil); err != nil {
			return err
		}
		for _, grant := range in.Roles {
			if err := st.GrantBinding(ctx, &model.Binding{UserID: userID, RoleID: grant.RoleID, Scope: scope, ScopeID: groupID, ExpiresAt: grant.ExpiresAt, GrantedBy: actor}, false); err != nil {
				return err
			}
		}
		if err := st.BumpUserVersion(ctx, userID); err != nil {
			return err
		}
		if groupID == 0 {
			if err := ensureAdmin(ctx, st); err != nil {
				return err
			}
		} else {
			if err := ensureOwner(ctx, st, groupID); err != nil {
				return err
			}
			if err := refreshAccess(ctx, st, groupID, userID); err != nil {
				return err
			}
			if len(in.Roles) == 0 {
				ids, err := stopLifecycleNotebooks(ctx, st, actor, groupID, userID)
				if err != nil {
					return err
				}
				g, err := st.Group(ctx, groupID, false)
				if err != nil {
					return err
				}
				if err := s.queueResourcePolicy(ctx, st, o, userID, groupID, ids, g.Cluster); err != nil {
					return err
				}
				return result(o, map[string]any{"userId": userID, "groupId": groupID, "businessRolesUpdated": true, "resourcePolicy": "PENDING", "dataPolicy": "RETAIN", "notebookStopOperations": ids})
			}
		}
		return result(o, map[string]any{"userId": userID, "groupId": groupID, "businessRolesUpdated": true, "accessPending": groupID > 0})
	})
}

// refreshAccess 将多个角色聚合为一份目标，持久化后续执行任务。
// 任务不携带旧角色作为真相，执行时必须重读 desired_version 和有效绑定。
func refreshAccess(ctx context.Context, st *repository.Store, groupID, userID uint64) error {
	level, err := st.AccessLevel(ctx, groupID, userID)
	if err != nil {
		return err
	}
	group, err := st.Group(ctx, groupID, false)
	if err != nil {
		return err
	}
	if group.Status == "DISABLING" || group.Status == "DISABLED" || group.Status == "DELETING" || group.Status == "DELETED" {
		level = "none"
	}
	access, err := st.SetDesiredAccess(ctx, groupID, userID, level)
	if err != nil {
		return err
	}
	version, status := access.DesiredVersion, access.Status
	if status == "READY" || status == "FAILED" || status == "DRIFTED" {
		return nil
	}
	pending, err := st.HasAccessOperation(ctx, fmt.Sprintf("%d/%d", groupID, userID), &version)
	if err != nil {
		return err
	}
	if pending {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"groupId": groupID, "userId": userID})
	o := &model.Operation{ID: uuid.NewString(), Action: "access.reconcile", TargetType: "access", TargetKey: fmt.Sprintf("%d/%d", groupID, userID), TargetVersion: version, Status: "PENDING", Step: "ACCEPTED", Payload: payload, Result: json.RawMessage(`{}`)}
	// 每次重新计算均可唤醒，旧任务由版本检查淘汰；系统任务键不与用户请求共用。
	return st.InsertOperation(ctx, o, "access-"+uuid.NewString(), fmt.Sprintf("%064d", version))
}

func (s *Service) Members(ctx context.Context, actor, groupID uint64) ([]model.Member, error) {
	if err := s.Require(ctx, actor, groupID, "group.read"); err != nil {
		return nil, err
	}
	ids, err := s.Store.MemberIDs(ctx, groupID)
	if err != nil {
		return nil, err
	}
	result := []model.Member{}
	for _, id := range ids {
		u, err := s.Store.User(ctx, id, false)
		if err != nil {
			return nil, err
		}
		bindings, err := s.Store.Bindings(ctx, id, groupID)
		if err != nil {
			return nil, err
		}
		m := model.Member{UserID: id, Username: u.Username, DisplayName: u.DisplayName, Roles: bindings, DesiredLevel: "none", ObservedLevel: "unknown", AccessStatus: "PENDING"}
		a, err := s.Store.Access(ctx, groupID, id, false)
		if err != nil {
			return nil, err
		}
		m.DesiredLevel, m.ObservedLevel, m.AccessStatus, m.LastError = a.DesiredLevel, a.ObservedLevel, a.Status, a.LastError
		result = append(result, m)
	}
	return result, nil
}
