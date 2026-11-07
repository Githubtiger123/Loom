package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/rbac"
	"github.com/githubtiger123/loom/platform/internal/repository"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
)

type SaveRole struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Scope       string   `json:"scope"`
	AccessLevel string   `json:"accessLevel"`
	Enabled     bool     `json:"enabled"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (s *Service) SaveRole(ctx context.Context, actor, id uint64, key string, in SaveRole) (*model.Operation, error) {
	if !validator.IsRoleCode(in.Code) || !validator.RequiredText(in.Name, 128) || len(in.Description) > 1000 {
		return nil, apperr.New(apperr.CodeInvalid, "角色编码、名称或描述无效")
	}
	if in.Scope != "global" && in.Scope != "group" {
		return nil, apperr.New(apperr.CodeInvalid, "角色范围无效")
	}
	if in.AccessLevel != "none" && in.AccessLevel != "view" && in.AccessLevel != "edit" {
		return nil, apperr.New(apperr.CodeInvalid, "基础访问级别无效")
	}
	if in.Scope == "global" && in.AccessLevel != "none" {
		return nil, apperr.New(apperr.CodeInvalid, "全局角色不能隐式授予空间访问")
	}
	dictionary := map[string]model.Permission{}
	for _, p := range rbac.Permissions() {
		dictionary[p.Code] = p
	}
	seen := map[string]bool{}
	for _, code := range in.Permissions {
		p, ok := dictionary[code]
		if !ok || p.Scope != in.Scope || seen[code] {
			return nil, apperr.New(apperr.CodeInvalid, "权限码不存在、重复或范围不符")
		}
		seen[code] = true
		if strings.HasPrefix(code, "notebook.") || strings.HasPrefix(code, "workspace.") || strings.HasPrefix(code, "pipeline.") || strings.HasPrefix(code, "trainer.") || strings.HasPrefix(code, "kserve.") {
			readOnly := strings.HasSuffix(code, ".read") || strings.HasSuffix(code, ".read.any")
			if (!readOnly && in.AccessLevel != "edit") || (readOnly && in.AccessLevel == "none") {
				return nil, apperr.New(apperr.CodeInvalid, "工作负载/目录权限与基础访问级别不匹配")
			}
		}
	}
	return s.mutate(ctx, actor, 0, key, "role.manage", "role.save", "role", fmt.Sprint(id), in, func(st *repository.Store, o *model.Operation) error {
		if id > 0 {
			r, err := st.Role(ctx, id, true)
			if err != nil {
				return err
			}
			if r.Builtin {
				return apperr.New(apperr.CodeForbidden, "内置角色只能通过版本升级调整")
			}
			if r.Code != in.Code || r.Scope != in.Scope {
				return apperr.New(apperr.CodeInvalid, "角色编码与范围不可修改")
			}
			if err := st.DB.WithContext(ctx).Model(&model.Role{}).Where("id = ?", id).Updates(map[string]any{
				"name": in.Name, "access_level": in.AccessLevel, "enabled": in.Enabled, "description": in.Description, "version": gorm.Expr("version + 1"),
			}).Error; err != nil {
				return err
			}
			if err := st.DB.WithContext(ctx).Where("role_id = ?", id).Delete(&model.RolePermission{}).Error; err != nil {
				return err
			}
		} else {
			r := &model.Role{Code: in.Code, Name: in.Name, Scope: in.Scope, AccessLevel: in.AccessLevel, Enabled: in.Enabled, Description: in.Description, Version: 1}
			if err := st.DB.WithContext(ctx).Create(r).Error; err != nil {
				return err
			}
			id = r.ID
		}
		for _, code := range in.Permissions {
			if err := st.AttachPermission(ctx, id, code); err != nil {
				return err
			}
		}
		list := []model.Binding{}
		if err := st.DB.WithContext(ctx).Where("role_id = ? AND status = ?", id, "ACTIVE").Order("user_id,scope_id").Find(&list).Error; err != nil {
			return err
		}
		for _, a := range list {
			if err := st.BumpUserVersion(ctx, a.UserID); err != nil {
				return err
			}
			if a.ScopeID > 0 {
				if err := refreshAccess(ctx, st, a.ScopeID, a.UserID); err != nil {
					return err
				}
			}
		}
		o.TargetKey = fmt.Sprint(id)
		return result(o, map[string]any{"id": id, "affectedBindings": len(list)})
	})
}
