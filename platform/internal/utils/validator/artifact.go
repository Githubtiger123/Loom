package validator

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func IsMetadataID(value string) bool {
	n, err := strconv.ParseUint(value, 10, 63)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
}

// IsArtifactKey permits normal S3 filenames but rejects ambiguous paths and escaping.
func IsArtifactKey(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\%?#*") {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
