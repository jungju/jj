package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPlan(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(planPath, []byte("build jj\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	content, abs, err := LoadPlan("plan.md", dir)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	if content != "build jj\n" {
		t.Fatalf("unexpected content %q", content)
	}
	if abs != planPath {
		t.Fatalf("unexpected path %s", abs)
	}
}

func TestLoadPlanPrefersInvocationDirectoryOverTargetCWD(t *testing.T) {
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	target := filepath.Join(root, "playground", "workspace")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	planPath := filepath.Join(root, "playground", "plan.md")
	if err := os.WriteFile(planPath, []byte("from invocation cwd\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	content, abs, err := LoadPlan("playground/plan.md", target)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	if content != "from invocation cwd\n" {
		t.Fatalf("unexpected content %q", content)
	}
	if abs != planPath {
		t.Fatalf("expected %s, got %s", planPath, abs)
	}
}

func TestLoadPlanFallsBackToTargetCWD(t *testing.T) {
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	target := filepath.Join(root, "workspace")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	planPath := filepath.Join(target, "plan.md")
	if err := os.WriteFile(planPath, []byte("from target cwd\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	content, abs, err := LoadPlan("plan.md", target)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	if content != "from target cwd\n" {
		t.Fatalf("unexpected content %q", content)
	}
	if abs != planPath {
		t.Fatalf("expected %s, got %s", planPath, abs)
	}
}

func TestLoadPlanMissing(t *testing.T) {
	_, _, err := LoadPlan("missing.md", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "read plan file") {
		t.Fatalf("expected missing file error, got %v", err)
	}
}

func TestLoadPlanRejectsStdin(t *testing.T) {
	_, _, err := LoadPlan("-", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "stdin input is not supported") {
		t.Fatalf("expected stdin rejection, got %v", err)
	}
}
