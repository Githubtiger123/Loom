package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Access = model.GroupAccess

func (s *Store) Access(ctx context.Context, groupID, userID uint64, lock bool) (*Access, error) {
	a := &Access{}
	err := s.query(ctx, lock).Where("group_id = ? AND user_id = ?", groupID, userID).First(a).Error
	if len(a.Objects) == 0 {
		a.Objects = []byte("[]")
	}
	return a, err
}

// 调用方必须已持有授权串行锁。先锁旧行，再按等级变化增版本，避免 upsert 赋值顺序歧义。
func (s *Store) SetDesiredAccess(ctx context.Context, groupID, userID uint64, level string) (*Access, error) {
	a, err := s.Access(ctx, groupID, userID, true)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		a = &Access{GroupID: groupID, UserID: userID, DesiredLevel: level, Objects: []byte("[]")}
		return a, s.query(ctx, false).Create(a).Error
	}
	if err != nil {
		return nil, err
	}
	if a.DesiredLevel != level {
		a.DesiredLevel, a.Status = level, "PENDING"
		a.DesiredVersion++
		err = s.query(ctx, false).Model(&Access{}).Where("group_id = ? AND user_id = ?", groupID, userID).
			Updates(map[string]any{"desired_level": level, "desired_version": a.DesiredVersion, "status": a.Status}).Error
	}
	return a, err
}

func (s *Store) RecordSpace(ctx context.Context, groupID uint64, profileUID, namespaceUID string, quota any) error {
	b, err := json.Marshal(quota)
	if err != nil {
		return err
	}
	updates := map[string]any{"observed_at": gorm.Expr("UTC_TIMESTAMP(3)")}
	if profileUID != "" {
		updates["profile_uid"] = gorm.Expr("COALESCE(profile_uid, ?)", profileUID)
	}
	if namespaceUID != "" {
		updates["namespace_uid"] = gorm.Expr("COALESCE(namespace_uid, ?)", namespaceUID)
	}
	if string(b) != "null" {
		updates["quota_observed"] = string(b)
	}
	return s.query(ctx, false).Model(&model.Group{}).Where("id = ?", groupID).
		Where("profile_uid IS NULL OR profile_uid = ?", profileUID).
		Where("namespace_uid IS NULL OR namespace_uid = ? OR ? = ''", namespaceUID, namespaceUID).Updates(updates).Error
}

// GrantBinding 用复合唯一键恢复既有绑定；ignore 模式不会重启已撤销的默认角色。
func (s *Store) GrantBinding(ctx context.Context, b *model.Binding, ignore bool) error {
	conflict := clause.OnConflict{DoNothing: true}
	if !ignore {
		conflict = clause.OnConflict{DoUpdates: clause.Assignments(map[string]any{
			"status": "ACTIVE", "expires_at": b.ExpiresAt, "granted_by": b.GrantedBy,
			"granted_at": gorm.Expr("UTC_TIMESTAMP(3)"), "revoked_by": nil, "revoked_at": nil,
			"version": gorm.Expr("version + 1"),
		})}
	}
	return s.query(ctx, false).Clauses(conflict).Create(b).Error
}
func (s *Store) RevokeBindings(ctx context.Context, actor, userID, groupID uint64, scope string, roleID *uint64) error {
	db := s.query(ctx, false).Model(&model.Binding{}).
		Where("user_id = ? AND scope_type = ? AND scope_id = ? AND status = ?", userID, scope, groupID, "ACTIVE")
	if roleID != nil {
		db = db.Where("role_id = ?", *roleID)
	}
	return db.Updates(map[string]any{"status": "REVOKED", "revoked_by": actor,
		"revoked_at": gorm.Expr("UTC_TIMESTAMP(3)"), "version": gorm.Expr("version + 1")}).Error
}
