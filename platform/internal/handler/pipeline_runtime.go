package handler

import (
	"context"
	"encoding/json"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

// PipelineRuntime is mounted only on the dedicated TLS listener. It never uses
// user cookies, trusted identity headers or the public user JWT middleware.
func (a *API) PipelineRuntime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.TLS == nil || r.Header.Get("Cookie") != "" {
		http.Error(w, "runtime TLS authentication required", http.StatusUnauthorized)
		return
	}
	if values := r.Header.Values("Authorization"); len(values) != 1 {
		http.Error(w, "runtime bearer required", http.StatusUnauthorized)
		return
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		http.Error(w, "runtime bearer required", http.StatusUnauthorized)
		return
	}
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return
	}
	select {
	case a.runtimeRequests <- struct{}{}:
		defer func() { <-a.runtimeRequests }()
	default:
		http.Error(w, "runtime request capacity exceeded", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, runtimeapi.MaxMessageBytes)
	defer r.Body.Close()
	var envelope runtimeapi.Envelope
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid runtime envelope", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	reply, err := a.service.PipelineRuntime(ctx, strings.TrimPrefix(authorization, "Bearer "), runtimeapi.Operation(r.PathValue("operation")), envelope)
	if err != nil {
		problem := apperr.From(err)
		http.Error(w, string(problem.Code), problem.HTTPStatus())
		return
	}
	_ = json.NewEncoder(w).Encode(reply)
}
