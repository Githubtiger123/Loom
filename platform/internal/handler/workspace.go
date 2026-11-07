package handler

import (
	"context"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"net/http"
)

func (a *API) Workspaces(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	after, limit, err := page(r)
	if err != nil {
		return nil, err
	}
	return a.service.Workspaces(ctx, u.ID, gid, after, limit)
}
func (a *API) Workspace(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	id, err := pathID(r, "wid")
	if err != nil {
		return nil, err
	}
	return a.service.Workspace(ctx, u.ID, gid, id)
}
func (a *API) DeleteWorkspace(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	id, err := pathID(r, "wid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.DeleteWorkspaceReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.DeleteWorkspace(ctx, u.ID, gid, id, requestKey(r), in)
}
func (a *API) TransferWorkspaceOwner(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	id, err := pathID(r, "wid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.TransferResourceReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.TransferWorkspaceOwner(ctx, u.ID, gid, id, requestKey(r), in)
}
func (a *API) AttachWorkspace(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	id, err := pathID(r, "wid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.WorkspaceActionReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.AttachWorkspace(ctx, u.ID, gid, id, requestKey(r), in)
}
func (a *API) DetachWorkspace(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	id, err := pathID(r, "wid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.WorkspaceActionReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.DetachWorkspace(ctx, u.ID, gid, id, requestKey(r), in)
}
