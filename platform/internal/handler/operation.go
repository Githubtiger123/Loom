package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) Operation(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.service.Operation(ctx, u.ID, r.PathValue("oid"))
}

func (a *API) RetryOperation(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.service.Retry(ctx, u.ID, requestKey(r), r.PathValue("oid"))
}
