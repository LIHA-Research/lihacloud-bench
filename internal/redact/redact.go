package redact

import (
	"regexp"
	"strings"
)

var (
	credentialURL = regexp.MustCompile(`(?i)(postgres(?:ql)?|clickhouse|https?)://[^\s/@:]+:[^\s/@]+@`)
	secretQuery   = regexp.MustCompile(`(?i)(password|passwd|token|secret|access[_-]?key)=([^&\s]+)`)
)

func String(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = credentialURL.ReplaceAllString(value, `${1}://[REDACTED]@`)
	return secretQuery.ReplaceAllString(value, `${1}=[REDACTED]`)
}
