// Package rbac 定义权限字典和无缓存的后端权限判断。
package rbac

import (
	"context"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

func Permissions() []model.Permission {
	result := []model.Permission{}
	for _, entry := range []struct{ scope, codes string }{
		{"global", "user.read user.manage role.manage group.create group.quota.update audit.read"},
		{"group", "pipeline.update pipeline.version.update"},
		{"group", "group.read group.update group.delete group.disable group.restore group.member.manage group.owner.transfer group.quota.read notebook.create notebook.read notebook.read.any notebook.update notebook.delete notebook.stop notebook.start notebook.connect notebook.transfer notebook.transfer.any workspace.read workspace.attach workspace.delete workspace.transfer workspace.manage.any pipeline.create pipeline.read pipeline.read.any pipeline.version.create pipeline.run pipeline.stop pipeline.delete trainer.create trainer.read trainer.read.any trainer.stop trainer.delete kserve.create kserve.read kserve.read.any kserve.update kserve.deploy kserve.stop kserve.delete"},
	} {
		for _, code := range strings.Fields(entry.codes) {
			result = append(result, model.Permission{Code: code, Name: code, Scope: entry.scope, Module: strings.SplitN(code, ".", 2)[0]})
		}
	}
	return result
}

// 内置角色的新增能力仅在显式版本迁移时升级，bootstrap 只用于全新角色。
func BuiltinPermissionCodes(role string) []string {
	result := []string{}
	for _, p := range Permissions() {
		allow := false
		switch role {
		case "platform_admin":
			allow = p.Scope == "global"
		case "group_owner":
			allow = p.Scope == "group"
		case "group_editor":
			allow = p.Scope == "group" && (p.Code == "group.read" || p.Code == "group.quota.read" ||
				strings.HasPrefix(p.Code, "notebook.") || strings.HasPrefix(p.Code, "workspace.") ||
				strings.HasPrefix(p.Code, "pipeline.") || strings.HasPrefix(p.Code, "trainer.") || strings.HasPrefix(p.Code, "kserve."))
			if p.Code == "workspace.manage.any" || p.Code == "notebook.transfer.any" ||
				p.Code == "pipeline.read.any" || p.Code == "trainer.read.any" || p.Code == "kserve.read.any" {
				allow = false
			}
		case "group_viewer":
			allow = p.Scope == "group" && (p.Code == "group.read" || p.Code == "group.quota.read" ||
				p.Code == "notebook.read" || p.Code == "workspace.read" || p.Code == "pipeline.read" || p.Code == "trainer.read" || p.Code == "kserve.read")
		}
		if allow {
			result = append(result, p.Code)
		}
	}
	return result
}

type Checker struct{ Store *repository.Store }

func (c Checker) Require(ctx context.Context, userID, groupID uint64, permission string) error {
	admin, err := c.Store.IsAdmin(ctx, userID)
	if err != nil {
		return err
	}
	if admin {
		return nil
	}
	allowed, err := c.Store.HasPermission(ctx, userID, groupID, permission)
	if err != nil {
		return err
	}
	if !allowed {
		return apperr.New(apperr.CodeForbidden, "没有执行此操作的权限")
	}
	return nil
}
