package validator

import (
	"net/url"
	"strings"
)

func IsModelRegistryName(value string) bool {
	if len(value) < 1 || len(value) > 63 {
		return false
	}
	isAlphaNumeric := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	if !isAlphaNumeric(value[0]) || !isAlphaNumeric(value[len(value)-1]) {
		return false
	}
	for i := 1; i < len(value)-1; i++ {
		c := value[i]
		if !isAlphaNumeric(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func IsModelArtifactURI(value, storageType string) bool {
	if len(value) < 6 || len(value) > 2048 || strings.Contains(value, "..") || strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || strings.TrimSpace(u.Path) == "" {
		return false
	}
	want := map[string]string{"S3": "s3", "GS": "gs", "PVC": "pvc", "OCI": "oci"}[storageType]
	return want != "" && u.Scheme == want
}

func IsModelArtifactFormat(value string) bool {
	return IsModelRegistryName(value)
}

func IsModelArtifactFormatVersion(value string) bool {
	return value == "" || (len(value) <= 64 && MaxTextLength(value, 64) && !strings.ContainsAny(value, "\r\n\x00"))
}
