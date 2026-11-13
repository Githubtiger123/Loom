package validator

import (
	"net/url"
	"path"
	"strings"
)

func IsObjectURIWithin(root, value string) bool {
	base, err := url.Parse(root)
	if err != nil || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" {
		return false
	}
	object, err := url.Parse(value)
	if err != nil {
		return false
	}
	if base.Host == "" || object.Host != base.Host || object.Scheme != base.Scheme || object.User != nil || object.RawQuery != "" || object.ForceQuery || object.Fragment != "" || object.Opaque != "" {
		return false
	}
	if base.Scheme != "s3" && base.Scheme != "gs" && base.Scheme != "minio" {
		return false
	}
	if strings.ContainsAny(base.Path+object.Path, "\\\x00") || strings.Contains(base.Path+object.Path, "//") ||
		(base.Path != "" && base.Path != "/" && path.Clean(base.Path) != strings.TrimSuffix(base.Path, "/")) ||
		path.Clean(object.Path) != strings.TrimSuffix(object.Path, "/") {
		return false
	}
	prefix := strings.TrimRight(base.Path, "/") + "/"
	return strings.HasPrefix(object.Path, prefix) && len(object.Path) > len(prefix)
}
