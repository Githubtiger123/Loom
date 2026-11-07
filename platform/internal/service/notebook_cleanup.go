package service

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/repository"
)

// notebookCleanupGroup 仅供 stop/delete 及其重试使用。冻结空间仍须允许安全清理，
// 但不能借此放宽 create/start/update/关联/转移等使用 notebookGroup 的业务入口。
func notebookCleanupGroup(ctx context.Context, st *repository.Store, groupID uint64) (*model.Group, error) {
	g, err := st.Group(ctx, groupID, true)
	if err != nil {
		return nil, err
	}
	if g.DeletedAt == nil {
		switch g.Status {
		case "ACTIVE", "DEGRADED", "DISABLING", "DISABLED":
			return g, nil
		}
	}
	return nil, apperr.New(apperr.CodeConflict, "空间当前不允许 Notebook 停止或删除")
}
