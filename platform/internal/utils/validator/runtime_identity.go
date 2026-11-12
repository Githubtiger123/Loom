package validator

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

func IsCanonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

// Kubernetes resourceVersion is opaque, not a timestamp or sortable counter.
// This bounds storage/transport only; callers may compare it for equality.
func IsKubernetesResourceVersion(value string) bool {
	return value != "" && len(value) <= 512 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
