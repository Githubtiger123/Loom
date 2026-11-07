package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
)

func (a *API) Groups(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	after, limit, err := page(r)
	if err != nil {
		return nil, err
	}
	return a.service.Groups(ctx, u.ID, after, limit)
}

func (a *API) CreateGroup(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	in, err := decode[dto.CreateGroupReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.CreateGroup(ctx, u.ID, requestKey(r), in)
}

func (a *API) Group(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	return a.service.Group(ctx, u.ID, id)
}

func (a *API) UpdateGroup(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	in, err := decode[service.GroupMetadata](r)
	if err != nil {
		return nil, err
	}
	return a.service.UpdateGroup(ctx, u.ID, id, requestKey(r), in)
}
