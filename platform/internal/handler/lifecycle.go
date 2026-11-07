package handler

import (
	"context"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"net/http"
)

func (a *API) DisableGroup(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.LifecycleReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.DisableGroup(ctx, u.ID, id, requestKey(r), in)
}
func (a *API) RestoreGroup(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.LifecycleReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.RestoreGroup(ctx, u.ID, id, requestKey(r), in)
}
func (a *API) DeleteGroup(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.LifecycleReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.DeleteGroup(ctx, u.ID, id, requestKey(r), in)
}
func (a *API) TransferNotebookOwner(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	id, err := pathID(r, "nid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.TransferResourceReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.TransferNotebookOwner(ctx, u.ID, gid, id, requestKey(r), in)
}
