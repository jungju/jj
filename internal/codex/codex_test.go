package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	args := BuildArgs(Request{
		CWD:               "/tmp/repo",
		Model:             "gpt-5.4-mini",
		OutputLastMessage: "/tmp/run/codex-summary.md",
		AllowNoGit:        true,
	})
	want := []string{
		"exec",
		"--cd", "/tmp/repo",
		"--json",
		"--output-last-message", "/tmp/run/codex-summary.md",
		"--sandbox", "workspace-write",
		"--full-auto",
		"--model", "gpt-5.4-mini",
		"--skip-git-repo-check",
		"-",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch\nwant: %#v\n got: %#v", want, args)
	}
}

func TestScrubImplementationEnvRemovesGitHubCredentials(t *testing.T) {
	got := scrubImplementationEnv([]string{
		"PATH=/bin",
		"JJ_GITHUB_TOKEN=secret",
		"GITHUB_TOKEN=secret",
		"GH_TOKEN=secret",
		"JJ_GITHUB_ASKPASS_TOKEN=secret",
		"GIT_ASKPASS=/tmp/helper",
		"OPENAI_API_KEY=removed",
		"MY_CREDENTIAL=removed",
	})
	if !reflect.DeepEqual(got, []string{"PATH=/bin"}) {
		t.Fatalf("unexpected scrubbed env: %#v", got)
	}
}

func TestSafeOutputPathRejectsOutsideAndSymlinkEscape(t *testing.T) {
	cwd := t.TempDir()
	inside := filepath.Join(cwd, ".jj", "runs", "run", "codex", "events.jsonl")
	got, err := safeOutputPath(cwd, inside)
	if err != nil {
		t.Fatalf("safe output path: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(inside) {
		t.Fatalf("unexpected safe path: %s", got)
	}

	if _, err := safeOutputPath(cwd, filepath.Join(t.TempDir(), "events.jsonl")); err == nil {
		t.Fatal("expected outside output path to be rejected")
	}
	if _, err := safeOutputPath(cwd, filepath.Join(cwd, "codex", "events.jsonl")); err == nil {
		t.Fatal("expected non-run-root output path to be rejected")
	}
	if _, err := safeOutputPath(cwd, filepath.Join(cwd, ".jj", "runs", "run", "codex", ".env")); err == nil {
		t.Fatal("expected hidden output path to be rejected")
	}
	if _, err := safeOutputPath(cwd, "relative/events.jsonl"); err == nil {
		t.Fatal("expected relative output path to be rejected")
	}

	link := filepath.Join(cwd, ".jj", "runs", "run", "linked")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir link parent: %v", err)
	}
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := safeOutputPath(cwd, filepath.Join(link, "summary.md")); err == nil || !strings.Contains(err.Error(), "symlinked path") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	insideTarget := filepath.Join(cwd, "inside-target")
	if err := os.MkdirAll(insideTarget, 0o755); err != nil {
		t.Fatalf("mkdir inside target: %v", err)
	}
	insideLink := filepath.Join(cwd, ".jj", "runs", "run", "inside-link")
	if err := os.Symlink(insideTarget, insideLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := safeOutputPath(cwd, filepath.Join(insideLink, "summary.md")); err == nil || !strings.Contains(err.Error(), "symlinked path") {
		t.Fatalf("expected internal symlink rejection, got %v", err)
	}
}

func TestRunnerPublishesOnlyRedactedCodexArtifacts(t *testing.T) {
	cwd := t.TempDir()
	secret := "sk-proj-codexrunnersecret1234567890"
	fakeCodex := filepath.Join(cwd, "fake-codex.sh")
	script := `#!/bin/sh
set -eu
last_message=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message)
      shift
      last_message="$1"
      ;;
  esac
  shift || true
done
cat >/dev/null
printf '%s\n' '{"type":"done","api_key":"` + secret + `"}'
printf '%s\n' 'Authorization: Bearer ` + secret + `' >&2
printf '%s\n' 'summary token=` + secret + `' > "$last_message"
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	eventsPath := filepath.Join(cwd, ".jj", "runs", "run", "codex", "events.jsonl")
	summaryPath := filepath.Join(cwd, ".jj", "runs", "run", "codex", "summary.md")
	result, err := (Runner{}).Run(context.Background(), Request{
		Bin:               fakeCodex,
		CWD:               cwd,
		Prompt:            "do work",
		EventsPath:        eventsPath,
		OutputLastMessage: summaryPath,
	})
	if err != nil {
		t.Fatalf("run fake codex: %v", err)
	}
	if strings.Contains(result.Summary, secret) || !strings.Contains(result.Summary, "[jj-omitted]") {
		t.Fatalf("result summary was not redacted: %q", result.Summary)
	}
	for _, path := range []string{eventsPath, summaryPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), secret) || !strings.Contains(string(data), "[jj-omitted]") {
			t.Fatalf("%s was not redacted:\n%s", path, data)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(eventsPath))
	if err != nil {
		t.Fatalf("read codex dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".raw-") {
			t.Fatalf("quarantine file was not removed: %s", entry.Name())
		}
	}
}

func TestRunnerRejectsInvalidCWDWithoutLeakingPath(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "unsafe-secret-token-1234567890")
	_, err := (Runner{}).Run(context.Background(), Request{
		Bin:               "codex",
		CWD:               secretPath,
		Prompt:            "do work",
		EventsPath:        filepath.Join(secretPath, ".jj", "runs", "run", "codex", "events.jsonl"),
		OutputLastMessage: filepath.Join(secretPath, ".jj", "runs", "run", "codex", "summary.md"),
	})
	if err == nil {
		t.Fatal("expected invalid cwd rejection")
	}
	if strings.Contains(err.Error(), secretPath) || strings.Contains(err.Error(), "unsafe-secret-token-1234567890") {
		t.Fatalf("invalid cwd error leaked path: %v", err)
	}
}
