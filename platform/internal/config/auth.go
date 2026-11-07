package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

func (c *Config) validateAuth() error {
	if !c.Auth.Enabled {
		return nil
	}
	a := c.Auth
	issuer, err := url.Parse(a.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || len(a.Issuer) > 255 {
		return fmt.Errorf("auth.issuer 必须为有效的 HTTPS issuer URL")
	}
	origin, err := url.Parse(a.PublicOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || strings.Contains(a.PublicOrigin, "#") {
		return fmt.Errorf("auth.public_origin 必须为不带路径的 HTTPS 来源，如 https://loom.example.com")
	}
	if a.NotebookOrigin != "" {
		notebook, err := url.Parse(a.NotebookOrigin)
		if err != nil || notebook.Scheme != "https" || notebook.Hostname() == "" || notebook.User != nil || notebook.Path != "" || notebook.RawQuery != "" || notebook.ForceQuery || strings.Contains(a.NotebookOrigin, "#") || strings.EqualFold(notebook.Hostname(), origin.Hostname()) {
			return fmt.Errorf("auth.notebook_origin 必须是与平台不同主机的独立 HTTPS 来源（仅不同端口不能隔离 host-only Cookie）")
		}
	}
	key, err := base64.StdEncoding.DecodeString(a.SigningKey)
	if err != nil || len(key) < 32 {
		return fmt.Errorf("auth.signing_key 必须是至少 32 字节随机密钥的标准 Base64")
	}
	if strings.TrimSpace(a.Audience) == "" || len(a.Audience) > 128 {
		return fmt.Errorf("auth.audience 无效")
	}
	if a.AccessTTL.Std() < time.Minute || a.AccessTTL.Std() > 15*time.Minute || a.SessionTTL.Std() < time.Hour || a.SessionTTL.Std() > 15*24*time.Hour {
		return fmt.Errorf("认证期限无效：access 1–15 分钟，session 1 小时–15 天")
	}
	if len(c.Identity.TrustedProxyCIDRs) == 0 || c.Identity.SubjectHeader == c.Identity.UserIDHeader {
		return fmt.Errorf("本地认证也必须配置 /verify 可信网关及独立 subject_header")
	}
	if !validator.IsIdentityHeaderName(c.Identity.UserIDHeader) || !validator.IsIdentityHeaderName(c.Identity.SubjectHeader) {
		return fmt.Errorf("认证身份头必须为独立的小写 HTTP 头，不能占用 Cookie、Authorization 等协议头")
	}
	if c.Identity.Issuer != "" && c.Identity.Issuer != a.Issuer {
		return fmt.Errorf("本地模式下 identity.issuer 需为空或与 auth.issuer 一致，不自动合并外部身份")
	}
	return nil
}
