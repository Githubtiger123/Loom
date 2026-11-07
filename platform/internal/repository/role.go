package repository

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

func (s *Store) Role(ctx context.Context, id uint64, lock bool) (*model.Role, error) {
	r := &model.Role{Permissions: []string{}}
	if err := s.query(ctx, lock).First(r, "id = ?", id).Error; err != nil {
		return nil, err
	}
	err := s.query(ctx, false).Table("tb_role_permission rp").
		Joins("JOIN tb_permission p ON p.id = rp.permission_id").
		Where("rp.role_id = ?", id).Order("p.code").Pluck("p.code", &r.Permissions).Error
	return r, err
}
func (s *Store) Roles(ctx context.Context) ([]model.Role, error) {
	ids := []uint64{}
	if err := s.query(ctx, false).Model(&model.Role{}).Order("id").Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	roles := []model.Role{}
	for _, id := range ids {
		role, err := s.Role(ctx, id, false)
		if err != nil {
			return nil, err
		}
		roles = append(roles, *role)
	}
	return roles, nil
}
func (s *Store) RoleID(ctx context.Context, code string) (uint64, error) {
	var role model.Role
	err := s.query(ctx, false).Select("id").Where("code = ? AND enabled = ?", code, true).First(&role).Error
	return role.ID, err
}
func (s *Store) Permissions(ctx context.Context) ([]model.Permission, error) {
	permissions := []model.Permission{}
	err := s.query(ctx, false).Order("id").Find(&permissions).Error
	return permissions, err
}

// effective 统一有效性条件；仅业务绑定能产生授权。
func (s *Store) effective(ctx context.Context) *gorm.DB {
	return s.query(ctx, false).Table("tb_role_binding b").
		Joins("JOIN tb_role r ON r.id = b.role_id").
		Joins("JOIN tb_user u ON u.id = b.user_id").
		Where("b.status = ? AND u.status = ? AND r.enabled = ?", "ACTIVE", "ACTIVE", true).
		Where("b.expires_at IS NULL OR b.expires_at > UTC_TIMESTAMP(3)").
		Where("b.scope_type = r.scope_type")
}
func (s *Store) IsAdmin(ctx context.Context, userID uint64) (bool, error) {
	var count int64
	err := s.effective(ctx).Where("b.user_id = ? AND r.code = ? AND b.scope_type = ? AND b.scope_id = 0",
		userID, "platform_admin", "global").Count(&count).Error
	return count > 0, err
}
func (s *Store) HasPermission(ctx context.Context, userID, groupID uint64, permission string) (bool, error) {
	var count int64
	scope := "global"
	if groupID > 0 {
		scope = "group"
	}
	err := s.effective(ctx).
		Joins("JOIN tb_role_permission rp ON rp.role_id = r.id").
		Joins("JOIN tb_permission p ON p.id = rp.permission_id").
		Where("b.user_id = ? AND p.code = ? AND p.scope_type = r.scope_type", userID, permission).
		Where("b.scope_type = ? AND b.scope_id = ?", scope, groupID).Count(&count).Error
	return count > 0, err
}
func (s *Store) Bindings(ctx context.Context, userID, groupID uint64) ([]model.Binding, error) {
	scope := "global"
	if groupID > 0 {
		scope = "group"
	}
	bindings := []model.Binding{}
	err := s.effective(ctx).Select("b.*, r.code AS role_code").
		Where("b.user_id = ? AND b.scope_type = ? AND b.scope_id = ?", userID, scope, groupID).
		Order("b.id").Scan(&bindings).Error
	return bindings, err
}
func (s *Store) AdminCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.effective(ctx).Where("r.code = ? AND b.scope_type = ? AND b.scope_id = 0", "platform_admin", "global").
		Distinct("b.user_id").Count(&count).Error
	return count, err
}

func (s *Store) AttachPermission(ctx context.Context, roleID uint64, code string) error {
	var permission model.Permission
	if err := s.query(ctx, false).Where("code = ?", code).First(&permission).Error; err != nil {
		return err
	}
	return s.query(ctx, false).Create(&model.RolePermission{RoleID: roleID, PermissionID: permission.ID}).Error
}
