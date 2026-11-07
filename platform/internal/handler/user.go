package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
)

func (a *API) Me(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.service.Me(ctx, u)
}

func (a *API) Users(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	after, limit, err := page(r)
	if err != nil {
		return nil, err
	}
	return a.service.Users(ctx, u.ID, after, limit)
}

func (a *API) CreateUser(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	in, err := decode[service.RegisterUser](r)
	if err != nil {
		return nil, err
	}
	return a.service.CreateUser(ctx, u.ID, requestKey(r), in)
}

func (a *API) ReplaceUserRoles(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "uid")
	if err != nil {
		return nil, err
	}
	in, err := decode[service.ReplaceRoles](r)
	if err != nil {
		return nil, err
	}
	return a.service.ReplaceRoles(ctx, u.ID, id, 0, requestKey(r), in)
}

func (a *API) EnableUser(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.userStatus(ctx, u, r, "ACTIVE")
}

func (a *API) DisableUser(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.userStatus(ctx, u, r, "DISABLED")
}

func (a *API) userStatus(ctx context.Context, u *model.User, r *http.Request, status string) (any, error) {
	id, err := pathID(r, "uid")
	if err != nil {
		return nil, err
	}
	in, err := decode[service.UserState](r)
	if err != nil {
		return nil, err
	}
	if a.auth != nil {
		return a.service.UserStatus(ctx, u.ID, id, requestKey(r), status, in, a.auth.Config.Issuer)
	}
	return a.service.UserStatus(ctx, u.ID, id, requestKey(r), status, in)
}
