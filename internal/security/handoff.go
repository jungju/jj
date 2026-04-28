package security

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	handoffPayloadLinePattern = regexp.MustCompile(`(?i)^\s*([A-Za-z0-9_. -]*(?:raw[_ -]?)?(?:command|cmd|shell|argv|environment|env|stdout|stderr|validation[_ -]?(?:output|payload|stdout|stderr|log)|manifest|diff[_ -]?(?:body|patch|raw)|denied[_ -]?(?:path|payload))[A-Za-z0-9_. -]*\s*[:=]\s*).+$`)
	handoffEnvLinePattern     = regexp.MustCompile(`^\s*(?:export\s+)?([A-Z_][A-Z0-9_]{1,})\s*=\s*(.+)$`)
	handoffShellLinePattern   = regexp.MustCompile(`^\s*(?:\$|>)\s+\S+(?:\s+.*)?$`)
	handoffDiffStartPattern   = regexp.MustCompile(`^\s*(?:diff --git\b|@@\s|---\s+a/|\+\+\+\s+b/|index\s+[0-9a-f]+\.\.[0-9a-f]+)`)
)

// SanitizeHandoffString removes payload-shaped material that should not cross
// planner/Codex handoff boundaries while preserving disclosure-safe context.
func SanitizeHandoffString(s string, roots ...CommandPathRoot) string {
	out, _ := SanitizeHandoffStringWithReport(s, roots...)
	return out
}

func SanitizeHandoffStringWithReport(s string, roots ...CommandPathRoot) (string, RedactionReport) {
	if s == "" {
		return "", RedactionReport{}
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	sanitized, report := SanitizeDisplayStringWithReport(s, roots...)
	sanitized, lineReport := sanitizeHandoffLinesWithReport(sanitized)
	report.Merge(lineReport)
	return sanitized, report
}

func SanitizeHandoffBytes(data []byte, roots ...CommandPathRoot) []byte {
	out, _ := SanitizeHandoffBytesWithReport(data, roots...)
	return out
}

func SanitizeHandoffBytesWithReport(data []byte, roots ...CommandPathRoot) ([]byte, RedactionReport) {
	out, report := SanitizeHandoffStringWithReport(string(data), roots...)
	return []byte(out), report
}

func SanitizeHandoffContent(path string, data []byte, roots ...CommandPathRoot) []byte {
	out, _ := SanitizeHandoffContentWithReport(path, data, roots...)
	return out
}

func SanitizeHandoffContentWithReport(path string, data []byte, roots ...CommandPathRoot) ([]byte, RedactionReport) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed) {
		return sanitizeHandoffJSONBytesWithReport(trimmed, true, roots...)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return sanitizeHandoffJSONBytesWithReport(data, true, roots...)
	case ".jsonl":
		return sanitizeHandoffJSONLinesBytesWithReport(data, roots...)
	default:
		return SanitizeHandoffBytesWithReport(data, roots...)
	}
}

func SanitizeHandoffJSONText(s string, roots ...CommandPathRoot) string {
	out, _ := SanitizeHandoffJSONTextWithReport(s, roots...)
	return out
}

func SanitizeHandoffJSONTextWithReport(s string, roots ...CommandPathRoot) (string, RedactionReport) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", RedactionReport{}
	}
	if json.Valid([]byte(trimmed)) {
		out, report := sanitizeHandoffJSONBytesWithReport([]byte(trimmed), true, roots...)
		return string(out), report
	}
	return SanitizeHandoffStringWithReport(s, roots...)
}

func SanitizeHandoffJSONValue(value any, roots ...CommandPathRoot) any {
	out, _ := SanitizeHandoffJSONValueWithReport(value, roots...)
	return out
}

func SanitizeHandoffJSONValueWithReport(value any, roots ...CommandPathRoot) (any, RedactionReport) {
	redacted, report := RedactJSONValueWithReport(value)
	sanitized, handoffReport := sanitizeHandoffJSONValue(redacted, "", roots...)
	report.Merge(handoffReport)
	return sanitized, report
}

func sanitizeHandoffJSONBytesWithReport(data []byte, pretty bool, roots ...CommandPathRoot) ([]byte, RedactionReport) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return SanitizeHandoffBytesWithReport(data, roots...)
	}
	sanitized, report := SanitizeHandoffJSONValueWithReport(value, roots...)
	var (
		out []byte
		err error
	)
	if pretty {
		out, err = json.MarshalIndent(sanitized, "", "  ")
	} else {
		out, err = json.Marshal(sanitized)
	}
	if err != nil {
		return SanitizeHandoffBytesWithReport(data, roots...)
	}
	return append(out, '\n'), report
}

func sanitizeHandoffJSONLinesBytesWithReport(data []byte, roots ...CommandPathRoot) ([]byte, RedactionReport) {
	if len(data) == 0 {
		return data, RedactionReport{}
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	out := make([]byte, 0, len(data))
	report := RedactionReport{}
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		hasNewline := bytes.HasSuffix(line, []byte("\n"))
		body := bytes.TrimSuffix(line, []byte("\n"))
		if len(bytes.TrimSpace(body)) == 0 {
			if hasNewline {
				out = append(out, '\n')
			}
			continue
		}
		redacted, lineReport := sanitizeHandoffJSONBytesWithReport(body, false, roots...)
		redacted = bytes.TrimSuffix(redacted, []byte("\n"))
		report.Merge(lineReport)
		out = append(out, redacted...)
		if hasNewline {
			out = append(out, '\n')
		}
	}
	return out, report
}

func sanitizeHandoffJSONValue(value any, key string, roots ...CommandPathRoot) (any, RedactionReport) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		report := RedactionReport{}
		for childKey, childValue := range v {
			safeKey, keyReport := sanitizeHandoffJSONKey(childKey, roots...)
			safeValue, valueReport := sanitizeHandoffJSONValue(childValue, childKey, roots...)
			report.Merge(keyReport)
			report.Merge(valueReport)
			out[safeKey] = safeValue
		}
		return out, report
	case []any:
		out := make([]any, len(v))
		report := RedactionReport{}
		for i, item := range v {
			safeValue, valueReport := sanitizeHandoffJSONValue(item, key, roots...)
			report.Merge(valueReport)
			out[i] = safeValue
		}
		return out, report
	case string:
		if handoffOpaquePayloadKey(key) {
			return RedactionMarker, RedactionReport{Count: 1, Kinds: map[string]int{"handoff_payload": 1}}
		}
		if nested, nestedReport, ok := sanitizeNestedHandoffJSONString(v, roots...); ok {
			return nested, nestedReport
		}
		return SanitizeHandoffStringWithReport(v, roots...)
	default:
		return value, RedactionReport{}
	}
}

func sanitizeNestedHandoffJSONString(value string, roots ...CommandPathRoot) (string, RedactionReport, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') || !json.Valid([]byte(trimmed)) {
		return "", RedactionReport{}, false
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return "", RedactionReport{}, false
	}
	sanitized, report := SanitizeHandoffJSONValueWithReport(decoded, roots...)
	out, err := json.Marshal(sanitized)
	if err != nil {
		return "", RedactionReport{}, false
	}
	return string(out), report, true
}

func sanitizeHandoffJSONKey(key string, roots ...CommandPathRoot) (string, RedactionReport) {
	safe, report := SanitizeHandoffStringWithReport(key, roots...)
	safe = strings.TrimSpace(safe)
	if safe == "" {
		report.Add("handoff_key", 1)
		return "field", report
	}
	if strings.Contains(safe, RedactionMarker) || strings.Contains(safe, "[path]") || strings.ContainsAny(safe, `/\`) || len(safe) > 128 {
		report.Add("handoff_key", 1)
		return "field", report
	}
	return safe, report
}

func handoffOpaquePayloadKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" || isRedactionMetadataKey(key) {
		return false
	}
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	if strings.Contains(normalized, "path") || strings.Contains(normalized, "label") || strings.Contains(normalized, "status") ||
		strings.Contains(normalized, "count") || strings.Contains(normalized, "category") || strings.Contains(normalized, "provider") ||
		strings.Contains(normalized, "mode") || strings.Contains(normalized, "policy") || strings.Contains(normalized, "artifact") ||
		strings.Contains(normalized, "duration") || strings.Contains(normalized, "exit_code") || normalized == "validation_command" {
		return false
	}
	switch normalized {
	case "prompt", "stdin", "stdout", "stderr", "env", "environment", "raw", "raw_response", "raw_output", "raw_error", "command", "cmd", "shell", "argv":
		return true
	}
	return strings.Contains(normalized, "_prompt") ||
		strings.Contains(normalized, "raw_") ||
		strings.Contains(normalized, "_raw") ||
		strings.Contains(normalized, "payload") ||
		strings.Contains(normalized, "body") ||
		strings.Contains(normalized, "diff_patch") ||
		strings.Contains(normalized, "diff_raw") ||
		strings.Contains(normalized, "diff_content") ||
		strings.Contains(normalized, "manifest_content") ||
		strings.Contains(normalized, "manifest_raw") ||
		strings.Contains(normalized, "validation_output")
}

func sanitizeHandoffLinesWithReport(s string) (string, RedactionReport) {
	report := RedactionReport{}
	lines := strings.SplitAfter(s, "\n")
	if len(lines) == 0 {
		return s, report
	}
	var b strings.Builder
	inDiff := false
	for _, raw := range lines {
		if raw == "" {
			continue
		}
		hasNewline := strings.HasSuffix(raw, "\n")
		line := strings.TrimSuffix(raw, "\n")
		trimmed := strings.TrimSpace(line)
		replacement := ""
		switch {
		case trimmed == "":
			inDiff = false
		case handoffDiffStartPattern.MatchString(line):
			inDiff = true
			replacement = RedactionMarker
		case inDiff:
			replacement = RedactionMarker
		case handoffPayloadLinePattern.MatchString(line):
			replacement = handoffPayloadLinePattern.ReplaceAllString(line, "${1}"+RedactionMarker)
		case handoffEnvLineShouldBeOmitted(line):
			replacement = RedactionMarker
		case handoffShellLinePattern.MatchString(line):
			replacement = RedactionMarker
		}
		if replacement != "" {
			report.Add("handoff_payload", 1)
			b.WriteString(replacement)
		} else {
			b.WriteString(line)
		}
		if hasNewline {
			b.WriteByte('\n')
		}
	}
	return b.String(), report
}

func handoffEnvLineShouldBeOmitted(line string) bool {
	matches := handoffEnvLinePattern.FindStringSubmatch(line)
	if len(matches) != 3 {
		return false
	}
	key := strings.TrimSpace(matches[1])
	value := strings.TrimSpace(matches[2])
	if isEnvNameReferenceKey(key) || isSecretPresenceKey(key) || lowInformationLiteral(value) || isJSONPrimitive(value) {
		return false
	}
	return true
}
