// Package validator 集中可复用的纯格式校验；不访问数据库或 Kubernetes，
// 不决定权限、身份是否可信或资源是否可用。业务规则由调用方组合。
package validator

import (
	"net/mail"
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/resource"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
)

var roleCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9a-z-]+$")

func RequiredText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && MaxTextLength(value, max)
}
func IsDNS1123Label(value string) bool {
	return len(kvalidation.IsDNS1123Label(value)) == 0
}
func IsRoleCode(value string) bool   { return roleCode.MatchString(value) }
func IsLabelKey(value string) bool   { return len(kvalidation.IsQualifiedName(value)) == 0 }
func IsLabelValue(value string) bool { return len(kvalidation.IsValidLabelValue(value)) == 0 }
func IsNonNegativeQuantity(value string) bool {
	q, err := resource.ParseQuantity(value)
	return err == nil && q.Sign() >= 0
}
func IsPositiveQuantity(value string) bool {
	q, err := resource.ParseQuantity(value)
	return err == nil && q.Sign() > 0
}
func IsLoginEmail(value string) bool {
	addr, err := mail.ParseAddress(value)
	return err == nil && addr.Address == value && strings.Contains(value, "@") && len(value) <= 255
}
func IsIdempotencyKey(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func IsTrustedProxyCIDR(value string) bool {
	prefix, err := netip.ParsePrefix(value)
	return err == nil && prefix.Bits() > 0
}

// 身份头使用小写 HTTP token；这里不判断请求是否来自可信代理。
func IsLowerHeaderName(value string) bool { return headerName.MatchString(value) }

// IsIdentityHeaderName 禁止身份头与传输、认证和代理控制头冲突。
func IsIdentityHeaderName(value string) bool {
	if !IsLowerHeaderName(value) || strings.HasPrefix(value, "x-envoy-") || strings.HasPrefix(value, "x-forwarded-") {
		return false
	}
	switch value {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "host", "origin", "connection",
		"upgrade", "content-length", "content-type", "transfer-encoding", "forwarded":
		return false
	}
	return true
}
func MaxTextLength(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
}
func IsIdentityHeaderValue(value string) bool {
	return value != "" && len(value) <= 255 && !strings.ContainsAny(value, "\r\n\x00,")
}

func IsEnvVarName(value string) bool { return len(kvalidation.IsEnvVarName(value)) == 0 }
func IsSecretName(value string) bool { return len(kvalidation.IsDNS1123Subdomain(value)) == 0 }
func IsSecretKey(value string) bool  { return len(kvalidation.IsConfigMapKey(value)) == 0 }

// IsCatalogID 只检查外部目录标识的格式，不判断镜像是否存在或获准使用。
func IsCatalogID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func IsUserLabelKey(value string) bool {
	if !IsLabelKey(value) {
		return false
	}
	for _, prefix := range []string{"platform.example.com/", "loom.", "kubeflow.org/", "notebooks.kubeflow.org/", "kubernetes.io/", "k8s.io/"} {
		if strings.HasPrefix(value, prefix) {
			return false
		}
	}
	return value != "statefulset" && value != "notebook-name"
}
