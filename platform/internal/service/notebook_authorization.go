package service

import (
	"context"
	"errors"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"gorm.io/gorm"
)

// AuthorizeNotebookPath 用于真实 Notebook HTTP/WS 入口，而不只是“打开链接”按钮。
// 这里判断访问资格，不把数据库投影当成路由可达性或实时运行证明。
func (s *Service) AuthorizeNotebookPath(ctx context.Context, actor uint64, namespace, name string) error {
	if !validator.IsDNS1123Label(namespace) || !validator.IsDNS1123Label(name) {
		return apperr.New(apperr.CodeForbidden, "Notebook 路径无效")
	}
	var group model.Group
	err := s.DB.WithContext(ctx).Where("cluster = ? AND name = ? AND status = ? AND deleted_at IS NULL", s.Cluster, namespace, "ACTIVE").First(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperr.New(apperr.CodeForbidden, "无权访问该空间")
	}
	if err != nil {
		return err
	}
	if err := s.Require(ctx, actor, group.ID, "notebook.connect"); err != nil {
		return err
	}
	var n model.Notebook
	err = s.DB.WithContext(ctx).Where("group_id = ? AND cluster = ? AND namespace = ? AND name = ? AND deleted_at IS NULL", group.ID, group.Cluster, namespace, name).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperr.New(apperr.CodeForbidden, "Notebook 不存在或不可访问")
	}
	if err != nil {
		return err
	}
	if err := notebookAllowed(ctx, s.Store, actor, &n, "notebook.connect"); err != nil {
		return err
	}
	if n.DesiredState != "RUNNING" || n.Status == "DELETING" {
		return apperr.New(apperr.CodeForbidden, "Notebook 正在停止或删除，不接受新连接")
	}
	return nil
}
