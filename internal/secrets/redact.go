package secrets

import (
	"os"
	"regexp"
	"strings"
)

var (
	openAIKeyPattern     = regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_*.-]{6,}[A-Za-z0-9_*]`)
	authorizationPattern = regexp.MustCompile(`(?im)^(\s*Authorization\s*:\s*)[^\r\n]+`)
	bearerPattern        = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	secretKVPattern      = regexp.MustCompile(`(?i)(["']?\b(?:api[_-]?key|token|password|secret|authorization)\b["']?\s*[:=]\s*)(["']?)([^"'\s,}]+)(["']?)`)
)

// Redact removes common secret values from strings before they are logged or
// persisted into artifacts.
func Redact(s string) string {
	for _, env := range os.Environ() {
		key, value, ok := strings.Cut(env, "=")
		if !ok || len(value) < 8 {
			continue
		}
		lower := strings.ToLower(key)
		if strings.Contains(lower, "key") ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") {
			s = strings.ReplaceAll(s, value, "[redacted]")
		}
	}
	s = authorizationPattern.ReplaceAllString(s, "${1}[redacted]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [redacted]")
	s = openAIKeyPattern.ReplaceAllString(s, "[redacted-openai-key]")
	s = secretKVPattern.ReplaceAllString(s, "${1}${2}[redacted]${4}")
	return s
}
