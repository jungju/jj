package secrets

import (
	"strings"
	"testing"
)

func TestRedactRemovesSecretEnvValues(t *testing.T) {
	t.Setenv("JJ_TEST_SECRET", "super-secret-value")

	got := Redact("failed with super-secret-value")
	if strings.Contains(got, "super-secret-value") {
		t.Fatalf("secret value was not redacted: %q", got)
	}
}

func TestRedactRemovesOpenAIKeyCandidates(t *testing.T) {
	got := Redact("Incorrect API key provided: sk-proj-********************************pO0A.")
	if strings.Contains(got, "sk-proj") || strings.Contains(got, "pO0A") {
		t.Fatalf("OpenAI key candidate was not redacted: %q", got)
	}
}

func TestRedactRemovesBearerTokens(t *testing.T) {
	got := Redact("Authorization: Bearer abcdefghijklmnop")
	if strings.Contains(got, "abcdefghijklmnop") {
		t.Fatalf("bearer token was not redacted: %q", got)
	}
}

func TestRedactRemovesAuthorizationHeaderValues(t *testing.T) {
	got := Redact("Authorization: Basic dXNlcjpwYXNz\nnext: visible")
	if strings.Contains(got, "dXNlcjpwYXNz") || !strings.Contains(got, "next: visible") {
		t.Fatalf("authorization header was not redacted correctly: %q", got)
	}
}

func TestRedactRemovesSecretKeyValuePairs(t *testing.T) {
	input := `api_key: abcdefghijklmnop token="tokensecret" password=hunter2 {"api_key":"jsonsecret"}`
	got := Redact(input)
	for _, secret := range []string{"abcdefghijklmnop", "tokensecret", "hunter2", "jsonsecret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q was not redacted: %q", secret, got)
		}
	}
}

func TestRedactRemovesGitHubTokenCandidates(t *testing.T) {
	input := "clone failed with ghp_abcdefghijklmnop and github_pat_1234567890abcdef"
	got := Redact(input)
	for _, secret := range []string{"ghp_abcdefghijklmnop", "github_pat_1234567890abcdef"} {
		if strings.Contains(got, secret) {
			t.Fatalf("GitHub token %q was not redacted: %q", secret, got)
		}
	}
	if !strings.Contains(got, "[jj-omitted]") {
		t.Fatalf("expected GitHub redaction marker, got %q", got)
	}
}

func TestRedactRemovesURLCredentials(t *testing.T) {
	got := Redact("remote https://user:ghp_abcdefghijklmnop@github.com/org/repo.git failed")
	if strings.Contains(got, "user:") || strings.Contains(got, "ghp_abcdefghijklmnop") {
		t.Fatalf("URL credentials were not redacted: %q", got)
	}
	if !strings.Contains(got, "https://github.com/org/repo.git") {
		t.Fatalf("sanitized URL missing from redacted text: %q", got)
	}
}
