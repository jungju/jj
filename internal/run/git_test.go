package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectGitUnavailableOutsideRepo(t *testing.T) {
	state, err := InspectGit(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("inspect git: %v", err)
	}
	if state.Available {
		t.Fatal("expected temp dir outside git repo to be unavailable")
	}
}

func TestCaptureGitDiff(t *testing.T) {
	dir := initGit(t)
	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, dir, "add", "tracked.txt")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	state, err := InspectGit(context.Background(), dir)
	if err != nil {
		t.Fatalf("inspect git: %v", err)
	}
	if !state.Available {
		t.Fatal("expected git repo to be available")
	}
	if state.Root == "" || state.Branch == "" || state.Head == "" {
		t.Fatalf("expected git metadata, got %#v", state)
	}
	diff, err := CaptureGitDiff(context.Background(), dir, true)
	if err != nil {
		t.Fatalf("capture diff: %v", err)
	}
	if !strings.Contains(diff.Status, "tracked.txt") {
		t.Fatalf("status missing file: %#v", diff)
	}
	if !strings.Contains(diff.Markdown(), "git diff --stat") {
		t.Fatalf("markdown missing sections: %s", diff.Markdown())
	}
}

func TestCaptureUntrackedEvidenceSkipsDeletedOutsideAndInternalPaths(t *testing.T) {
	dir := t.TempDir()
	evidence, err := CaptureUntrackedEvidence(context.Background(), dir, true, fakeGitRunner{
		outputs: map[string]string{
			"ls-files --others --exclude-standard -z": "gone.txt\x00../outside.txt\x00.jj/runs/current/input.md\x00",
		},
	})
	if err != nil {
		t.Fatalf("capture untracked evidence: %v", err)
	}
	if len(evidence.Files) != 1 || len(evidence.Captured) != 0 || len(evidence.Skipped) != 3 {
		t.Fatalf("unexpected evidence: %#v", evidence)
	}
	for _, want := range []string{"deleted during capture", "outside workspace", "jj internal artifact path"} {
		if !strings.Contains(evidence.Summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, evidence.Summary)
		}
	}
	if strings.TrimSpace(evidence.Patch) != "" {
		t.Fatalf("unsafe files should not be inlined:\n%s", evidence.Patch)
	}
}

func TestCaptureUntrackedEvidenceUnavailableWithoutGit(t *testing.T) {
	evidence, err := CaptureUntrackedEvidence(context.Background(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("capture untracked evidence: %v", err)
	}
	if evidence.Available || !strings.Contains(evidence.Summary, "unavailable") {
		t.Fatalf("expected unavailable evidence, got %#v", evidence)
	}
}

func initGit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	path := filepath.Join(dir, "scripts", "validate.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir validation script: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\nprintf 'ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("write validation script: %v", err)
	}
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}
