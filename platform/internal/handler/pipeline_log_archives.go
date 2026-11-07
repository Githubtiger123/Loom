package handler

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

func (a *API) PipelineLogArchives(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineLogArchive(ctx, u, r, false)
}

func (a *API) PipelineLogArchiveContent(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineLogArchive(ctx, u, r, true)
}

func (a *API) pipelineLogArchive(ctx context.Context, u *model.User, r *http.Request, content bool) (any, error) {
	gid, rid, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, apperr.New(apperr.CodeInvalid, "归档查询参数编码无效")
	}
	instance, etag := "", ""
	offset, limit := int64(0), int64(256<<10)
	for key, items := range query {
		if len(items) != 1 || items[0] == "" {
			return nil, apperr.New(apperr.CodeInvalid, "归档参数不能为空或重复")
		}
		switch key {
		case "instanceId":
			instance = items[0]
		case "etag":
			if !content || !validator.IsArchiveETag(items[0]) {
				return nil, apperr.New(apperr.CodeInvalid, "etag 参数无效")
			}
			etag = items[0]
		case "offset", "limitBytes":
			if !content {
				return nil, apperr.New(apperr.CodeInvalid, "列表不接受内容读取参数")
			}
			value, err := strconv.ParseInt(items[0], 10, 64)
			if err != nil || value < 0 {
				return nil, apperr.New(apperr.CodeInvalid, "归档字节范围无效")
			}
			if key == "offset" {
				offset = value
			} else {
				limit = value
			}
		default:
			return nil, apperr.New(apperr.CodeInvalid, "不支持的归档参数")
		}
	}
	if content && (!validator.IsArchiveSegment(r.PathValue("entryId")) || instance == "" || etag == "") {
		return nil, apperr.New(apperr.CodeInvalid, "内容读取需提供列表返回的 instanceId、entryId 和 etag")
	}
	release, err := a.logStreams.reserve(u.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	var value any
	if content {
		value, err = a.service.PipelineLogArchiveContent(ctx, u.ID, gid, rid, instance, r.PathValue("entryId"), etag, offset, limit)
	} else {
		value, err = a.service.PipelineLogArchives(ctx, u.ID, gid, rid, instance)
	}
	if err != nil {
		return nil, err
	}
	if a.auth != nil {
		p, err := a.bearer(r)
		if err != nil {
			return nil, err
		}
		if p.User.ID != u.ID || p.PasswordChangeRequired {
			return nil, apperr.New(apperr.CodeUnauthorized, "日志会话身份已失效")
		}
	}
	return value, nil
}
