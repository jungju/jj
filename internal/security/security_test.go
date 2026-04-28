package security

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRedactPatternsAndLiteralEnvSecrets(t *testing.T) {
	redactor := NewRedactor([]string{
		"JJ_TEST_SECRET=literal-secret-value",
		"MY_CREDENTIAL=credential-value",
		"VISIBLE=value",
	})
	input := strings.Join([]string{
		"OPENAI_API_KEY=sk-proj-redact1234567890",
		"Authorization: Bearer bearersecret123456",
		`curl -H "Authorization: Basic dXNlcjpwYXNz" https://example.test`,
		"token=literal-secret-value",
		"credential=credential-value",
		"github_pat_1234567890abcdef",
		"https://user:pass@example.com/repo.git",
		"ssh://git:credential-value@example.com/repo.git",
	}, "\n")

	got := redactor.Redact(input)
	for _, leaked := range []string{
		"sk-proj-redact1234567890",
		"bearersecret123456",
		"dXNlcjpwYXNz",
		"literal-secret-value",
		"credential-value",
		"github_pat_1234567890abcdef",
		"user:pass@",
		"git:credential-value@",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q:\n%s", leaked, got)
		}
	}
	if count := strings.Count(got, RedactionMarker); count < 5 {
		t.Fatalf("expected redaction markers, got %d in:\n%s", count, got)
	}
	if !strings.Contains(got, "https://example.com/repo.git") {
		t.Fatalf("credential URL was not preserved safely:\n%s", got)
	}
	if !strings.Contains(got, "ssh://example.com/repo.git") {
		t.Fatalf("non-http credential URL was not preserved safely:\n%s", got)
	}
}

func TestRedactNormalizesLegacyPlaceholderLiteral(t *testing.T) {
	legacy := "[" + "REDACT" + "ED]"
	if RedactionMarker == legacy {
		t.Fatalf("redaction marker must not use legacy literal %q", legacy)
	}

	got, report := RedactStringWithReport("planner returned " + legacy + " in output")
	if strings.Contains(got, legacy) {
		t.Fatalf("legacy redaction placeholder should not be persisted:\n%s", got)
	}
	if !strings.Contains(got, RedactionMarker) {
		t.Fatalf("legacy redaction placeholder should be normalized:\n%s", got)
	}
	if report.Kinds["legacy_redaction_marker"] == 0 {
		t.Fatalf("legacy marker normalization was not reported: %#v", report.Kinds)
	}
}

func TestRedactNormalizesOmissionPlaceholdersAndBasicCredentials(t *testing.T) {
	got, report := RedactStringWithReport("provider returned [omitted] and <hidden>; proxy used Basic dXNlcjpwYXNzMTIz")
	for _, leaked := range []string{"[omitted]", "<hidden>", "dXNlcjpwYXNzMTIz", "Basic [jj-omitted]"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked placeholder or credential %q:\n%s", leaked, got)
		}
	}
	if strings.Count(got, RedactionMarker) < 3 {
		t.Fatalf("expected redaction markers, got:\n%s", got)
	}
	if report.Kinds["omission_placeholder"] == 0 || report.Kinds["basic_credential"] == 0 {
		t.Fatalf("expected omission/basic redaction kinds, got %#v", report.Kinds)
	}
}

func TestRedactWithCountReportsPracticalReplacementCount(t *testing.T) {
	redactor := NewRedactor([]string{"JJ_TEST_SECRET=literal-secret-value"})
	got, count := redactor.RedactWithCount("Authorization: Bearer bearer-secret\napi_key=literal-secret-value\nsk-proj-count1234567890\n")
	if strings.Contains(got, "bearer-secret") || strings.Contains(got, "literal-secret-value") || strings.Contains(got, "sk-proj-count1234567890") {
		t.Fatalf("redacted output leaked secret:\n%s", got)
	}
	if count < 3 {
		t.Fatalf("expected at least three redactions, got %d in:\n%s", count, got)
	}
}

func TestRedactWithReportRecordsKindsWithoutValues(t *testing.T) {
	redactor := NewRedactor([]string{"JJ_TEST_SECRET=literal-secret-value"})
	got, report := redactor.RedactWithReport("Authorization: Bearer bearer-secret\napi_key=literal-secret-value\nsk-proj-count1234567890\n")
	if strings.Contains(got, "bearer-secret") || strings.Contains(got, "literal-secret-value") || strings.Contains(got, "sk-proj-count1234567890") {
		t.Fatalf("redacted output leaked secret:\n%s", got)
	}
	if report.Count < 3 {
		t.Fatalf("expected redaction count, got %#v", report)
	}
	for _, want := range []string{"authorization_header", "configured_secret", "openai_key"} {
		if report.Kinds[want] == 0 {
			t.Fatalf("missing redaction kind %q in %#v", want, report.Kinds)
		}
	}
	for kind := range report.Kinds {
		if strings.Contains(kind, "literal-secret-value") || strings.Contains(kind, "bearer-secret") {
			t.Fatalf("redaction kind leaked original value: %#v", report.Kinds)
		}
	}
}

func TestRedactStandaloneBearerTokenDoesNotPreserveBearerShape(t *testing.T) {
	got := RedactString("request failed with Bearer bearer-secret-value")
	if strings.Contains(got, "bearer-secret-value") || strings.Contains(got, "Bearer [jj-omitted]") {
		t.Fatalf("standalone bearer token kept an unsafe shape:\n%s", got)
	}
	if !strings.Contains(got, RedactionMarker) {
		t.Fatalf("standalone bearer token missing redaction marker:\n%s", got)
	}
}

func TestRedactConfiguredBearerLiteralDoesNotPreserveBearerShape(t *testing.T) {
	redactor := NewRedactor([]string{"JJ_BEARER_TOKEN=bearer-literal-secret"})
	input := strings.Join([]string{
		"request failed with Bearer bearer-literal-secret",
		"callback used bearer/bearer-literal-secret",
		`metadata bearer="bearer-literal-secret"`,
		"provider returned Bearer [omitted]",
	}, "\n")
	got, report := redactor.RedactWithReport(input)
	for _, leaked := range []string{
		"bearer-literal-secret",
		"Bearer [jj-omitted]",
		"bearer/[jj-omitted]",
		`bearer="[jj-omitted]"`,
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("configured bearer redaction kept unsafe shape %q:\n%s", leaked, got)
		}
	}
	if strings.Count(got, RedactionMarker) < 4 {
		t.Fatalf("configured bearer redaction missing markers:\n%s", got)
	}
	if report.Kinds["bearer_marker"] == 0 {
		t.Fatalf("expected bearer_marker report kind, got %#v", report.Kinds)
	}
}

func TestRedactStringCoversCommonArtifactFormats(t *testing.T) {
	privateKey := "-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----"
	tests := []struct {
		name  string
		input string
		leaks []string
		want  []string
	}{
		{
			name:  "markdown and bearer token",
			input: "key sk-proj-redactabcdef123456\nAuthorization: Bearer bearer-secret-value\n",
			leaks: []string{"sk-proj-redactabcdef123456", "bearer-secret-value"},
		},
		{
			name:  "json key value",
			input: `{"OPENAI_API_KEY":"plain-secret-value","openai_api_key_env":"OPENAI_API_KEY"}`,
			leaks: []string{"plain-secret-value"},
			want:  []string{"OPENAI_API_KEY"},
		},
		{
			name:  "env assignment",
			input: "DATABASE_PASSWORD=hunter2\nVISIBLE=value\n",
			leaks: []string{"hunter2"},
			want:  []string{"VISIBLE=value"},
		},
		{
			name:  "command output private key",
			input: "command failed\n" + privateKey + "\n",
			leaks: []string{privateKey, "abc123"},
		},
		{
			name:  "git diff npm token",
			input: "+//registry.npmjs.org/:_authToken=npm_secret_value\n+npm_token=npm_abcdefghijklmnopqrstuvwxyz\n",
			leaks: []string{"npm_secret_value", "npm_abcdefghijklmnopqrstuvwxyz"},
		},
		{
			name:  "high confidence inline tokens",
			input: "aws AKIAIOSFODNN7EXAMPLE slack xoxb-" + "123456789012-123456789012-abcdefghijklmnopqrstuvwx jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\n",
			leaks: []string{"AKIAIOSFODNN7EXAMPLE", "xoxb-" + "123456789012-123456789012-abcdefghijklmnopqrstuvwx", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
		},
		{
			name:  "generic high entropy token",
			input: "opaque token AbCdEfGhIjKlMnOpQrStUvWxYz_1234567890",
			leaks: []string{"AbCdEfGhIjKlMnOpQrStUvWxYz_1234567890"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactString(tt.input)
			for _, leak := range tt.leaks {
				if strings.Contains(got, leak) {
					t.Fatalf("redacted output leaked %q:\n%s", leak, got)
				}
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("redacted output missing %q:\n%s", want, got)
				}
			}
			if !strings.Contains(got, RedactionMarker) {
				t.Fatalf("redacted output missing marker:\n%s", got)
			}
		})
	}
}

func TestRedactJSONValueRecursesAndUsesKeys(t *testing.T) {
	input := map[string]any{
		"token":              "short",
		"service_key":        "generic-key-secret",
		"apiKey":             "camel-api-key-secret",
		"clientSecret":       "camel-client-secret",
		"refreshToken":       "camel-refresh-token",
		"openai_api_key_env": "OPENAI_API_KEY",
		"nested": map[string]any{
			"password": "hunter2",
			"notes":    []any{"visible", "sk-proj-jsonsecret1234567890"},
		},
	}
	data, err := json.Marshal(RedactJSONValue(input))
	if err != nil {
		t.Fatalf("marshal redacted json: %v", err)
	}
	got := string(data)
	for _, leak := range []string{"short", "generic-key-secret", "camel-api-key-secret", "camel-client-secret", "camel-refresh-token", "hunter2", "sk-proj-jsonsecret1234567890"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted json leaked %q:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "OPENAI_API_KEY") {
		t.Fatalf("env variable name should remain visible:\n%s", got)
	}
}

func TestRedactMapHelperRedactsNestedMap(t *testing.T) {
	input := map[string]any{
		"api_key": "plain-secret-value",
		"nested": map[string]any{
			"message": "Authorization: Basic dXNlcjpwYXNz",
		},
	}
	got := RedactMap(input)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal redacted map: %v", err)
	}
	text := string(data)
	for _, leak := range []string{"plain-secret-value", "dXNlcjpwYXNz"} {
		if strings.Contains(text, leak) {
			t.Fatalf("redacted map leaked %q:\n%s", leak, text)
		}
	}
	if strings.Count(text, RedactionMarker) < 2 {
		t.Fatalf("redacted map missing markers:\n%s", text)
	}
}

func TestRedactJSONValueHandlesConcreteEnvMapsAndSlices(t *testing.T) {
	input := map[string]string{
		"SERVICE_KEY":        "generic-key-secret",
		"SESSION_TOKEN":      "token-secret",
		"OPENAI_API_KEY_ENV": "OPENAI_API_KEY",
		"VISIBLE":            "ok",
	}
	redacted, count := RedactJSONValueWithCount(input)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted env map: %v", err)
	}
	got := string(data)
	for _, leak := range []string{"generic-key-secret", "token-secret"} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted env map leaked %q:\n%s", leak, got)
		}
	}
	for _, want := range []string{"OPENAI_API_KEY", "VISIBLE", "ok", RedactionMarker} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted env map missing %q:\n%s", want, got)
		}
	}
	if count < 2 {
		t.Fatalf("expected concrete map redaction count, got %d in %s", count, got)
	}

	sliceRedacted := RedactJSONValue([]string{"safe", "sk-proj-slicesecret1234567890"})
	data, err = json.Marshal(sliceRedacted)
	if err != nil {
		t.Fatalf("marshal redacted slice: %v", err)
	}
	got = string(data)
	if strings.Contains(got, "sk-proj-slicesecret1234567890") || !strings.Contains(got, RedactionMarker) {
		t.Fatalf("redacted slice leaked secret:\n%s", got)
	}
}

func TestRedactContentUsesJSONKeysAndJSONLines(t *testing.T) {
	jsonData := []byte(`{"clientSecret":"secret value with spaces","visible":"ok"}`)
	got := string(RedactContent("manifest.json", jsonData))
	if strings.Contains(got, "secret value with spaces") || !strings.Contains(got, RedactionMarker) || !strings.Contains(got, `"visible": "ok"`) {
		t.Fatalf("json content was not key-redacted:\n%s", got)
	}

	jsonlData := []byte("{\"token\":\"line secret with spaces\"}\n{\"message\":\"Authorization: Bearer bearer-secret-value\"}\n")
	got = string(RedactContent("events.jsonl", jsonlData))
	for _, leak := range []string{"line secret with spaces", "bearer-secret-value"} {
		if strings.Contains(got, leak) {
			t.Fatalf("jsonl content leaked %q:\n%s", leak, got)
		}
	}
	if strings.Count(got, "\n") != 2 || strings.Count(got, RedactionMarker) < 2 {
		t.Fatalf("jsonl content not preserved/redacted:\n%s", got)
	}
}

func TestRedactCoversQuotedDotenvLinesAndQuerySecrets(t *testing.T) {
	input := strings.Join([]string{
		`DATABASE_PASSWORD="secret value with spaces"`,
		`export API_TOKEN='quoted token value'`,
		`SERVICE_KEY=generic-key-secret`,
		`callback=https://example.test/cb?api_key=query-secret&visible=true`,
		`redirect=https://example.test/cb?service_key=query-key-secret&visible=true`,
		`openai_api_key_env=OPENAI_API_KEY`,
		`token_present=true`,
	}, "\n")
	got := RedactString(input)
	for _, leaked := range []string{"secret value with spaces", "quoted token value", "generic-key-secret", "query-secret", "query-key-secret"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q:\n%s", leaked, got)
		}
	}
	for _, want := range []string{"DATABASE_PASSWORD=[jj-omitted]", "API_TOKEN=[jj-omitted]", "SERVICE_KEY=[jj-omitted]", "api_key=[jj-omitted]&visible=true", "service_key=[jj-omitted]&visible=true", "openai_api_key_env=OPENAI_API_KEY", "token_present=true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted output missing %q:\n%s", want, got)
		}
	}
}

func TestRedactCoversCookiesAndRegisteredConfigSecrets(t *testing.T) {
	RegisterSensitiveConfigJSON([]byte(`{
		"openai_api_key": "jjrc-openai-secret",
		"nested": {"cookie": "jjrc-cookie-secret"},
		"github_token_env": "GITHUB_TOKEN",
		"token_present": true
	}`))
	input := strings.Join([]string{
		"Cookie: session=raw-cookie-secret; theme=dark",
		"Set-Cookie: sid=raw-set-cookie-secret; HttpOnly",
		"HTTP_COOKIE=env-cookie-secret",
		"configured jjrc-openai-secret and jjrc-cookie-secret",
		"github_token_env=GITHUB_TOKEN",
		"token_present=true",
	}, "\n")
	got := RedactString(input)
	for _, leaked := range []string{"raw-cookie-secret", "raw-set-cookie-secret", "env-cookie-secret", "jjrc-openai-secret", "jjrc-cookie-secret"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q:\n%s", leaked, got)
		}
	}
	for _, want := range []string{"Cookie: [jj-omitted]", "Set-Cookie: [jj-omitted]", "HTTP_COOKIE=[jj-omitted]", "github_token_env=GITHUB_TOKEN", "token_present=true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted output missing %q:\n%s", want, got)
		}
	}
}

func TestNewRedactorPreservesEnvNameReferencesAndLowInformationValues(t *testing.T) {
	redactor := NewRedactor([]string{
		"JJ_OPENAI_API_KEY_ENV=OPENAI_API_KEY",
		"JJ_GITHUB_TOKEN_ENV=GITHUB_TOKEN",
		"JJ_FLAG_SECRET=true",
		"JJ_REAL_SECRET=literal-secret-value",
	})
	got := redactor.Redact("env OPENAI_API_KEY GITHUB_TOKEN true literal-secret-value")
	for _, want := range []string{"OPENAI_API_KEY", "GITHUB_TOKEN", "true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redactor should preserve env name/low-info literal %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "literal-secret-value") || !strings.Contains(got, RedactionMarker) {
		t.Fatalf("redactor did not redact real configured secret:\n%s", got)
	}
}

func TestRegisterSensitiveLiteralsIgnoresLowInformationValues(t *testing.T) {
	RegisterSensitiveLiterals("true", "false", "null", "none", "literal-config-secret")

	got := RedactString("flags true false null none literal-config-secret")
	for _, want := range []string{"true", "false", "null", "none"} {
		if !strings.Contains(got, want) {
			t.Fatalf("low-information literal %q should remain visible:\n%s", want, got)
		}
	}
	if strings.Contains(got, "literal-config-secret") || !strings.Contains(got, RedactionMarker) {
		t.Fatalf("configured secret literal was not redacted:\n%s", got)
	}
}

func TestSanitizeCommandArgvRedactsSecretsAndRewritesKnownRoots(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".jj", "runs", "run-1")
	secret := "argv-secret-token-1234567890"
	pairedSecret := "paired-argv-secret-1234567890"
	t.Setenv("JJ_ARGV_SECRET_TOKEN", secret)

	got := SanitizeCommandArgv([]string{
		"codex",
		"--cd", root,
		"--output-last-message", filepath.Join(runDir, "codex", "summary.md"),
		"--token", secret,
		"--api-key", pairedSecret,
		"--client-secret=inline-secret-value",
		"OPENAI_API_KEY=inline-env-secret",
	}, CommandPathRoot{Path: runDir, Label: "[run]"}, CommandPathRoot{Path: root, Label: "[workspace]"})

	joined := strings.Join(got, " ")
	if strings.Contains(joined, root) || strings.Contains(joined, runDir) || strings.Contains(joined, secret) || strings.Contains(joined, pairedSecret) || strings.Contains(joined, "inline-secret-value") || strings.Contains(joined, "inline-env-secret") {
		t.Fatalf("sanitized argv leaked sensitive data:\n%#v", got)
	}
	if !strings.Contains(joined, "[workspace]") || !strings.Contains(joined, "[run]/codex/summary.md") || !strings.Contains(joined, "--client-secret=[jj-omitted]") || !strings.Contains(joined, "OPENAI_API_KEY=[jj-omitted]") || !strings.Contains(joined, RedactionMarker) {
		t.Fatalf("sanitized argv missing expected replacements:\n%#v", got)
	}
}

func TestSanitizeDisplayStringWithReportLabelsPathsAndCountsRedactions(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-secret.txt")
	secret := "display-report-secret"
	t.Setenv("JJ_DISPLAY_REPORT_TOKEN", secret)

	got, report := SanitizeDisplayStringWithReport(
		"workspace="+filepath.Join(root, "config.json")+" outside="+outside+" token="+secret,
		CommandPathRoot{Path: root, Label: "[workspace]"},
	)
	for _, leaked := range []string{root, filepath.ToSlash(root), outside, filepath.ToSlash(outside), secret} {
		if strings.Contains(got, leaked) {
			t.Fatalf("display string leaked %q:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "[workspace]/config.json") || !strings.Contains(got, "[path]") || !strings.Contains(got, RedactionMarker) {
		t.Fatalf("display string missing sanitized path/redaction evidence:\n%s", got)
	}
	if report.Kinds["path_label"] == 0 || report.Kinds["absolute_path"] == 0 || report.Kinds["configured_secret"] == 0 {
		t.Fatalf("display redaction report missing expected safe categories: %#v", report)
	}
}

func TestRedactTokenLikePreservesHexCommit(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	got := RedactString("commit " + sha)
	if strings.Contains(got, RedactionMarker) || !strings.Contains(got, sha) {
		t.Fatalf("hex commit should remain visible, got:\n%s", got)
	}
}

func TestRedactTokenLikeCoversAlphanumericHighEntropy(t *testing.T) {
	token := "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890QwErTy"
	got := RedactString("opaque " + token)
	if strings.Contains(got, token) || !strings.Contains(got, RedactionMarker) {
		t.Fatalf("high-entropy token was not redacted:\n%s", got)
	}
}

func TestFilterEnvRemovesSensitiveKeys(t *testing.T) {
	got := FilterEnv([]string{
		"PATH=/bin",
		"OPENAI_API_KEY=secret",
		"JJ_GITHUB_TOKEN=secret",
		"DB_PASSWORD=secret",
		"MY_CREDENTIAL=secret",
		"GIT_ASKPASS=/tmp/helper",
		"VISIBLE=value",
	})
	want := []string{"PATH=/bin", "VISIBLE=value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered env mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestFilterEnvRemovesSecretLookingValuesUnderBenignNames(t *testing.T) {
	got := FilterEnv([]string{
		"PATH=/bin",
		"VISIBLE=value",
		"BENIGN_OPENAI=sk-proj-envsecret1234567890",
		"BENIGN_TOKENLIKE=AbCdEfGhIjKlMnOpQrStUvWxYz1234567890QwErTy",
	})
	want := []string{"PATH=/bin", "VISIBLE=value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered env mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestNewSafeConfigOmitsSecretValuesAndKeepsPresenceMetadata(t *testing.T) {
	t.Setenv("JJ_SAFE_CONFIG_SECRET", "safe-config-secret-value")
	got := NewSafeConfig(SafeConfig{
		PlanningAgents:   2,
		OpenAIModel:      "gpt-test",
		CodexModel:       "safe-config-secret-value",
		CodexBin:         "/tmp/codex",
		TaskProposalMode: "security",
		ConfigFile:       "/tmp/.jjrc",
		OpenAIKeyEnv:     "OPENAI_API_KEY",
		OpenAIKeySet:     true,
		AllowNoGit:       true,
		SpecPath:         ".jj/spec.json",
		TaskPath:         ".jj/tasks.json",
	})

	if got.CodexModel != "" {
		t.Fatalf("safe config should omit redacted secret-like model values, got %#v", got)
	}
	if got.OpenAIModel != "gpt-test" || got.CodexBin != "/tmp/codex" || got.OpenAIKeyEnv != "OPENAI_API_KEY" || !got.OpenAIKeySet || !got.AllowNoGit {
		t.Fatalf("safe config dropped non-secret metadata: %#v", got)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal safe config: %v", err)
	}
	if strings.Contains(string(data), "safe-config-secret-value") || strings.Contains(string(data), RedactionMarker) {
		t.Fatalf("safe config JSON retained secret value or placeholder:\n%s", data)
	}
}

func TestSafeJoinRejectsTraversalAbsoluteEncodedAndHiddenPaths(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"../outside",
		"/etc/passwd",
		"C:/secret.txt",
		"docs/../manifest.json",
		"docs%2f..%2fmanifest.json",
		"docs/%2e%2e/manifest.json",
		"docs/.secret",
		"docs\\TASK.md",
	} {
		if _, err := SafeJoin(root, rel, PathPolicy{}); err == nil {
			t.Fatalf("expected %q to be rejected", rel)
		}
	}
}

func TestResolveCommandCWDValidatesAndSanitizesErrors(t *testing.T) {
	root := t.TempDir()
	resolved, err := ResolveCommandCWD(root)
	if err != nil {
		t.Fatalf("resolve command cwd: %v", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(root) {
		t.Fatalf("resolved cwd = %q, want %q", resolved, root)
	}

	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := ResolveCommandCWD(filePath); err == nil || strings.Contains(err.Error(), filePath) {
		t.Fatalf("expected sanitized non-directory error, got %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "unsafe-secret-token-1234567890")
	if _, err := ResolveCommandCWD(secretPath); err == nil || strings.Contains(err.Error(), secretPath) || strings.Contains(err.Error(), "unsafe-secret-token-1234567890") {
		t.Fatalf("expected sanitized missing cwd error, got %v", err)
	}

	if _, err := ResolveCommandCWD(filepath.Join(root, "docs%2f..%2fsecret")); err == nil || strings.Contains(err.Error(), "docs%2f") {
		t.Fatalf("expected sanitized encoded cwd rejection, got %v", err)
	}
}

func TestSafeJoinRejectsControlAndOverlongPaths(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"docs/bad\x1fpath.md",
		"docs/" + strings.Repeat("a", 256) + ".md",
		strings.Repeat("a", 4097),
	} {
		if _, err := SafeJoin(root, rel, PathPolicy{}); err == nil {
			t.Fatalf("expected %q to be rejected", rel)
		}
	}
}

func TestSafeJoinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "docs", "secret.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := SafeJoin(root, "docs/secret.md", PathPolicy{}); err == nil || !strings.Contains(err.Error(), "symlink outside workspace") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestSafeJoinAllowsHiddenWhenRequested(t *testing.T) {
	root := t.TempDir()
	got, err := SafeJoin(root, ".jj/runs/run", PathPolicy{AllowHidden: true})
	if err != nil {
		t.Fatalf("safe join hidden path: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "/.jj/runs/run") {
		t.Fatalf("unexpected hidden path: %s", got)
	}
}

func TestSafeJoinNoSymlinksRejectsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "target"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "target"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := SafeJoin(root, "link/file.txt", PathPolicy{}); err != nil {
		t.Fatalf("regular SafeJoin should allow symlinks that stay in root: %v", err)
	}
	if _, err := SafeJoinNoSymlinks(root, "link/file.txt", PathPolicy{}); err == nil || !strings.Contains(err.Error(), "symlinked path") {
		t.Fatalf("expected strict symlink rejection, got %v", err)
	}
}

func TestSafePathInRootRejectsOutsideAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, ".jj", "runs", "run", "codex", "events.jsonl")
	got, err := SafePathInRoot(root, inside, PathPolicy{AllowHidden: true})
	if err != nil {
		t.Fatalf("safe output path: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(inside) {
		t.Fatalf("unexpected output path: %s", got)
	}

	outside := filepath.Join(t.TempDir(), "events.jsonl")
	if _, err := SafePathInRoot(root, outside, PathPolicy{AllowHidden: true}); err == nil {
		t.Fatal("expected outside absolute path rejection")
	}
	encoded := filepath.Join(root, "docs%2f..%2fsecret.json")
	if _, err := SafePathInRoot(root, encoded, PathPolicy{AllowHidden: true}); err == nil {
		t.Fatal("expected encoded absolute path rejection")
	}

	linkParent := filepath.Join(root, ".jj", "runs", "run", "linked")
	if err := os.MkdirAll(filepath.Dir(linkParent), 0o755); err != nil {
		t.Fatalf("mkdir link parent: %v", err)
	}
	if err := os.Symlink(t.TempDir(), linkParent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := SafePathInRoot(root, filepath.Join(linkParent, "events.jsonl"), PathPolicy{AllowHidden: true}); err == nil || !strings.Contains(err.Error(), "symlink outside workspace") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}
