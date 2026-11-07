package validator

import (
	"regexp"
	"strings"
)

var archiveSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var archiveETag = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func IsArchiveSegment(value string) bool { return archiveSegment.MatchString(value) }
func IsArchiveETag(value string) bool    { return archiveETag.MatchString(value) }

// IsArchivePath accepts relative object keys only, without escaping or traversal.
func IsArchivePath(value string) bool {
	if len(value) == 0 || len(value) > 1024 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if !IsArchiveSegment(part) {
			return false
		}
	}
	return true
}
