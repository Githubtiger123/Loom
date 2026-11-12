package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) KServeRuntimes(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, _, err := mlIDs(r, "")
	if err != nil {
		return nil, err
	}
	return a.service.KServeRuntimes(ctx, u.ID, gid)
}

func (a *API) KServePreflight(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, _, err := mlIDs(r, "")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.CreateKServeReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.KServePreflight(ctx, u.ID, gid, in)
}

func (a *API) KServeDiagnostics(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, id, err := mlIDs(r, "sid")
	if err != nil {
		return nil, err
	}
	return a.service.KServeDiagnostics(ctx, u.ID, gid, id)
}

func (a *API) KServeLogs(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, id, err := mlIDs(r, "sid")
	if err != nil {
		return nil, err
	}
	values := r.URL.Query()
	for key, items := range values {
		if len(items) != 1 {
			return nil, apperr.New(apperr.CodeInvalid, "日志参数不允许重复")
		}
		switch key {
		case "instanceId", "revision", "pod", "podUID", "container", "tailLines", "limitBytes", "sinceSeconds", "previous":
		default:
			return nil, apperr.New(apperr.CodeInvalid, "不支持的日志参数")
		}
	}
	q := dto.KServeLogQuery{InstanceID: values.Get("instanceId"), Pod: values.Get("pod"), PodUID: values.Get("podUID"), Container: values.Get("container")}
	q.Revision, err = strconv.ParseUint(values.Get("revision"), 10, 64)
	if err != nil {
		return nil, apperr.New(apperr.CodeInvalid, "revision 无效")
	}
	for key, target := range map[string]*int64{"tailLines": &q.TailLines, "limitBytes": &q.LimitBytes, "sinceSeconds": &q.SinceSeconds} {
		if values.Has(key) {
			*target, err = strconv.ParseInt(values.Get(key), 10, 64)
			if err != nil || *target <= 0 {
				return nil, apperr.New(apperr.CodeInvalid, "日志范围必须为正整数")
			}
		}
	}
	if values.Has("previous") {
		q.Previous, err = strconv.ParseBool(values.Get("previous"))
		if err != nil {
			return nil, apperr.New(apperr.CodeInvalid, "previous 必须为布尔值")
		}
	}
	return a.service.KServeLogs(ctx, u.ID, gid, id, q)
}
