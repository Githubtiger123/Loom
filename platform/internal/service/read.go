package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"gorm.io/gorm"
)

func (s *Service) Users(ctx context.Context, actor, after uint64, limit int) ([]model.User, error) {
	if err := s.Require(ctx, actor, 0, "user.read"); err != nil {
		return nil, err
	}
	return s.Store.Users(ctx, after, limit)
}
func (s *Service) Roles(ctx context.Context, actor uint64) ([]model.Role, error) {
	if err := s.Require(ctx, actor, 0, "role.manage"); err != nil {
		return nil, err
	}
	return s.Store.Roles(ctx)
}
func (s *Service) AvailableRoles(ctx context.Context, actor, groupID uint64) ([]model.Role, error) {
	if err := s.Require(ctx, actor, groupID, "group.member.manage"); err != nil {
		return nil, err
	}
	roles, err := s.Store.Roles(ctx)
	if err != nil {
		return nil, err
	}
	result := []model.Role{}
	for _, role := range roles {
		if role.Enabled && role.Scope == "group" {
			result = append(result, role)
		}
	}
	return result, nil
}
func (s *Service) Permissions(ctx context.Context, actor uint64) ([]model.Permission, error) {
	if err := s.Require(ctx, actor, 0, "role.manage"); err != nil {
		return nil, err
	}
	return s.Store.Permissions(ctx)
}
func (s *Service) Groups(ctx context.Context, actor, after uint64, limit int) ([]model.Group, error) {
	admin, err := s.Store.IsAdmin(ctx, actor)
	if err != nil {
		return nil, err
	}
	return s.Store.Groups(ctx, actor, admin, after, limit)
}
func (s *Service) Group(ctx context.Context, actor, groupID uint64) (*model.Group, error) {
	if err := s.Require(ctx, actor, groupID, "group.read"); err != nil {
		return nil, err
	}
	g, err := s.Store.Group(ctx, groupID, false)
	return g, domainError(err)
}

func (s *Service) Me(ctx context.Context, user *model.User) (any, error) {
	if err := s.Store.RecordLogin(ctx, user.ID); err != nil {
		return nil, err
	}
	bindings, err := s.Store.Bindings(ctx, user.ID, 0)
	if err != nil {
		return nil, err
	}
	permissions := []string{}
	admin, err := s.Store.IsAdmin(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range permissionCodes() {
		if admin {
			permissions = append(permissions, p)
			continue
		}
		ok, err := s.Store.HasPermission(ctx, user.ID, 0, p)
		if err != nil {
			return nil, err
		}
		if ok {
			permissions = append(permissions, p)
		}
	}
	return map[string]any{"user": user, "globalRoles": bindings, "permissions": permissions}, nil
}

func permissionCodes() []string {
	return []string{"user.read", "user.manage", "role.manage", "group.create", "group.quota.update", "audit.read"}
}

func (s *Service) Operation(ctx context.Context, actor uint64, id string) (*model.Operation, error) {
	o, err := s.Store.Operation(ctx, id)
	if err != nil {
		return nil, domainError(err)
	}
	// ML 操作先校验当前模块和对象范围，不走历史发起人/管理员快捷返回。
	if IsMLTarget(o.TargetType) {
		if err := s.RequireMLOperation(ctx, actor, o); err != nil {
			return nil, err
		}
		return o, nil
	}
	// Notebook 不因"曾经发起过操作"而绕过当前空间与对象权限。
	if o.TargetType == "notebook" {
		var n model.Notebook
		if err := s.DB.WithContext(ctx).Where("CAST(id AS CHAR) = ?", o.TargetKey).First(&n).Error; err != nil {
			return nil, domainError(err)
		}
		if err := notebookAllowed(ctx, s.Store, actor, &n, "notebook.read"); err != nil {
			return nil, err
		}
		return o, nil
	}
	if o.TargetType == "workspace" {
		var w model.Workspace
		if err := s.DB.WithContext(ctx).Where("CAST(id AS CHAR)=?", o.TargetKey).First(&w).Error; err != nil {
			return nil, domainError(err)
		}
		if err := workspaceAllowed(ctx, s.Store, actor, &w, "workspace.read"); err != nil {
			return nil, err
		}
		return o, nil
	}
	if o.ActorID == actor {
		return o, nil
	}
	admin, err := s.Store.IsAdmin(ctx, actor)
	if err != nil {
		return nil, err
	}
	if admin {
		return o, nil
	}
	if o.TargetType == "group" {
		groupID, err := strconv.ParseUint(o.TargetKey, 10, 64)
		if err == nil {
			if err := s.Require(ctx, actor, groupID, "group.member.manage"); err == nil {
				return o, nil
			}
		}
	}
	if o.TargetType == "access" {
		parts := strings.Split(o.TargetKey, "/")
		if len(parts) == 2 {
			groupID, err := strconv.ParseUint(parts[0], 10, 64)
			if err == nil && groupID > 0 {
				if err := s.Require(ctx, actor, groupID, "group.member.manage"); err == nil {
					return o, nil
				}
			}
		}
	}
	return nil, apperr.New(apperr.CodeForbidden, "不能查看此操作记录")
}

type AccessState struct {
	UserID         uint64          `json:"userId"`
	Username       string          `json:"username"`
	UserStatus     string          `json:"userStatus"`
	DesiredLevel   string          `json:"desiredLevel"`
	ObservedLevel  string          `json:"observedLevel"`
	DesiredVersion uint64          `json:"desiredVersion"`
	AppliedVersion uint64          `json:"appliedVersion"`
	Status         string          `json:"status"`
	Objects        json.RawMessage `json:"objects" gorm:"column:objects"`
	ObservedAt     *time.Time      `json:"observedAt"`
	LastError      *string         `json:"lastError"`
	OperationID    *string         `json:"operationId"`
}

// Accesses 与成员列表分开：已移除/禁用成员的撤权进度也必须可见。
func (s *Service) Accesses(ctx context.Context, actor, groupID, after uint64, limit int) ([]AccessState, error) {
	if err := s.Require(ctx, actor, groupID, "group.member.manage"); err != nil {
		return nil, err
	}
	states := []AccessState{}
	latest := s.DB.WithContext(ctx).Model(&model.Operation{}).Select("operation_id").
		Where("action = ? AND target_key = CONCAT(a.group_id,'/',a.user_id)", "access.reconcile").Order("id DESC").Limit(1)
	err := s.DB.WithContext(ctx).Table("tb_group_access a").
		Select("a.user_id,u.username,u.status AS user_status,a.desired_level,a.observed_level,a.desired_version,a.applied_version,a.status,COALESCE(a.observed_objects,JSON_ARRAY()) AS objects,a.observed_at,a.last_error,(?) AS operation_id", latest).
		Joins("JOIN tb_user u ON u.id = a.user_id").Where("a.group_id = ? AND a.user_id > ?", groupID, after).
		Order("a.user_id").Limit(limit).Scan(&states).Error
	return states, err
}

type Audit struct {
	ID          uint64  `json:"id"`
	OperationID *string `json:"operationId"`
	ActorID     *uint64 `json:"actorId"`
	ActorName   string  `json:"actorName"`
	GroupID     *uint64 `json:"groupId"`
	Action      string  `json:"action"`
	TargetType  string  `json:"targetType"`
	TargetID    string  `json:"targetId"`
	Outcome     string  `json:"outcome"`
	CreatedAt   string  `json:"createdAt"`
}

func (s *Service) Audits(ctx context.Context, actor, after uint64, limit int) ([]Audit, error) {
	if err := s.Require(ctx, actor, 0, "audit.read"); err != nil {
		return nil, err
	}
	records := []model.Audit{}
	if err := s.DB.WithContext(ctx).Where("id > ?", after).Order("id").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	result := []Audit{}
	for _, a := range records {
		result = append(result, Audit{ID: a.ID, OperationID: a.OperationID, ActorID: a.ActorID, ActorName: a.ActorName, GroupID: a.GroupID,
			Action: a.Action, TargetType: a.TargetType, TargetID: a.TargetID, Outcome: a.Outcome, CreatedAt: a.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000Z")})
	}
	return result, nil
}

// Retry 只重置失败的持久任务；不得直接重放 SUCCEEDED 或过时目标。
func (s *Service) Retry(ctx context.Context, actor uint64, key, id string) (*model.Operation, error) {
	original, err := s.Store.Operation(ctx, id)
	if err != nil {
		return nil, domainError(err)
	}
	// ML 重试复用原动作权限而非 role.manage，并在 mutate 的 guard 中
	// 重新校验当前授权与对象范围；同幂等键重放也不能绕过撤权。
	if IsMLTarget(original.TargetType) {
		return s.RetryMLOperation(ctx, actor, key, id)
	}
	return s.mutate(ctx, actor, 0, key, "role.manage", "operation.retry", "operation", id, map[string]string{"operationId": id}, func(st *repository.Store, o *model.Operation) error {
		original, err := st.Operation(ctx, id)
		if err != nil {
			return err
		}
		if original.TargetType == "notebook" {
			if err := prepareNotebookRetry(ctx, st, original); err != nil {
				return err
			}
		}
		if err := prepareLifecycleRetry(ctx, st, original); err != nil {
			return err
		}
		res := st.DB.WithContext(ctx).Model(&model.Operation{}).Where("operation_id = ? AND status = ?", id, "FAILED").
			Updates(map[string]any{"status": "PENDING", "attempt": 0, "next_attempt_at": gorm.Expr("UTC_TIMESTAMP(3)"), "deadline_at": nil,
				"last_error": nil, "lease_owner": nil, "lease_until": nil, "finished_at": nil})
		if res.Error != nil {
			return res.Error
		}
		count := res.RowsAffected
		if count != 1 {
			return apperr.New(apperr.CodeConflict, "任务不存在或不是失败状态")
		}
		return result(o, map[string]string{"operationId": id, "status": "PENDING"})
	})
}
