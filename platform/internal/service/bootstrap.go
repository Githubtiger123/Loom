package service

import (
	"context"
	"errors"
	"fmt"

	inframysql "github.com/githubtiger123/loom/platform/internal/infra/mysql"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Bootstrap 只由显式 CLI 命令调用。不会因某人登录名为 admin 而自动升级。
func (s *Service) Bootstrap(ctx context.Context, issuer, subject, username string) error {
	if !validator.RequiredText(issuer, 255) || !validator.RequiredText(subject, 255) || !validator.RequiredText(username, 255) {
		return fmt.Errorf("bootstrap 必须显式指定 issuer、subject、username")
	}
	// 首次初始化还没有 platform_admin 行可锁；用固定会话锁串行化种子创建。
	lock, err := inframysql.AcquireLock(ctx, s.DB, "loom.identity.bootstrap", 10)
	if err != nil {
		return err
	}
	defer lock.Close()
	return repository.InTx(ctx, lock.DB, func(st *repository.Store) error {
		// INSERT 是幂等初始化；既有角色与自定义角色不会被重写。
		for _, p := range rbac.Permissions() {
			if err := st.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&p).Error; err != nil {
				return err
			}
		}
		for _, r := range []model.Role{
			{Code: "platform_admin", Name: "平台管理员", Scope: "global", AccessLevel: "none"},
			{Code: "platform_user", Name: "平台用户", Scope: "global", AccessLevel: "none", Permissions: []string{"group.create"}},
			{Code: "auditor", Name: "审计员", Scope: "global", AccessLevel: "none", Permissions: []string{"audit.read"}},
			{Code: "group_owner", Name: "空间负责人", Scope: "group", AccessLevel: "edit"},
			{Code: "group_editor", Name: "空间开发者", Scope: "group", AccessLevel: "edit"},
			{Code: "group_viewer", Name: "空间观察者", Scope: "group", AccessLevel: "view"},
		} {
			// 先查再建且只处理新建角色，不依赖 MySQL clientFoundRows 的计数语义。
			var existing model.Role
			err := st.DB.WithContext(ctx).Where("code = ?", r.Code).First(&existing).Error
			if err == nil {
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			r.Builtin, r.Enabled, r.Version = true, true, 1
			if err := st.DB.WithContext(ctx).Create(&r).Error; err != nil {
				return err
			}
			id := r.ID
			codes := r.Permissions
			codes = append(codes, rbac.BuiltinPermissionCodes(r.Code)...)
			for _, code := range codes {
				if err := st.AttachPermission(ctx, id, code); err != nil {
					return err
				}
			}
		}
		if err := lockAuthorization(ctx, st); err != nil {
			return err
		}
		u, err := st.Identity(ctx, issuer, subject)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			u = &model.User{Issuer: issuer, Subject: &subject, Username: username, DisplayName: username, Status: "ACTIVE"}
			if err := st.InsertUser(ctx, u); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if u.Username != username || u.Status != "ACTIVE" {
			return fmt.Errorf("管理员身份与既有记录不符或已被禁用；拒绝自动修改")
		}
		var count int64
		if err := st.DB.WithContext(ctx).Table("tb_role_binding b").Joins("JOIN tb_role r ON r.id = b.role_id").
			Where("r.code = ? AND b.scope_type = ? AND b.scope_id = 0 AND b.status = ?", "platform_admin", "global", "ACTIVE").Count(&count).Error; err != nil {
			return err
		}
		// 初始化只允许首个管理员；后续通过已经授权的管理接口操作。
		if count > 0 {
			admin, err := st.IsAdmin(ctx, u.ID)
			if err != nil {
				return err
			}
			if !admin {
				return fmt.Errorf("平台已初始化，不能通过 bootstrap 增加其他管理员")
			}
			return nil
		}
		roleID, err := st.RoleID(ctx, "platform_admin")
		if err != nil {
			return err
		}
		if err := st.GrantBinding(ctx, &model.Binding{UserID: u.ID, RoleID: roleID, Scope: "global", GrantedBy: u.ID}, false); err != nil {
			return err
		}
		return audit(ctx, st, u.ID, 0, "platform.bootstrap", "user", fmt.Sprint(u.ID), "", "SUCCEEDED")
	})
}
