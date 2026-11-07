package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
)

func (a *API) Roles(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.service.Roles(ctx, u.ID)
}

func (a *API) Permissions(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.service.Permissions(ctx, u.ID)
}

func (a *API) CreateRole(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	in, err := decode[service.SaveRole](r)
	if err != nil {
		return nil, err
	}
	return a.service.SaveRole(ctx, u.ID, 0, requestKey(r), in)
}

func (a *API) UpdateRole(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "rid")
	if err != nil {
		return nil, err
	}
	in, err := decode[service.SaveRole](r)
	if err != nil {
		return nil, err
	}
	return a.service.SaveRole(ctx, u.ID, id, requestKey(r), in)
}
