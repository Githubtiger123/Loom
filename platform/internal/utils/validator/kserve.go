package validator

import (
	"net/url"
	"regexp"
	"strings"
)

var kserveModelVersion = regexp.MustCompile(`^(?:[0-9]+)(?:\.[0-9]+){0,2}$`)

func IsKServeStorageURI(value string) bool {
	if len(value) < 6 || len(value) > 2048 || strings.Contains(value, "..") || strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || strings.TrimSpace(u.Path) == "" {
		return false
	}
	switch u.Scheme {
	case "s3", "gs", "pvc", "oci":
		return true
	default:
		return false
	}
}

func IsKServeModelFormat(value string) bool {
	switch value {
	case "sklearn", "xgboost", "tensorflow", "pytorch", "triton", "onnx", "huggingface", "pmml", "lightgbm", "paddle":
		return true
	default:
		return false
	}
}

func IsKServeModelVersion(value string) bool {
	return value == "" || (len(value) <= 32 && kserveModelVersion.MatchString(value))
}

func IsKServeHealthPath(value string) bool {
	return len(value) > 0 && len(value) <= 256 && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") &&
		!strings.ContainsAny(value, "?#\r\n\x00") && !strings.Contains(value, "..")
}

func IsKServeCommand(values []string) bool {
	if len(values) > 32 {
		return false
	}
	total := 0
	for _, value := range values {
		total += len(value)
		if value == "" || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
			return false
		}
	}
	return total <= 8192
}
