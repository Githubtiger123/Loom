package dto

import (
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
)

type WorkspaceActionReq struct {
	Revision         uint64 `json:"revision"`
	NotebookID       uint64 `json:"notebookId"`
	NotebookRevision uint64 `json:"notebookRevision"`
}

func (r WorkspaceActionReq) Validate() error {
	if r.Revision == 0 || r.NotebookID == 0 || r.NotebookRevision == 0 {
		return apperr.New(apperr.CodeInvalid, "必须提供目录、实例 ID 和各自 revision")
	}
	return nil
}

type DeleteWorkspaceReq struct {
	LifecycleReq
	ReleaseRetention bool `json:"releaseRetention"`
}
type WorkspaceView struct {
	model.Workspace
	NotebookIDs []uint64 `json:"notebookIds"`
}
type WorkspacePage struct {
	Items     []WorkspaceView `json:"items"`
	NextAfter *uint64         `json:"nextAfter,omitempty"`
}
