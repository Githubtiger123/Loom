package handler

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/authn"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

func (a *API) trustedGateway(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range a.trusted {
		if prefix.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

// Verify 接收 Envoy HTTP ext_authz 的 pathPrefix=/verify：原始路径会跟在前缀后面。
// 不从客户端可伪造的 X-Forwarded-Uri/X-User 读取目标或身份。不得公开路由此入口。
func (a *API) Verify(w http.ResponseWriter, r *http.Request) {
	if !a.authAvailable(w, r) {
		return
	}
	if !a.trustedGateway(r) {
		a.error(w, apperr.New(apperr.CodeForbidden, "仅允许可信网关调用认证校验"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/verify")
	// 拒绝代理与应用可能产生不同解释的路径，不在认证后允许路径跨空间归一化。
	escaped := strings.ToLower(r.URL.EscapedPath())
	if strings.ContainsAny(path, "\\\x00\r\n") || strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.Contains(escaped, "%25") {
		a.error(w, apperr.New(apperr.CodeForbidden, "请求路径编码不明确"))
		return
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			a.error(w, apperr.New(apperr.CodeForbidden, "请求路径无效"))
			return
		}
	}
	var p *authn.Principal
	var err error
	notebook := strings.HasPrefix(path, "/notebook/")
	switch {
	case notebook:
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			a.error(w, apperr.New(apperr.CodeForbidden, "Notebook 不支持此请求方法"))
			return
		}
		raw, e := oneCookie(r, authn.BrowserCookie)
		if e != nil {
			a.error(w, e)
			return
		}
		p, err = a.auth.Browser(r.Context(), raw)
		if err == nil {
			err = a.auth.RequirePasswordChanged(r.Context(), p.User)
		}
		if err == nil {
			parts := strings.SplitN(strings.TrimPrefix(path, "/notebook/"), "/", 3)
			if len(parts) < 2 || !validator.IsDNS1123Label(parts[0]) || !validator.IsDNS1123Label(parts[1]) {
				err = apperr.New(apperr.CodeForbidden, "Notebook 路径无效")
			} else {
				err = a.service.AuthorizeNotebookPath(r.Context(), p.User.ID, parts[0], parts[1])
			}
		}
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		websocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		if err == nil {
			err = a.checkNotebookOrigin(r, unsafe || websocket)
		}
	case strings.HasPrefix(path, "/api/v1/") && !strings.HasPrefix(path, "/api/v1/auth/"):
		p, err = a.bearer(r)
		if err == nil {
			err = a.checkBrowserOrigin(r, false)
		}
	default:
		err = apperr.New(apperr.CodeForbidden, "此路径尚未接入认证校验")
	}
	if err != nil {
		a.error(w, err)
		return
	}
	// 即使 JWT 验证成功也不使用客户端身份头；每次从数据库稳定身份重新生成。
	if p.User.Subject == nil || !validator.IsIdentityHeaderValue(*p.User.Subject) ||
		!validator.IsIdentityHeaderValue(a.identity.UserIDPrefix+p.User.Username) {
		a.error(w, apperr.New(apperr.CodeForbidden, "用户身份不能安全传递"))
		return
	}
	w.Header().Set(a.identity.UserIDHeader, a.identity.UserIDPrefix+p.User.Username)
	w.Header().Set(a.identity.SubjectHeader, *p.User.Subject)
	if notebook {
		// 网关必须将这些结果配置为覆盖上游头，不能追加。
		// 仅剥离 Loom 凭据，Jupyter 自己的 _xsrf 等 Cookie 保留。
		cookies := []string{}
		for _, c := range r.Cookies() {
			if c.Name != authn.BrowserCookie && c.Name != authn.RefreshCookie && c.Name != authn.LegacyBrowserCookie {
				cookies = append(cookies, c.String())
			}
		}
		clean := strings.Join(cookies, "; ")
		if clean == "" {
			clean = "loom-auth-stripped=1"
		}
		w.Header().Set("Cookie", clean)
		// 空头可能被代理丢弃而保留原始凭据，使用无凭据的固定标记确保发生覆盖。
		w.Header().Set("Authorization", "Loom-Verified")
	}
	w.WriteHeader(http.StatusOK)
}

// 独立 Notebook 主机只接受自己的页面来源；平台的 Refresh 不会发到该主机。
// 原始 Host 必须由网关按保留原请求 Host 的契约传入，不信任客户端可写的转发头。
func (a *API) checkNotebookOrigin(r *http.Request, required bool) error {
	origin := a.auth.Config.NotebookOrigin
	if origin == "" {
		return a.checkBrowserOrigin(r, required)
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(r.Host, u.Host) {
		return apperr.New(apperr.CodeForbidden, "Notebook 请求主机与配置不匹配")
	}
	// 允许平台跳转到独立主机的顶层只读导航，不允许跨站子资源或写入。
	topNavigation := !required && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document"
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" && !topNavigation {
		return apperr.New(apperr.CodeForbidden, "拒绝跨站 Notebook 请求")
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 && !required {
		return nil
	}
	if len(origins) != 1 || origins[0] != origin {
		return apperr.New(apperr.CodeForbidden, "Notebook 请求来源不匹配")
	}
	return nil
}
