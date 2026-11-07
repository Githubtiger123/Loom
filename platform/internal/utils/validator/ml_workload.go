package validator

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// IsMLReference accepts an opaque managed reference, never a URL or storage credential.
// The execution adapter still has to resolve it within the group and actor's grants.
func IsMLReference(value string) bool { return IsCatalogID(value) }

func IsSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func IsMLPublicValue(kind, value string) bool {
	if len(value) > 4096 || !MaxTextLength(value, 4096) || strings.ContainsRune(value, '\x00') {
		return false
	}
	switch kind {
	case "STRING":
		return true
	case "INTEGER":
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	case "NUMBER", "DOUBLE":
		n, err := strconv.ParseFloat(value, 64)
		return err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case "BOOLEAN":
		return value == "true" || value == "false"
	case "LIST":
		var values []json.RawMessage
		return json.Unmarshal([]byte(value), &values) == nil && values != nil
	case "STRUCT":
		var values map[string]json.RawMessage
		return json.Unmarshal([]byte(value), &values) == nil && values != nil
	}
	return false
}

// Credential-like parameters cannot accidentally use the public literal channel.
func IsMLCredentialName(value string) bool {
	n := strings.ToLower(strings.ReplaceAll(value, "-", "_"))
	return strings.Contains(n, "password") || strings.Contains(n, "secret") ||
		strings.Contains(n, "token") || strings.Contains(n, "credential") ||
		strings.Contains(n, "private_key") || strings.Contains(n, "api_key") ||
		strings.Contains(n, "access_key")
}
