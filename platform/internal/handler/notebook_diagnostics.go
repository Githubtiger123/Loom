package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) NotebookEvents(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			return nil, apperr.New(apperr.CodeInvalid, "limit 必须为整数")
		}
	}
	return a.service.NotebookEvents(ctx, u.ID, groupID, id, limit)
}
func (a *API) NotebookLogs(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	values := r.URL.Query()
	for key, items := range values {
		if len(items) != 1 {
			return nil, apperr.New(apperr.CodeInvalid, "日志参数不允许重复")
		}
		switch key {
		case "pod", "podUID", "tailLines", "limitBytes", "sinceSeconds", "previous":
		default:
			return nil, apperr.New(apperr.CodeInvalid, "不支持的日志参数")
		}
	}
	query := dto.NotebookLogQuery{Pod: values.Get("pod"), PodUID: values.Get("podUID")}
	for key, target := range map[string]*int64{"tailLines": &query.TailLines, "limitBytes": &query.LimitBytes, "sinceSeconds": &query.SinceSeconds} {
		if values.Has(key) {
			*target, err = strconv.ParseInt(values.Get(key), 10, 64)
			if err != nil || *target <= 0 {
				return nil, apperr.New(apperr.CodeInvalid, "日志范围参数必须为正整数")
			}
		}
	}
	if values.Has("previous") {
		query.Previous, err = strconv.ParseBool(values.Get("previous"))
		if err != nil {
			return nil, apperr.New(apperr.CodeInvalid, "previous 必须为布尔值")
		}
	}
	return a.service.NotebookLogs(ctx, u.ID, groupID, id, query)
}
func (a *API) NotebookDiagnostics(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	return a.service.NotebookDiagnostics(ctx, u.ID, groupID, id)
}
func (a *API) NotebookUsage(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := notebookIDs(r)
	if err != nil {
		return nil, err
	}
	return a.service.NotebookUsage(ctx, u.ID, groupID, id)
}
