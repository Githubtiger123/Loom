package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) Audits(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	after, limit, err := page(r)
	if err != nil {
		return nil, err
	}
	return a.service.Audits(ctx, u.ID, after, limit)
}
