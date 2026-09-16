package agent

import "strings"

// ValidMCPHTTPBearerToken accepts RFC 6750 b64token bytes without normalization.
// Credential resource storage has a separate, opaque-string contract.
func ValidMCPHTTPBearerToken(token string) bool {
	value := strings.TrimRight(token, "=")
	if value == "" {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", rune(c))) {
			return false
		}
	}
	return true
}
