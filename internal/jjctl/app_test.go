package jjctl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAppMigratesAndEncryptsSecrets(t *testing.T) {
	ctx := context.Background()
	app, err := OpenApp(ctx, AppOptions{Home: t.TempDir()})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	defer app.Close()

	for _, table := range []string{"users", "repositories", "k8s_credentials", "deployments", "audit_logs"} {
		var name string
		if err := app.DB.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name); err != nil {
			t.Fatalf("expected table %s: %v", table, err)
		}
	}

	ref, err := app.Secrets.Save("test", []byte("super-secret-token"), app.timestamp())
	if err != nil {
		t.Fatalf("save secret: %v", err)
	}
	got, err := app.Secrets.Load(ref)
	if err != nil {
		t.Fatalf("load secret: %v", err)
	}
	if string(got) != "super-secret-token" {
		t.Fatalf("unexpected secret roundtrip: %q", string(got))
	}
	storeData, err := os.ReadFile(app.Paths.SecretStorePath)
	if err != nil {
		t.Fatalf("read secret store: %v", err)
	}
	if strings.Contains(string(storeData), "super-secret-token") {
		t.Fatal("secret store contains plaintext token")
	}
}

func TestSaveGitHubLoginStoresTokenReferenceOnly(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ghp_plaintext" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         123,
			"login":      "octocat",
			"name":       "Octo Cat",
			"avatar_url": "https://example.test/avatar.png",
		})
	}))
	defer server.Close()
	t.Setenv("JJ_GITHUB_API_URL", server.URL)

	app, err := OpenApp(ctx, AppOptions{Home: t.TempDir()})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	defer app.Close()

	user, err := app.SaveGitHubLogin(ctx, "ghp_plaintext", []string{"repo", "read:user"})
	if err != nil {
		t.Fatalf("save github login: %v", err)
	}
	if user.Login != "octocat" {
		t.Fatalf("unexpected login: %s", user.Login)
	}
	var ref string
	if err := app.DB.QueryRowContext(ctx, "SELECT access_token_ref FROM github_accounts").Scan(&ref); err != nil {
		t.Fatalf("read token ref: %v", err)
	}
	if strings.Contains(ref, "ghp_plaintext") || !strings.HasPrefix(ref, "local-aesgcm:") {
		t.Fatalf("token was not stored as encrypted ref: %q", ref)
	}
	dbData, err := os.ReadFile(app.Paths.DBPath)
	if err != nil {
		t.Fatalf("read sqlite file: %v", err)
	}
	if strings.Contains(string(dbData), "ghp_plaintext") {
		t.Fatal("sqlite database contains plaintext token")
	}
}

func TestInitDocsPreservesExistingFilesAndMergesAgents(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# Existing\n\nKeep me.\n"), 0o644); err != nil {
		t.Fatalf("write agents: %v", err)
	}
	existingPath := filepath.Join(repo, "docs", "README.md")
	if err := os.MkdirAll(filepath.Dir(existingPath), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(existingPath, []byte("existing docs\n"), 0o644); err != nil {
		t.Fatalf("write docs readme: %v", err)
	}

	result, err := initDocs(repo, "owner/repo", false)
	if err != nil {
		t.Fatalf("init docs: %v", err)
	}
	if result.Skipped == 0 || result.Created == 0 {
		t.Fatalf("expected created and skipped files: %#v", result)
	}
	data, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("read docs readme: %v", err)
	}
	if string(data) != "existing docs\n" {
		t.Fatalf("existing file was overwritten: %q", string(data))
	}
	agents, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read agents: %v", err)
	}
	if !strings.Contains(string(agents), "Keep me.") || !strings.Contains(string(agents), "This repository is managed by jj.") {
		t.Fatalf("AGENTS.md was not merged:\n%s", string(agents))
	}
	if !fileExists(filepath.Join(repo, ".jj", "docs-manifest.json")) {
		t.Fatal("docs manifest was not created")
	}
}

func TestDefaultDeployConfigUsesTarget(t *testing.T) {
	cfg := defaultDeployConfig("owner/api-server", targetRecord{
		Name:        "dev",
		PoolName:    "personal-dev",
		Environment: "dev",
		Namespace:   "jj-dev",
		Strategy:    "build-push-apply",
	})
	if cfg.Repo.FullName != "owner/api-server" || cfg.Deploy.App != "api-server" {
		t.Fatalf("unexpected repo/app: %#v", cfg)
	}
	if len(cfg.Deploy.Targets) != 1 {
		t.Fatalf("expected one target: %#v", cfg.Deploy.Targets)
	}
	target := cfg.Deploy.Targets[0]
	if target.Pool != "personal-dev" || target.Namespace != "jj-dev" || target.Image.Repository != "ghcr.io/owner/api-server" {
		t.Fatalf("unexpected deploy target: %#v", target)
	}
}
