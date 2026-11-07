package service

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
)

// ListNotebookImages 使用与创建相同的可信目录，不查询 Kubernetes 或镜像仓库。
func (s *Service) ListNotebookImages(ctx context.Context, actor, groupID uint64, query dto.NotebookImageListQuery) (*dto.NotebookImageList, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if err := s.Require(ctx, actor, groupID, "notebook.create"); err != nil {
		return nil, err
	}
	g, err := s.Store.Group(ctx, groupID, false)
	if err != nil {
		return nil, domainError(err)
	}
	if g.DeletedAt != nil || g.Status == "DELETED" {
		return nil, apperr.New(apperr.CodeNotFound, "空间不存在")
	}
	if s.NotebookImages == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "Notebook 镜像目录尚未配置")
	}
	result := &dto.NotebookImageList{Items: []dto.NotebookImageOption{}}
	for _, entry := range s.NotebookImages.Entries() {
		if !entry.Enabled || (query.Category != "" && entry.Category != query.Category) {
			continue
		}
		result.Items = append(result.Items, dto.NotebookImageOption{
			ID: entry.ID, Name: entry.Name, Category: entry.Category, ServerType: entry.ServerType,
		})
	}
	return result, nil
}
