package secrets

import "github.com/jungju/jj/internal/security"

func Redact(s string) string {
	return security.Redact(s)
}

func RedactString(s string) string {
	return security.RedactString(s)
}

func RedactBytes(data []byte) []byte {
	return security.RedactBytes(data)
}

func RedactMap(value map[string]any) map[string]any {
	return security.RedactMap(value)
}
