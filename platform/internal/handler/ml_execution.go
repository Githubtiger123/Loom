package handler

import (
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/executor"
)

// These methods are http.HandlerFuncs, deliberately not user Endpoints. Register
// them only on route.NewExecutor's private mux wrapped by executor.Authenticate.
// Every method independently demands the verified-chain + pinned-leaf context.
func (a *API) ClaimMLExecution(w http.ResponseWriter, r *http.Request) {
	peer, ok := executor.PeerSHA256(r)
	if !ok {
		a.error(w, apperr.New(apperr.CodeForbidden, "执行器客户端证书验证失败"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	in, err := decode[dto.ClaimMLExecutionReq](r)
	if err != nil {
		a.error(w, err)
		return
	}
	out, err := a.service.ClaimMLExecution(r.Context(), peer, in)
	if err != nil {
		a.error(w, err)
		return
	}
	if out == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *API) RenewMLExecution(w http.ResponseWriter, r *http.Request) {
	peer, ok := executor.PeerSHA256(r)
	if !ok {
		a.error(w, apperr.New(apperr.CodeForbidden, "执行器客户端证书验证失败"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	in, err := decode[dto.MLExecutionLeaseReq](r)
	if err != nil {
		a.error(w, err)
		return
	}
	out, err := a.service.RenewMLExecution(r.Context(), peer, r.PathValue("oid"), in)
	if err != nil {
		a.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *API) ReportMLExecution(w http.ResponseWriter, r *http.Request) {
	peer, ok := executor.PeerSHA256(r)
	if !ok {
		a.error(w, apperr.New(apperr.CodeForbidden, "执行器客户端证书验证失败"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	in, err := decode[dto.ReportMLExecutionReq](r)
	if err != nil {
		a.error(w, err)
		return
	}
	out, err := a.service.ReportMLExecution(r.Context(), peer, r.PathValue("oid"), in)
	if err != nil {
		a.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (a *API) MLExecutionOperation(w http.ResponseWriter, r *http.Request) {
	peer, ok := executor.PeerSHA256(r)
	if !ok {
		a.error(w, apperr.New(apperr.CodeForbidden, "执行器客户端证书验证失败"))
		return
	}
	out, err := a.service.MLExecutionOperation(r.Context(), peer, r.PathValue("oid"))
	if err != nil {
		a.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// ObserveMLExecution is also exclusively registered on the private mTLS mux.
func (a *API) ObserveMLExecution(w http.ResponseWriter, r *http.Request) {
	peer, ok := executor.PeerSHA256(r)
	if !ok {
		a.error(w, apperr.New(apperr.CodeForbidden, "执行器客户端证书验证失败"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	in, err := decode[dto.ObserveMLExecutionReq](r)
	if err != nil {
		a.error(w, err)
		return
	}
	out, err := a.service.ObserveMLExecution(r.Context(), peer, r.PathValue("oid"), in)
	if err != nil {
		a.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
