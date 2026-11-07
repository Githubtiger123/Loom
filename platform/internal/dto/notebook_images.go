package dto

import (
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type NotebookImageListQuery struct {
	Category string
}

func (q *NotebookImageListQuery) Validate() error {
	q.Category = strings.TrimSpace(q.Category)
	if q.Category != "" && !validator.IsNotebookImageCategory(q.Category) {
		return apperr.New(apperr.CodeInvalid, "category 只支持 jupyter、vscode、rstudio")
	}
	return nil
}

// NotebookImageOption 仅返回选择器需要的字段，不暴露实际仓库地址。
type NotebookImageOption struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	ServerType string `json:"serverType"`
}

type NotebookImageList struct {
	Items []NotebookImageOption `json:"items"`
}
