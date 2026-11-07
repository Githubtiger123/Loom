package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func notebookIDs(r *http.Request) (uint64, uint64, error) {
	groupID, err := pathID(r, "gid")
	if err != nil {
		return 0, 0, err
	}
	id, err := pathID(r, "nid")
	return groupID, id, err
}

func (a *API) NotebookImages(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	query := dto.NotebookImageListQuery{Category: r.URL.Query().Get("category")}
	return a.service.ListNotebookImages(ctx, u.ID, groupID, query)
}

func (a *API) Notebooks(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	after, limit, err := page(r)
	if err != nil {
		return nil, err
	}
	values := r.URL.Query()
	filter := dto.NotebookListQuery{After: after, Limit: limit, Scope: values.Get("scope"), Search: values.Get("search"), Status: values.Get("status"), ServerType: values.Get("serverType")}
	if values.Has("includeDeleted") {
		filter.IncludeDeleted, err = strconv.ParseBool(values.Get("includeDeleted"))
		if err != nil {
			return nil, apperr.New(apperr.CodeInvalid, "includeDeleted 必须为布尔值")
		}
	}
	return a.service.Notebooks(ctx, u.ID, groupID, filter)
}

func (a *API) CreateNotebook(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, err := pathID(r, "gid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.CreateNotebookReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.CreateNotebook(ctx, u.ID, groupID, requestKey(r), in)
}

func (a *API) Notebook(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	return a.service.Notebook(ctx, u.ID, groupID, id)
}

func (a *API) NotebookStatus(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	return a.service.NotebookStatus(ctx, u.ID, groupID, id)
}

func (a *API) NotebookAccess(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	access, err := a.service.NotebookAccess(ctx, u.ID, groupID, id)
	if err != nil {
		return nil, err
	}
	if access.URL != "" && a.auth != nil && a.auth.Config.NotebookOrigin != "" {
		access.URL = a.auth.Config.NotebookOrigin + access.URL
	}
	return access, nil
}

func (a *API) UpdateNotebook(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.UpdateNotebookReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.UpdateNotebook(ctx, u.ID, groupID, id, requestKey(r), in)
}

func (a *API) StartNotebook(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.NotebookActionReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.StartNotebook(ctx, u.ID, groupID, id, requestKey(r), in)
}

func (a *API) StopNotebook(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.NotebookActionReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.StopNotebook(ctx, u.ID, groupID, id, requestKey(r), in)
}

func (a *API) DeleteNotebook(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.DeleteNotebookReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.DeleteNotebook(ctx, u.ID, groupID, id, requestKey(r), in)
}

func (a *API) RetryNotebook(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.RetryNotebookReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.RetryNotebook(ctx, u.ID, groupID, id, requestKey(r), in)
}
