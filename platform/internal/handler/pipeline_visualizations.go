package handler

import (
	"context"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
)

// Keeps bounded buffers counted until the response has actually been delivered.
type artifactResponse struct {
	value   any
	file    *service.PipelineArtifactResult
	release func()
}

func (a *API) PipelineRunVisualizations(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineVisualization(ctx, u, r, "")
}
func (a *API) PipelineArtifactPreview(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineVisualization(ctx, u, r, "preview")
}
func (a *API) PipelineArtifactDownload(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineVisualization(ctx, u, r, "download")
}
func (a *API) PipelineArtifactFiles(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineVisualization(ctx, u, r, "files")
}
func (a *API) PipelineArtifactFilePreview(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineVisualization(ctx, u, r, "file-preview")
}
func (a *API) PipelineArtifactFileDownload(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.pipelineVisualization(ctx, u, r, "file-download")
}

func (a *API) pipelineVisualization(ctx context.Context, u *model.User, r *http.Request, mode string) (any, error) {
	gid, rid, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, apperr.New(apperr.CodeInvalid, "可视化参数编码无效")
	}
	instance := ""
	for key, items := range query {
		fileArgument := (mode == "file-preview" || mode == "file-download") && (key == "path" || key == "manifestDigest" || key == "etag")
		if (key != "instanceId" && !fileArgument) || len(items) != 1 || items[0] == "" {
			return nil, apperr.New(apperr.CodeInvalid, "不支持、为空或重复的产物读取参数")
		}
		if key == "instanceId" {
			instance = items[0]
		}
	}
	select {
	case a.artifactReads <- struct{}{}:
	default:
		return nil, apperr.New(apperr.CodeRateLimited, "可视化读取繁忙，请稍后重试")
	}
	transferred := false
	release := func() { <-a.artifactReads }
	defer func() {
		if !transferred {
			release()
		}
	}()
	response := &artifactResponse{release: release}
	if mode == "" {
		response.value, err = a.service.PipelineRunVisualizations(ctx, u.ID, gid, rid, r.PathValue("nodeId"), instance)
	} else if mode == "files" {
		response.value, err = a.service.PipelineArtifactFiles(ctx, u.ID, gid, rid, instance, r.PathValue("artifactId"))
	} else if mode == "file-preview" || mode == "file-download" {
		result, readErr := a.service.PipelineArtifactFileContent(ctx, u.ID, gid, rid, instance, r.PathValue("artifactId"), query.Get("path"), query.Get("manifestDigest"), query.Get("etag"), mode == "file-download")
		err = readErr
		if mode == "file-download" {
			response.file = result
		} else {
			response.value = result
		}
	} else {
		result, readErr := a.service.PipelineArtifactContent(ctx, u.ID, gid, rid, r.PathValue("nodeId"), instance, r.PathValue("artifactId"), mode == "download")
		err = readErr
		if mode == "download" {
			response.file = result
		} else {
			response.value = result
		}
	}
	if err != nil {
		return nil, err
	}
	// A revoked/expired session must not receive a file after slow storage IO.
	if a.auth != nil {
		p, err := a.bearer(r)
		if err != nil {
			return nil, err
		}
		if p.User.ID != u.ID || p.PasswordChangeRequired {
			return nil, apperr.New(apperr.CodeUnauthorized, "可视化会话已失效")
		}
	}
	transferred = true
	return response, nil
}

func (a *API) writeArtifactResponse(w http.ResponseWriter, r *http.Request, response *artifactResponse) {
	defer response.release()
	if response.file == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": response.value})
		return
	}
	obj := response.file.Object
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": obj.Name}))
	w.Header().Set("Content-Length", strconv.Itoa(len(obj.Data)))
	w.Header().Set("ETag", strconv.Quote(obj.ETag))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(obj.Data)
	}
}
