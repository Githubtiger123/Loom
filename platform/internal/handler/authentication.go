package handler

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/authn"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (a *API) authAvailable(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if a.auth == nil {
		a.error(w, apperr.New(apperr.CodeUnavailable, "本地认证未启用"))
		return false
	}
	return true
}

func (a *API) checkBrowserOrigin(r *http.Request, required bool) error {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return apperr.New(apperr.CodeForbidden, "拒绝跨站请求")
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 && !required {
		return nil
	}
	if len(origins) != 1 || origins[0] != a.auth.Config.PublicOrigin {
		return apperr.New(apperr.CodeForbidden, "请求来源不匹配")
	}
	return nil
}

func (a *API) bearer(r *http.Request) (*authn.Principal, error) {
	raw, err := bearerToken(r)
	if err != nil {
		return nil, err
	}
	return a.auth.Access(r.Context(), raw)
}

func bearerToken(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", apperr.New(apperr.CodeUnauthorized, "需要有效的 Bearer Token")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", apperr.New(apperr.CodeUnauthorized, "需要有效的 Bearer Token")
	}
	return parts[1], nil
}

// 拒绝同名重复 Cookie，避免不同代理/组件选择不同值。
func oneCookie(r *http.Request, name string) (string, error) {
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			count++
			value = cookie.Value
		}
	}
	if count != 1 || value == "" {
		return "", apperr.New(apperr.CodeUnauthorized, "缺少有效会话 Cookie")
	}
	return value, nil
}

func setAuthCookies(w http.ResponseWriter, tokens *authn.Tokens) {
	for _, c := range []struct {
		name, value, path string
		expires           time.Time
	}{
		{authn.BrowserCookie, tokens.AccessToken, "/", tokens.AccessExpires},
		{authn.RefreshCookie, tokens.RefreshToken, "/api/v1/auth", tokens.SessionExpires},
	} {
		http.SetCookie(w, &http.Cookie{Name: c.name, Value: c.value, Path: c.path, Expires: c.expires,
			MaxAge: max(1, int(time.Until(c.expires).Seconds())), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	}
	// 升级后不再接受旧的独立浏览器凭据。
	http.SetCookie(w, &http.Cookie{Name: authn.LegacyBrowserCookie, Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func clearAuthCookies(w http.ResponseWriter) {
	for name, path := range map[string]string{authn.BrowserCookie: "/", authn.LegacyBrowserCookie: "/", authn.RefreshCookie: "/api/v1/auth"} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: path, Value: "", MaxAge: -1,
			Expires: time.Unix(1, 0), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	}
}

func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	if !a.authAvailable(w, r) {
		return
	}
	if err := a.checkBrowserOrigin(r, true); err != nil {
		a.error(w, err)
		return
	}
	in, err := decode[dto.LoginReq](r)
	if err != nil {
		a.error(w, err)
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		a.error(w, apperr.New(apperr.CodeInvalid, "请求来源无效"))
		return
	}
	tokens, err := a.auth.Login(r.Context(), in, peer)
	if err != nil {
		a.error(w, err)
		return
	}
	setAuthCookies(w, tokens)
	writeJSON(w, http.StatusOK, map[string]any{"data": tokens})
}

func (a *API) Refresh(w http.ResponseWriter, r *http.Request) {
	if !a.authAvailable(w, r) {
		return
	}
	if err := a.checkBrowserOrigin(r, true); err != nil {
		a.error(w, err)
		return
	}
	raw, err := oneCookie(r, authn.RefreshCookie)
	if err != nil {
		a.error(w, err)
		return
	}
	tokens, err := a.auth.Refresh(r.Context(), raw)
	if err != nil {
		// 错误响应不能覆盖并发刷新已写回的新 Cookie；只有成功撤销才清理。
		a.error(w, err)
		return
	}
	setAuthCookies(w, tokens)
	writeJSON(w, http.StatusOK, map[string]any{"data": tokens})
}

func (a *API) sessionPrincipal(r *http.Request) (*authn.Principal, error) {
	if len(r.Header.Values("Authorization")) > 0 {
		return a.bearer(r)
	}
	raw, err := oneCookie(r, authn.BrowserCookie)
	if err != nil {
		return nil, err
	}
	// 账号退出允许受限会话，不能调用专为 Notebook 设置强制改密门禁的 Browser。
	return a.auth.Access(r.Context(), raw)
}

func (a *API) Logout(w http.ResponseWriter, r *http.Request)    { a.logout(w, r, false) }
func (a *API) LogoutAll(w http.ResponseWriter, r *http.Request) { a.logout(w, r, true) }

func (a *API) logout(w http.ResponseWriter, r *http.Request, all bool) {
	if !a.authAvailable(w, r) {
		return
	}
	if err := a.checkBrowserOrigin(r, true); err != nil {
		a.error(w, err)
		return
	}
	p, err := a.sessionPrincipal(r)
	if err != nil {
		if all || apperr.From(err).Code != apperr.CodeUnauthorized {
			a.error(w, err)
			return
		}
		// Access 到期或缺失时，只能凭真正有效的 Refresh 撤销当前设备。
		p = nil
	}
	if all {
		err = a.auth.LogoutAll(r.Context(), p.User)
	} else {
		raw := ""
		for _, c := range r.Cookies() {
			if c.Name == authn.RefreshCookie {
				raw, err = oneCookie(r, authn.RefreshCookie)
				if err != nil {
					a.error(w, err)
					return
				}
				break
			}
		}
		err = a.auth.LogoutDevice(r.Context(), p, raw)
	}
	if err != nil {
		a.error(w, err)
		return
	}
	clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"loggedOut": true}})
}

func (a *API) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if !a.authAvailable(w, r) {
		return
	}
	if err := a.checkBrowserOrigin(r, true); err != nil {
		a.error(w, err)
		return
	}
	u := r.Context().Value(actorKey{}).(*model.User)
	in, err := decode[dto.ChangePasswordReq](r)
	if err == nil {
		err = a.auth.ChangePassword(r.Context(), u, in)
	}
	if err != nil {
		a.error(w, err)
		return
	}
	clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"loginRequired": true}})
}

// EstablishNotebookSession 仅由目标 Notebook host 承接，POST 必须持有有效 Access Bearer。
// OPTIONS 是无凭据的 CORS 预检，不写 Cookie。路由不得套入普通业务 Authenticate。
func (a *API) EstablishNotebookSession(w http.ResponseWriter, r *http.Request) {
	if !a.authAvailable(w, r) {
		return
	}
	configured := a.auth.Config.NotebookOrigin
	if configured == "" {
		a.error(w, apperr.New(apperr.CodeUnavailable, "尚未配置独立 Notebook 来源"))
		return
	}
	notebook, err := url.Parse(configured)
	if err != nil || notebook.Scheme != "https" || notebook.Host == "" || notebook.User != nil ||
		notebook.Path != "" || notebook.RawQuery != "" || notebook.Fragment != "" || !strings.EqualFold(r.Host, notebook.Host) {
		a.error(w, apperr.New(apperr.CodeForbidden, "请求必须到达配置的 Notebook 主机"))
		return
	}
	// 此入口有意接受 PublicOrigin -> NotebookOrigin 的跨站请求，仍严格校验唯一 Origin。
	// 不复用 checkBrowserOrigin 的 Sec-Fetch-Site 拒绝规则，否则跨站初始化将不可达。
	origins := r.Header.Values("Origin")
	if len(origins) != 1 || origins[0] != a.auth.Config.PublicOrigin {
		a.error(w, apperr.New(apperr.CodeForbidden, "请求来源不匹配"))
		return
	}
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Origin", a.auth.Config.PublicOrigin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	switch r.Method {
	case http.MethodOptions:
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
		methods := r.Header.Values("Access-Control-Request-Method")
		if len(methods) != 1 || methods[0] != http.MethodPost {
			a.error(w, apperr.New(apperr.CodeForbidden, "只允许 POST 预检"))
			return
		}
		for _, value := range r.Header.Values("Access-Control-Request-Headers") {
			for _, name := range strings.Split(value, ",") {
				if !strings.EqualFold(strings.TrimSpace(name), "Authorization") {
					a.error(w, apperr.New(apperr.CodeForbidden, "只允许 Authorization 请求头"))
					return
				}
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization")
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, err := bearerToken(r)
	if err != nil {
		a.error(w, err)
		return
	}
	p, err := a.auth.Access(r.Context(), raw)
	if err == nil {
		err = a.auth.RequirePasswordChanged(r.Context(), p.User)
	}
	if err != nil {
		a.error(w, err)
		return
	}
	if !p.AccessExpiresAt.After(time.Now()) {
		a.error(w, apperr.New(apperr.CodeUnauthorized, "Access 已过期"))
		return
	}
	// 同一个 JWT，使用验证后的最早到期时间，不延长 Access，不发放 Refresh。
	// SameSite=None 支持不同站点的初始化，后续 Notebook 写请求由网关严格核验其来源。
	http.SetCookie(w, &http.Cookie{Name: authn.BrowserCookie, Value: raw, Path: "/", Expires: p.AccessExpiresAt,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode})
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"established": true}})
}

// Sessions 使用独立认证以保留当前 family 信息；仅接受 Bearer，不接受 Cookie 替代。
func (a *API) Sessions(w http.ResponseWriter, r *http.Request) {
	if !a.authAvailable(w, r) {
		return
	}
	if err := a.checkBrowserOrigin(r, false); err != nil {
		a.error(w, err)
		return
	}
	p, err := a.bearer(r)
	if err != nil {
		a.error(w, err)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			a.error(w, apperr.New(apperr.CodeInvalid, "limit 必须在 1–200 之间"))
			return
		}
	}
	value, err := a.auth.Sessions(r.Context(), p, r.URL.Query().Get("after"), limit)
	if err != nil {
		a.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": value})
}

// RevokeSession 撤销当前用户指定 family。撤销当前设备时只返回 loginRequired，
// 不清理可能属于其他设备/用户的浏览器 Cookie，客户端可丢弃自己的 Bearer。
func (a *API) RevokeSession(w http.ResponseWriter, r *http.Request) {
	if !a.authAvailable(w, r) {
		return
	}
	if err := a.checkBrowserOrigin(r, true); err != nil {
		a.error(w, err)
		return
	}
	p, err := a.bearer(r)
	if err == nil {
		err = a.auth.RevokeSession(r.Context(), p.User, r.PathValue("family"))
	}
	if err != nil {
		a.error(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": dto.RevokeAuthSessionResult{
		Revoked: true, LoginRequired: p.SessionID == r.PathValue("family"),
	}})
}

// AdminRevokeSession 是标准 Endpoint，必须经 Authenticate + Wrap 注册。
func (a *API) AdminRevokeSession(ctx context.Context, actor *model.User, r *http.Request) (any, error) {
	if a.auth == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "本地认证未启用")
	}
	if err := a.checkBrowserOrigin(r, true); err != nil {
		return nil, err
	}
	target, err := pathID(r, "uid")
	if err != nil {
		return nil, err
	}
	if err := a.auth.AdminRevokeSession(ctx, actor, target, r.PathValue("family")); err != nil {
		return nil, err
	}
	return map[string]bool{"revoked": true}, nil
}

func (a *API) ResetPassword(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	if a.auth == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "本地认证未启用")
	}
	id, err := pathID(r, "uid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.PasswordReq](r)
	if err != nil {
		return nil, err
	}
	if err := a.auth.ResetPassword(ctx, u, id, in); err != nil {
		return nil, err
	}
	return map[string]bool{"loginRequired": true}, nil
}

func (a *API) CreateLocalUser(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	if a.auth == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "本地认证未启用")
	}
	in, err := decode[dto.CreateLocalUserReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.CreateLocalUser(ctx, u.ID, requestKey(r), a.auth.Config.Issuer, in)
}
