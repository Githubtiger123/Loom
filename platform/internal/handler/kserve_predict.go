package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) PredictKServe(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, id, err := mlIDs(r, "sid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.KServePredictReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.PredictKServe(ctx, u.ID, gid, id, in)
}
