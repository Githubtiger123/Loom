package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) PipelineRunDAG(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	return a.service.PipelineRunDAG(ctx, u.ID, groupID, id)
}

func (a *API) PipelineRunNode(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	return a.service.PipelineRunNode(ctx, u.ID, groupID, id, r.PathValue("nodeId"), r.URL.Query().Get("instanceId"))
}
