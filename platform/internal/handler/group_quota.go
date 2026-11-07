package handler

import (
	"context"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
	"net/http"
)

func (a *API) GroupQuota(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	return a.service.GroupQuota(ctx, u.ID, id)
}
func (a *API) UpdateGroupQuota(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	id, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.UpdateGroupQuotaReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.UpdateGroupQuota(ctx, u.ID, id, requestKey(r), in)
}
