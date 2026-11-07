package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
)

func (a *API) Members(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	return a.service.Members(ctx, u.ID, id)
}

func (a *API) Accesses(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	after, limit, err := page(r)
	if err != nil {
		return nil, err
	}
	return a.service.Accesses(ctx, u.ID, id, after, limit)
}

func (a *API) AvailableRoles(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	return a.service.AvailableRoles(ctx, u.ID, id)
}

func (a *API) ReplaceMemberRoles(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	uid, err := pathID(r, "uid")
	if err != nil {
		return nil, err
	}
	in, err := decode[service.ReplaceRoles](r)
	if err != nil {
		return nil, err
	}
	return a.service.ReplaceRoles(ctx, u.ID, uid, gid, requestKey(r), in)
}

func (a *API) RemoveMember(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	uid, err := pathID(r, "uid")
	if err != nil {
		return nil, err
	}
	return a.service.ReplaceRoles(ctx, u.ID, uid, gid, requestKey(r), service.ReplaceRoles{Roles: []service.RoleGrant{}})
}

func (a *API) TransferOwner(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	in, err := decode[service.TransferOwner](r)
	if err != nil {
		return nil, err
	}
	return a.service.TransferOwner(ctx, u.ID, id, requestKey(r), in)
}
