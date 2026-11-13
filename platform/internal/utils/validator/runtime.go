package validator

import "strings"

// IsWorkloadBearerToken checks bounds only. Cryptographic validation and the
// audience/Pod binding must come from Kubernetes TokenReview, never JWT decode.
func IsWorkloadBearerToken(token string) bool {
	if len(token) == 0 || len(token) > 16384 || strings.TrimSpace(token) != token {
		return false
	}
	for _, ch := range token {
		if ch <= 32 || ch > 126 {
			return false
		}
	}
	return true
}
