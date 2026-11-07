// Package handler 负责输入解析和响应；业务授权与事务由 service 执行。
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/authn"
	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type actorKey struct{}
type API struct {
	service  *service.Service
	identity config.Identity
	log      *slog.Logger
	trusted  []netip.Prefix
	auth     *authn.Manager
}

// Endpoint 是请求处理函数，不负责注册路由。
type Endpoint func(context.Context, *model.User, *http.Request) (any, error)

func New(s *service.Service, identity config.Identity, logger *slog.Logger, managers ...*authn.Manager) *API {
	a := &API{service: s, identity: identity, log: logger}
	if len(managers) > 0 {
		a.auth = managers[0]
	}
	for _, cidr := range identity.TrustedProxyCIDRs {
		if prefix, err := netip.ParsePrefix(cidr); err == nil && prefix.Bits() > 0 {
			a.trusted = append(a.trusted, prefix)
		}
	}
	return a
}

// Wrap 统一处理响应和拒绝审计；action 由 route 注册时传入。
func (a *API) Wrap(action string, fn Endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := r.Context().Value(actorKey{}).(*model.User)
		value, err := fn(r.Context(), u, r)
		if err != nil {
			appErr := apperr.From(err)
			if appErr.Code == apperr.CodeForbidden {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
				if auditErr := a.service.Denied(ctx, u.ID, action); auditErr != nil {
					a.log.Error("拒绝操作的审计保存失败", "err", auditErr)
				}
				cancel()
			}
			a.error(w, err)
			return
		}
		status := http.StatusOK
		if o, ok := value.(*model.Operation); ok && o.Status != "SUCCEEDED" {
			status = http.StatusAccepted
		}
		writeJSON(w, status, map[string]any{"data": value})
	})
}

// Authenticate 包装全部业务接口，校验可信代理及用户身份。
func (a *API) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.log.Error("请求处理异常", "panic", fmt.Sprint(recovered))
				a.error(w, apperr.New(apperr.CodeInternal, "请求处理失败"))
			}
		}()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if a.auth != nil {
			// 本地模式没有 trusted-header 回退；伪造代理身份头不能绕过 JWT。
			principal, err := a.bearer(r)
			if err != nil {
				a.error(w, err)
				return
			}
			if err := a.checkBrowserOrigin(r, false); err != nil {
				a.error(w, err)
				return
			}
			if !(r.Method == http.MethodPut && r.URL.Path == "/api/v1/auth/password") &&
				!(r.Method == http.MethodGet && r.URL.Path == "/api/v1/me") {
				if err := a.auth.RequirePasswordChanged(r.Context(), principal.User); err != nil {
					a.error(w, err)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, principal.User)))
			return
		}
		if a.identity.Issuer == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "IDENTITY_NOT_CONFIGURED", "message": "尚未配置可信身份接入"}})
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		addr, parseErr := netip.ParseAddr(host)
		trusted := false
		if err == nil && parseErr == nil {
			for _, prefix := range a.trusted {
				if prefix.Contains(addr.Unmap()) {
					trusted = true
					break
				}
			}
		}
		if !trusted {
			a.error(w, apperr.New(apperr.CodeUnauthorized, "请求不是来自可信认证入口"))
			return
		}
		// 不读取 X-Forwarded-For 来推断可信代理，客户端不能据此伪造身份。
		username, ok := singleHeader(r, a.identity.UserIDHeader)
		subject, subjectOK := singleHeader(r, a.identity.SubjectHeader)
		if !ok || !subjectOK || !strings.HasPrefix(username, a.identity.UserIDPrefix) {
			a.error(w, apperr.New(apperr.CodeUnauthorized, "缺少可信登录身份"))
			return
		}
		username = strings.TrimPrefix(username, a.identity.UserIDPrefix)
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				a.error(w, apperr.New(apperr.CodeForbidden, "拒绝跨站操作"))
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(parsed.Host, r.Host) || (parsed.Scheme != "https" && parsed.Scheme != "http") {
					a.error(w, apperr.New(apperr.CodeForbidden, "请求来源不匹配"))
					return
				}
			}
		}
		user, err := a.service.Authenticate(r.Context(), a.identity.Issuer, subject, username)
		if err != nil {
			a.error(w, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, user)))
	})
}

func singleHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 || !validator.IsIdentityHeaderValue(values[0]) {
		return "", false
	}
	return values[0], true
}

func decode[T any](r *http.Request) (T, error) {
	var value T
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return value, apperr.New(apperr.CodeInvalid, "请求必须使用 application/json")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var decoded *T
	if err := decoder.Decode(&decoded); err != nil || decoded == nil {
		return value, apperr.New(apperr.CodeInvalid, "JSON 字段无效或请求体过大")
	}
	value = *decoded
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return value, apperr.New(apperr.CodeInvalid, "只能提交一个 JSON 对象")
	}
	return value, nil
}

func pathID(r *http.Request, key string) (uint64, error) {
	id, err := strconv.ParseUint(r.PathValue(key), 10, 64)
	if err != nil || id == 0 {
		return 0, apperr.New(apperr.CodeInvalid, "无效的对象 ID")
	}
	return id, nil
}
func page(r *http.Request) (uint64, int, error) {
	after := uint64(0)
	limit := 50
	if value := r.URL.Query().Get("after"); value != "" {
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, 0, apperr.New(apperr.CodeInvalid, "after 必须为非负整数")
		}
		after = n
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 200 {
			return 0, 0, apperr.New(apperr.CodeInvalid, "limit 必须在 1–200 之间")
		}
		limit = n
	}
	return after, limit, nil
}
func requestKey(r *http.Request) string {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return ""
	}
	return values[0]
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (a *API) error(w http.ResponseWriter, err error) {
	appErr := apperr.From(err)
	if appErr.HTTPStatus() >= 500 {
		a.log.Error("业务请求失败", "err", err)
	}
	writeJSON(w, appErr.HTTPStatus(), map[string]any{"error": map[string]any{"code": appErr.Code, "message": appErr.Message}})
}
