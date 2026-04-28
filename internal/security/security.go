package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const RedactionMarker = "[jj-omitted]"

const legacyRedactionMarker = "[" + "REDACT" + "ED]"

const (
	maxRelativePathLen = 4096
	maxPathSegmentLen  = 255
)

var (
	openAIKeyPattern      = regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_*.-]{6,}[A-Za-z0-9_*]`)
	githubTokenPattern    = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{6,}\b|\bgithub_pat_[A-Za-z0-9_]{10,}\b`)
	npmTokenPattern       = regexp.MustCompile(`\bnpm_[A-Za-z0-9_]{10,}\b`)
	awsAccessKeyPattern   = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
	slackTokenPattern     = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	jwtPattern            = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	credentialURLPattern  = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)([^/\s:@]+(?::[^/\s@]+)?@)([^/\s]+)`)
	authorizationPattern  = regexp.MustCompile(`(?i)\b(Authorization\s*:\s*)[^\r\n"']+`)
	cookieHeaderPattern   = regexp.MustCompile(`(?i)\b((?:Cookie|Set-Cookie)\s*:\s*)[^\r\n]+`)
	bearerPattern         = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	bearerMarkerPattern   = regexp.MustCompile(`(?i)\bbearer\s*(?:[:=/]|\s+)\s*["']?` + regexp.QuoteMeta(RedactionMarker) + `["']?`)
	basicPattern          = regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9._~+/=-]{8,}`)
	privateKeyPattern     = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	omissionPattern       = regexp.MustCompile(`(?i)(\[(?:redacted|omitted|hidden|removed)\]|<(?:redacted|omitted|hidden|removed)>|\{(?:redacted|omitted|hidden|removed)\})`)
	npmAuthLinePattern    = regexp.MustCompile(`(?im)^(\s*(?://[^\s=]+:)?_authToken\s*=\s*)[^\r\n]+`)
	secretLinePattern     = regexp.MustCompile(`(?im)^(\s*(?:export\s+)?)([A-Za-z0-9_.-]*(?:api[_-]?key|[_.-]key|access[_-]?token|refresh[_-]?token|auth[_-]?token|token|password|secret|authorization|credential|cookie)[A-Za-z0-9_.-]*)(\s*[:=]\s*)([^\r\n]*)`)
	querySecretPattern    = regexp.MustCompile(`(?i)([?&;]([^=&;#\s]*?(?:api[_-]?key|[_.-]key|access[_-]?token|refresh[_-]?token|auth[_-]?token|token|password|secret|authorization|credential|cookie)[^=&;#\s]*)=)([^&#;\s]+)`)
	secretKVQuotedPattern = regexp.MustCompile(`(?i)(["']?)([A-Za-z0-9_.-]*(?:api[_-]?key|[_.-]key|access[_-]?token|refresh[_-]?token|auth[_-]?token|token|password|secret|authorization|credential|cookie)[A-Za-z0-9_.-]*)(["']?\s*[:=]\s*)(["'])([^"'\r\n]+)(["'])`)
	secretKVPattern       = regexp.MustCompile(`(?i)(["']?)([A-Za-z0-9_.-]*(?:api[_-]?key|[_.-]key|access[_-]?token|refresh[_-]?token|auth[_-]?token|token|password|secret|authorization|credential|cookie)[A-Za-z0-9_.-]*)(["']?\s*[:=]\s*)(["']?)([^"'\s,}&;#\r\n]+)(["']?)`)
	tokenLikePattern      = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9._~+=-]{31,}[A-Za-z0-9=]\b`)
	absolutePathPattern   = regexp.MustCompile(`(^|[\s="'(])(/[^\s"'<>),}]+)`)
)

var (
	ErrOutsideWorkspace = errors.New("refusing to access path outside workspace")
	ErrSymlinkOutside   = errors.New("refusing to follow symlink outside workspace")
	ErrSymlinkPath      = errors.New("refusing to follow symlinked path")
)

var registeredSensitiveLiterals = struct {
	mu     sync.RWMutex
	values map[string]struct{}
}{values: map[string]struct{}{}}

type Redactor struct {
	literals []string
}

type RedactionReport struct {
	Count int
	Kinds map[string]int
}

type PathPolicy struct {
	AllowHidden bool
}

type CommandPathRoot struct {
	Path  string
	Label string
}

type SafeConfig struct {
	PlanningAgents   int    `json:"planning_agents"`
	OpenAIModel      string `json:"openai_model,omitempty"`
	CodexModel       string `json:"codex_model,omitempty"`
	CodexBin         string `json:"codex_bin,omitempty"`
	TaskProposalMode string `json:"task_proposal_mode,omitempty"`
	ConfigFile       string `json:"config_file,omitempty"`
	OpenAIKeyEnv     string `json:"openai_api_key_env,omitempty"`
	OpenAIKeySet     bool   `json:"openai_api_key_present"`
	AllowNoGit       bool   `json:"allow_no_git"`
	SpecPath         string `json:"spec_path,omitempty"`
	TaskPath         string `json:"task_path,omitempty"`
}

func NewSafeConfig(cfg SafeConfig) SafeConfig {
	return SafeConfig{
		PlanningAgents:   cfg.PlanningAgents,
		OpenAIModel:      safeConfigString(cfg.OpenAIModel),
		CodexModel:       safeConfigString(cfg.CodexModel),
		CodexBin:         safeConfigString(cfg.CodexBin),
		TaskProposalMode: safeConfigString(cfg.TaskProposalMode),
		ConfigFile:       safeConfigString(cfg.ConfigFile),
		OpenAIKeyEnv:     safeConfigString(cfg.OpenAIKeyEnv),
		OpenAIKeySet:     cfg.OpenAIKeySet,
		AllowNoGit:       cfg.AllowNoGit,
		SpecPath:         safeConfigString(cfg.SpecPath),
		TaskPath:         safeConfigString(cfg.TaskPath),
	}
}

func Redact(s string) string {
	redacted, _ := NewRedactor(os.Environ()).RedactWithCount(s)
	return redacted
}

func RedactString(s string) string {
	return Redact(s)
}

func RedactStringWithCount(s string) (string, int) {
	redacted, report := NewRedactor(os.Environ()).RedactWithReport(s)
	return redacted, report.Count
}

func RedactStringWithReport(s string) (string, RedactionReport) {
	return NewRedactor(os.Environ()).RedactWithReport(s)
}

func RedactBytes(data []byte) []byte {
	redacted, _ := RedactBytesWithCount(data)
	return redacted
}

func RedactBytesWithCount(data []byte) ([]byte, int) {
	redacted, report := RedactStringWithReport(string(data))
	return []byte(redacted), report.Count
}

func RedactBytesWithReport(data []byte) ([]byte, RedactionReport) {
	redacted, report := RedactStringWithReport(string(data))
	return []byte(redacted), report
}

func RedactJSONBytes(data []byte) []byte {
	redacted, _ := RedactJSONBytesWithCount(data)
	return redacted
}

func RedactJSONBytesWithCount(data []byte) ([]byte, int) {
	redacted, report := RedactJSONBytesWithReport(data)
	return redacted, report.Count
}

func RedactJSONBytesWithReport(data []byte) ([]byte, RedactionReport) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return RedactBytesWithReport(data)
	}
	redacted, report := RedactJSONValueWithReport(value)
	out, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return RedactBytesWithReport(data)
	}
	return append(out, '\n'), report
}

func RedactContent(path string, data []byte) []byte {
	redacted, _ := RedactContentWithCount(path, data)
	return redacted
}

func RedactContentWithCount(path string, data []byte) ([]byte, int) {
	redacted, report := RedactContentWithReport(path, data)
	return redacted, report.Count
}

func RedactContentWithReport(path string, data []byte) ([]byte, RedactionReport) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return RedactJSONBytesWithReport(data)
	case ".jsonl":
		return RedactJSONLinesBytesWithReport(data)
	default:
		return RedactBytesWithReport(data)
	}
}

func RedactJSONLinesBytes(data []byte) []byte {
	redacted, _ := RedactJSONLinesBytesWithCount(data)
	return redacted
}

func RedactJSONLinesBytesWithCount(data []byte) ([]byte, int) {
	redacted, report := RedactJSONLinesBytesWithReport(data)
	return redacted, report.Count
}

func RedactJSONLinesBytesWithReport(data []byte) ([]byte, RedactionReport) {
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
		redacted, lineReport := redactJSONLineBytesWithReport(body)
		report.Merge(lineReport)
		out = append(out, redacted...)
		if hasNewline {
			out = append(out, '\n')
		}
	}
	return out, report
}

func redactJSONLineBytes(data []byte) []byte {
	redacted, _ := redactJSONLineBytesWithCount(data)
	return redacted
}

func redactJSONLineBytesWithCount(data []byte) ([]byte, int) {
	redacted, report := redactJSONLineBytesWithReport(data)
	return redacted, report.Count
}

func redactJSONLineBytesWithReport(data []byte) ([]byte, RedactionReport) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		redacted, report := RedactBytesWithReport(data)
		return bytes.TrimSuffix(redacted, []byte("\n")), report
	}
	redacted, report := RedactJSONValueWithReport(value)
	out, err := json.Marshal(redacted)
	if err != nil {
		redacted, fallbackReport := RedactBytesWithReport(data)
		return bytes.TrimSuffix(redacted, []byte("\n")), fallbackReport
	}
	return out, report
}

func RedactJSONValue(value any) any {
	redacted, _ := RedactJSONValueWithCount(value)
	return redacted
}

func RedactJSONValueWithCount(value any) (any, int) {
	redacted, report := RedactJSONValueWithReport(value)
	return redacted, report.Count
}

func RedactJSONValueWithReport(value any) (any, RedactionReport) {
	return redactJSONValue(value, "")
}

func RedactMap(value map[string]any) map[string]any {
	redacted, _ := RedactMapWithCount(value)
	return redacted
}

func RedactMapWithCount(value map[string]any) (map[string]any, int) {
	redacted, report := RedactMapWithReport(value)
	return redacted, report.Count
}

func RedactMapWithReport(value map[string]any) (map[string]any, RedactionReport) {
	redacted, report := RedactJSONValueWithReport(value)
	if mapped, ok := redacted.(map[string]any); ok {
		return mapped, report
	}
	return map[string]any{}, report
}

func NewRedactor(env []string) Redactor {
	seen := map[string]bool{}
	var literals []string
	addLiteral := func(value string) {
		if len(strings.TrimSpace(value)) < 4 || lowInformationLiteral(value) || value == RedactionMarker || value == legacyRedactionMarker {
			return
		}
		if seen[value] {
			return
		}
		seen[value] = true
		literals = append(literals, value)
	}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !SensitiveEnvKey(key) || isEnvNameReferenceKey(key) || isSecretPresenceKey(key) {
			continue
		}
		addLiteral(value)
	}
	registeredSensitiveLiterals.mu.RLock()
	for value := range registeredSensitiveLiterals.values {
		addLiteral(value)
	}
	registeredSensitiveLiterals.mu.RUnlock()
	sort.Slice(literals, func(i, j int) bool { return len(literals[i]) > len(literals[j]) })
	return Redactor{literals: literals}
}

func RegisterSensitiveLiterals(values ...string) {
	registeredSensitiveLiterals.mu.Lock()
	defer registeredSensitiveLiterals.mu.Unlock()
	if registeredSensitiveLiterals.values == nil {
		registeredSensitiveLiterals.values = map[string]struct{}{}
	}
	for _, value := range values {
		if len(strings.TrimSpace(value)) < 4 || lowInformationLiteral(value) || value == RedactionMarker || value == legacyRedactionMarker {
			continue
		}
		registeredSensitiveLiterals.values[value] = struct{}{}
	}
}

func RegisterSensitiveConfigJSON(data []byte) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return
	}
	var literals []string
	collectSensitiveConfigLiterals(value, "", false, &literals)
	RegisterSensitiveLiterals(literals...)
}

func (r Redactor) Redact(s string) string {
	redacted, _ := r.RedactWithCount(s)
	return redacted
}

func (r Redactor) RedactWithCount(s string) (string, int) {
	redacted, report := r.RedactWithReport(s)
	return redacted, report.Count
}

func (r Redactor) RedactWithReport(s string) (string, RedactionReport) {
	report := RedactionReport{}
	for _, value := range r.literals {
		if value == RedactionMarker || value == legacyRedactionMarker {
			continue
		}
		if n := strings.Count(s, value); n > 0 {
			report.Add("configured_secret", n)
			s = strings.ReplaceAll(s, value, RedactionMarker)
		}
	}
	if n := strings.Count(s, legacyRedactionMarker); n > 0 {
		report.Add("legacy_redaction_marker", n)
		s = strings.ReplaceAll(s, legacyRedactionMarker, RedactionMarker)
	}
	s = replacePatternWithReport(s, omissionPattern, RedactionMarker, &report, "omission_placeholder")
	s = replacePatternWithReport(s, bearerMarkerPattern, RedactionMarker, &report, "bearer_marker")
	s = replacePatternWithReport(s, privateKeyPattern, RedactionMarker, &report, "private_key")
	s = replacePatternWithReport(s, npmAuthLinePattern, "${1}"+RedactionMarker, &report, "npm_auth_token")
	s = replacePatternWithReport(s, authorizationPattern, "${1}"+RedactionMarker, &report, "authorization_header")
	s = replacePatternWithReport(s, cookieHeaderPattern, "${1}"+RedactionMarker, &report, "cookie_header")
	s = replacePatternWithReport(s, bearerPattern, RedactionMarker, &report, "bearer_token")
	s = replacePatternWithReport(s, basicPattern, RedactionMarker, &report, "basic_credential")
	s = replacePatternWithReport(s, credentialURLPattern, "${1}${3}", &report, "credential_url")
	s = redactSensitiveLinesWithReport(s, &report)
	s = redactQuerySecretsWithReport(s, &report)
	s = replacePatternWithReport(s, openAIKeyPattern, RedactionMarker, &report, "openai_key")
	s = replacePatternWithReport(s, githubTokenPattern, RedactionMarker, &report, "github_token")
	s = replacePatternWithReport(s, npmTokenPattern, RedactionMarker, &report, "npm_token")
	s = replacePatternWithReport(s, awsAccessKeyPattern, RedactionMarker, &report, "aws_access_key")
	s = replacePatternWithReport(s, slackTokenPattern, RedactionMarker, &report, "slack_token")
	s = replacePatternWithReport(s, jwtPattern, RedactionMarker, &report, "jwt")
	s = secretKVQuotedPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := secretKVQuotedPattern.FindStringSubmatch(match)
		if len(parts) != 7 {
			return match
		}
		if isEnvNameReferenceKey(parts[2]) || isSecretPresenceKey(parts[2]) || parts[5] == RedactionMarker {
			return match
		}
		report.Add("secret_field", 1)
		return parts[1] + parts[2] + parts[3] + parts[4] + RedactionMarker + parts[6]
	})
	s = secretKVPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := secretKVPattern.FindStringSubmatch(match)
		if len(parts) != 7 {
			return match
		}
		if isEnvNameReferenceKey(parts[2]) || isSecretPresenceKey(parts[2]) || parts[5] == RedactionMarker {
			return match
		}
		if parts[4] == "" && strings.Contains(parts[3], ":") && isJSONPrimitive(parts[5]) {
			return match
		}
		report.Add("secret_field", 1)
		return parts[1] + parts[2] + parts[3] + parts[4] + RedactionMarker + parts[6]
	})
	s = redactTokenLikeStringsWithReport(s, &report)
	return s, report
}

func SensitiveEnvKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		return false
	}
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "AUTHORIZATION", "COOKIE"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func (r *RedactionReport) Add(kind string, count int) {
	if r == nil || count <= 0 {
		return
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "unknown"
	}
	r.Count += count
	if r.Kinds == nil {
		r.Kinds = map[string]int{}
	}
	r.Kinds[kind] += count
}

func (r *RedactionReport) Merge(other RedactionReport) {
	if r == nil || other.Count <= 0 {
		return
	}
	r.Count += other.Count
	if len(other.Kinds) == 0 {
		if r.Kinds == nil {
			r.Kinds = map[string]int{}
		}
		r.Kinds["unknown"] += other.Count
		return
	}
	if r.Kinds == nil {
		r.Kinds = map[string]int{}
	}
	for kind, count := range other.Kinds {
		if count > 0 {
			r.Kinds[kind] += count
		}
	}
}

func (r RedactionReport) KindNames() []string {
	kinds := make([]string, 0, len(r.Kinds))
	for kind, count := range r.Kinds {
		if strings.TrimSpace(kind) != "" && count > 0 {
			kinds = append(kinds, kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}

func SanitizeDisplayString(s string, roots ...CommandPathRoot) string {
	redacted, _ := SanitizeDisplayStringWithReport(s, roots...)
	return redacted
}

func SanitizeDisplayStringWithReport(s string, roots ...CommandPathRoot) (string, RedactionReport) {
	report := RedactionReport{}
	replaced := replaceCommandPaths(s, commandPathReplacements(roots...))
	if replaced != s {
		report.Add("path_label", 1)
	}
	replaced = redactAbsolutePathsWithReport(replaced, &report)
	redacted, redactionReport := RedactStringWithReport(replaced)
	report.Merge(redactionReport)
	return redacted, report
}

func FilterEnv(env []string, allowNames ...string) []string {
	allow := map[string]bool{}
	for _, name := range allowNames {
		allow[strings.ToUpper(strings.TrimSpace(name))] = true
	}
	out := make([]string, 0, len(env))
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		upperName := strings.ToUpper(strings.TrimSpace(name))
		if allow[upperName] {
			out = append(out, item)
			continue
		}
		if SensitiveEnvKey(upperName) || isSensitiveCommandEnv(upperName) {
			continue
		}
		if secretLookingEnvValue(item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func SanitizeCommandArgv(argv []string, roots ...CommandPathRoot) []string {
	replacements := commandPathReplacements(roots...)
	out := make([]string, 0, len(argv))
	redactNext := false
	for _, arg := range argv {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if redactNext {
			out = append(out, RedactionMarker)
			redactNext = false
			continue
		}
		if sensitiveCommandArgName(arg) {
			redactedArg := replaceCommandPaths(arg, replacements)
			if name, _, ok := strings.Cut(redactedArg, "="); ok {
				out = append(out, strings.TrimSpace(name)+"="+RedactionMarker)
				continue
			}
			redactedArg = strings.TrimSpace(Redact(redactedArg))
			out = append(out, redactedArg)
			if !strings.Contains(arg, "=") {
				redactNext = true
			}
			continue
		}
		arg = replaceCommandPaths(arg, replacements)
		arg = strings.TrimSpace(Redact(arg))
		if arg == "" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func safeConfigString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	redacted := RedactString(value)
	if strings.Contains(redacted, RedactionMarker) {
		return ""
	}
	return redacted
}

func SafePathInRoot(root, path string, policy PathPolicy) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	if strings.ContainsRune(path, 0) || hasControlCharacter(path) || hasEncodedPathMeta(path) {
		return "", ErrOutsideWorkspace
	}
	if !filepath.IsAbs(path) {
		return "", ErrOutsideWorkspace
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrOutsideWorkspace
	}
	return SafeJoin(absRoot, filepath.ToSlash(rel), policy)
}

func ResolveCommandCWD(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", errors.New("command cwd is required")
	}
	if strings.ContainsRune(cwd, 0) || hasControlCharacter(cwd) || hasEncodedPathMeta(cwd) {
		return "", errors.New("command cwd is not allowed")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", errors.New("command cwd is not allowed")
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("command cwd does not exist")
		}
		return "", errors.New("command cwd is not readable")
	}
	if !info.IsDir() {
		return "", errors.New("command cwd is not a directory")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("command cwd is not readable")
	}
	return filepath.Clean(resolved), nil
}

func ContainsEncodedPathMeta(path string) bool {
	return hasEncodedPathMeta(path)
}

func CleanRelativePath(rel string, policy PathPolicy) (string, error) {
	if strings.ContainsRune(rel, 0) || hasControlCharacter(rel) {
		return "", ErrOutsideWorkspace
	}
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("path is required")
	}
	if len(rel) > maxRelativePathLen || hasEncodedPathMeta(rel) {
		return "", ErrOutsideWorkspace
	}
	if strings.Contains(rel, `\`) {
		return "", ErrOutsideWorkspace
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "//") || isWindowsDrivePath(rel) {
		return "", ErrOutsideWorkspace
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrOutsideWorkspace
		}
		if len(part) > maxPathSegmentLen || hasControlCharacter(part) {
			return "", ErrOutsideWorkspace
		}
		if !policy.AllowHidden && strings.HasPrefix(part, ".") {
			return "", ErrOutsideWorkspace
		}
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean != rel || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrOutsideWorkspace
	}
	return clean, nil
}

func SafeJoin(root, rel string, policy PathPolicy) (string, error) {
	clean, err := CleanRelativePath(rel, policy)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(filepath.Join(absRoot, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	if !withinRoot(absRoot, absPath) {
		return "", ErrOutsideWorkspace
	}
	if err := ensureSymlinksWithinRoot(absRoot, clean); err != nil {
		return "", err
	}
	return absPath, nil
}

func SafeJoinNoSymlinks(root, rel string, policy PathPolicy) (string, error) {
	clean, err := CleanRelativePath(rel, policy)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(filepath.Join(absRoot, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	if !withinRoot(absRoot, absPath) {
		return "", ErrOutsideWorkspace
	}
	if err := ensureNoSymlinksInRelativePath(absRoot, clean); err != nil {
		return "", err
	}
	return absPath, nil
}

func isSensitiveCommandEnv(name string) bool {
	switch name {
	case "GIT_ASKPASS", "SSH_ASKPASS", "JJ_GITHUB_ASKPASS_TOKEN":
		return true
	default:
		return false
	}
}

func replacePatternWithReport(s string, pattern *regexp.Regexp, replacement string, report *RedactionReport, kind string) string {
	matches := pattern.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	report.Add(kind, len(matches))
	return pattern.ReplaceAllString(s, replacement)
}

func redactAbsolutePathsWithReport(s string, report *RedactionReport) string {
	matches := absolutePathPattern.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	report.Add("absolute_path", len(matches))
	return absolutePathPattern.ReplaceAllString(s, "${1}[path]")
}

func redactSensitiveLinesWithReport(s string, report *RedactionReport) string {
	return secretLinePattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := secretLinePattern.FindStringSubmatch(match)
		if len(parts) != 5 {
			return match
		}
		if isEnvNameReferenceKey(parts[2]) || isSecretPresenceKey(parts[2]) {
			return match
		}
		value := strings.TrimSpace(parts[4])
		if value == "" || value == RedactionMarker {
			return match
		}
		report.Add("env_assignment", 1)
		return parts[1] + parts[2] + parts[3] + RedactionMarker
	})
}

func redactQuerySecretsWithReport(s string, report *RedactionReport) string {
	return querySecretPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := querySecretPattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		if isEnvNameReferenceKey(parts[2]) || isSecretPresenceKey(parts[2]) || parts[3] == RedactionMarker {
			return match
		}
		report.Add("query_secret", 1)
		return parts[1] + RedactionMarker
	})
}

func redactTokenLikeStringsWithReport(s string, report *RedactionReport) string {
	return tokenLikePattern.ReplaceAllStringFunc(s, func(match string) string {
		if !looksHighEntropyTokenLike(match) {
			return match
		}
		report.Add("token_like", 1)
		return RedactionMarker
	})
}

func looksHighEntropyTokenLike(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 32 {
		return false
	}
	if isHexString(value) {
		return false
	}
	var lower, upper, digit, symbol int
	unique := map[rune]struct{}{}
	for _, r := range value {
		unique[r] = struct{}{}
		switch {
		case r >= 'a' && r <= 'z':
			lower++
		case r >= 'A' && r <= 'Z':
			upper++
		case r >= '0' && r <= '9':
			digit++
		default:
			symbol++
		}
	}
	if len(unique) < 12 || digit == 0 || lower+upper == 0 {
		return false
	}
	if symbol > 0 && lower+upper >= 8 {
		return true
	}
	return len(value) >= 40 && lower > 0 && upper > 0 && digit >= 4 && len(unique) >= 16
}

func lowInformationLiteral(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "false", "null", "none", "yes", "no", "on", "off":
		return true
	default:
		return false
	}
}

func secretLookingEnvValue(item string) bool {
	_, value, ok := strings.Cut(item, "=")
	if !ok {
		return false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	redacted := RedactString(value)
	return redacted != value && strings.Contains(redacted, RedactionMarker)
}

func isHexString(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func sensitiveCommandArgName(arg string) bool {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return false
	}
	hasFlagPrefix := strings.HasPrefix(arg, "-")
	hasEquals := strings.Contains(arg, "=")
	if !hasFlagPrefix && !hasEquals {
		return false
	}
	if before, _, ok := strings.Cut(arg, "="); ok {
		arg = before
	}
	arg = strings.TrimLeft(arg, "-")
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return false
	}
	return sensitiveDataKey(arg)
}

func redactJSONValue(value any, key string) (any, RedactionReport) {
	if isRedactionMetadataKey(key) {
		return value, RedactionReport{}
	}
	if sensitiveDataKey(key) && value != nil {
		if s, ok := value.(string); ok && (s == "" || s == RedactionMarker) {
			return RedactionMarker, RedactionReport{}
		}
		return RedactionMarker, RedactionReport{Count: 1, Kinds: map[string]int{"sensitive_json_key": 1}}
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		report := RedactionReport{}
		for childKey, childValue := range v {
			redacted, childReport := redactJSONValue(childValue, childKey)
			out[childKey] = redacted
			report.Merge(childReport)
		}
		return out, report
	case []any:
		out := make([]any, len(v))
		report := RedactionReport{}
		for i, item := range v {
			redacted, childReport := redactJSONValue(item, key)
			out[i] = redacted
			report.Merge(childReport)
		}
		return out, report
	case string:
		return RedactStringWithReport(v)
	default:
		return redactReflectJSONValue(v, key)
	}
}

func redactReflectJSONValue(value any, key string) (any, RedactionReport) {
	if value == nil {
		return nil, RedactionReport{}
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return value, RedactionReport{}
		}
		return redactJSONValue(rv.Elem().Interface(), key)
	case reflect.Struct:
		data, err := json.Marshal(value)
		if err != nil {
			return value, RedactionReport{}
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return value, RedactionReport{}
		}
		return redactJSONValue(decoded, key)
	case reflect.Map:
		if rv.IsNil() || rv.Type().Key().Kind() != reflect.String {
			return value, RedactionReport{}
		}
		out := make(map[string]any, rv.Len())
		report := RedactionReport{}
		iter := rv.MapRange()
		for iter.Next() {
			childKey := iter.Key().String()
			redacted, childReport := redactJSONValue(iter.Value().Interface(), childKey)
			out[childKey] = redacted
			report.Merge(childReport)
		}
		return out, report
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return value, RedactionReport{}
		}
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return value, RedactionReport{}
		}
		out := make([]any, rv.Len())
		report := RedactionReport{}
		for i := 0; i < rv.Len(); i++ {
			redacted, childReport := redactJSONValue(rv.Index(i).Interface(), key)
			out[i] = redacted
			report.Merge(childReport)
		}
		return out, report
	default:
		return value, RedactionReport{}
	}
}

func sensitiveDataKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" || isEnvNameReferenceKey(key) || isSecretPresenceKey(key) {
		return false
	}
	delimited := strings.NewReplacer("-", "_", ".", "_").Replace(key)
	if delimited == "key" || strings.HasSuffix(delimited, "_key") || strings.Contains(delimited, "_key_") {
		return true
	}
	for _, marker := range []string{"api_key", "api-key", "access_token", "access-token", "refresh_token", "refresh-token", "auth_token", "auth-token", "token", "password", "secret", "authorization", "credential", "cookie"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(key)
	for _, marker := range []string{"apikey", "accesstoken", "refreshtoken", "authtoken", "token", "password", "secret", "authorization", "credential", "cookie"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func collectSensitiveConfigLiterals(value any, key string, inheritedSensitive bool, out *[]string) {
	if isRedactionMetadataKey(key) {
		return
	}
	currentSensitive := inheritedSensitive || sensitiveDataKey(key)
	switch v := value.(type) {
	case map[string]any:
		for childKey, childValue := range v {
			collectSensitiveConfigLiterals(childValue, childKey, currentSensitive, out)
		}
	case []any:
		for _, item := range v {
			collectSensitiveConfigLiterals(item, key, currentSensitive, out)
		}
	case string:
		if currentSensitive && !isEnvNameReferenceKey(key) && !isSecretPresenceKey(key) {
			*out = append(*out, v)
		}
	}
}

func isEnvNameReferenceKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.HasSuffix(key, "_env") || strings.HasSuffix(key, "-env") || strings.HasSuffix(key, ".env")
}

func isSecretPresenceKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.HasSuffix(key, "_present") || strings.HasSuffix(key, "-present") || strings.HasSuffix(key, ".present") ||
		strings.HasSuffix(key, "_set") || strings.HasSuffix(key, "-set") || strings.HasSuffix(key, ".set")
}

func isRedactionMetadataKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "redaction_kinds" ||
		key == "redaction_kind_counts" ||
		strings.HasSuffix(key, "redaction_categories") ||
		strings.HasSuffix(key, "redaction_category_counts")
}

func isJSONPrimitive(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "true" || value == "false" || value == "null" {
		return true
	}
	if value == "" {
		return false
	}
	for i, r := range value {
		if r >= '0' && r <= '9' {
			continue
		}
		if i == 0 && r == '-' {
			continue
		}
		return false
	}
	return true
}

func hasEncodedPathMeta(path string) bool {
	candidate := path
	for i := 0; i < 4; i++ {
		lower := strings.ToLower(candidate)
		for _, token := range []string{"%00", "%2e", "%2f", "%5c"} {
			if strings.Contains(lower, token) {
				return true
			}
		}
		decoded, err := url.PathUnescape(candidate)
		if err != nil || decoded == candidate {
			return false
		}
		decoded = strings.ReplaceAll(decoded, `\`, "/")
		if strings.Contains(decoded, "../") || strings.HasPrefix(decoded, "/") || strings.Contains(decoded, "\x00") {
			return true
		}
		candidate = decoded
	}
	return false
}

func hasControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func ensureSymlinksWithinRoot(absRoot, cleanRel string) error {
	evalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return err
	}
	current := absRoot
	for _, part := range strings.Split(filepath.FromSlash(cleanRel), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := filepath.EvalSymlinks(current)
		if err != nil {
			return err
		}
		if !withinRoot(evalRoot, target) {
			return ErrSymlinkOutside
		}
		current = target
	}
	return nil
}

func ensureNoSymlinksInRelativePath(absRoot, cleanRel string) error {
	if info, err := os.Lstat(absRoot); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlinkPath
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current := absRoot
	for _, part := range strings.Split(filepath.FromSlash(cleanRel), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlinkPath
		}
	}
	return nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func isWindowsDrivePath(rel string) bool {
	if len(rel) < 2 || rel[1] != ':' {
		return false
	}
	c := rel[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

type commandPathReplacement struct {
	path  string
	slash string
	label string
}

func commandPathReplacements(roots ...CommandPathRoot) []commandPathReplacement {
	replacements := make([]commandPathReplacement, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		path := strings.TrimSpace(root.Path)
		label := strings.TrimSpace(root.Label)
		if path == "" || label == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		abs = filepath.Clean(abs)
		if abs == string(filepath.Separator) || seen[abs] {
			continue
		}
		seen[abs] = true
		replacements = append(replacements, commandPathReplacement{
			path:  abs,
			slash: filepath.ToSlash(abs),
			label: label,
		})
	}
	sort.Slice(replacements, func(i, j int) bool {
		return len(replacements[i].path) > len(replacements[j].path)
	})
	return replacements
}

func replaceCommandPaths(arg string, replacements []commandPathReplacement) string {
	for _, repl := range replacements {
		arg = replaceOneCommandPath(arg, repl.path, repl.label, string(filepath.Separator))
		if repl.slash != repl.path {
			arg = replaceOneCommandPath(arg, repl.slash, repl.label, "/")
		}
	}
	return arg
}

func replaceOneCommandPath(arg, root, label, sep string) string {
	if root == "" {
		return arg
	}
	if arg == root {
		return label
	}
	prefix := root + sep
	if strings.HasPrefix(arg, prefix) {
		return label + "/" + filepath.ToSlash(strings.TrimPrefix(arg, prefix))
	}
	return strings.ReplaceAll(arg, prefix, label+"/")
}
