package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) PipelineVersionNativeCompatibility(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, pid, err := mlIDs(r, "pid")
	if err != nil {
		return nil, err
	}
	vid, err := pathID(r, "vid")
	if err != nil {
		return nil, err
	}
	values := r.URL.Query()["revision"]
	if len(values) != 1 {
		return nil, apperr.New(apperr.CodeInvalid, "必须提供唯一的版本 revision")
	}
	revision, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil || revision == 0 {
		return nil, apperr.New(apperr.CodeInvalid, "revision 必须为正整数")
	}
	return a.service.PipelineVersionNativeCompatibility(ctx, u.ID, gid, pid, vid, revision)
}
