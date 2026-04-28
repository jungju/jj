package artifact

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewRunIDAndStorePath(t *testing.T) {
	runID := NewRunID(time.Date(2026, 4, 25, 1, 2, 3, 0, time.UTC))
	if !regexp.MustCompile(`^20260425-010203-[A-Za-z0-9]+`).MatchString(runID) {
		t.Fatalf("unexpected run id %q", runID)
	}
	if err := ValidateRunID(runID); err != nil {
		t.Fatalf("run id should validate: %v", err)
	}

	store, err := NewStore(t.TempDir(), runID)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	path, err := store.WriteString("planning/draft.json", "{}\n")
	if err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("artifact missing: %v", err)
	}
	if filepath.Base(filepath.Dir(path)) != "planning" {
		t.Fatalf("artifact path did not preserve relative dir: %s", path)
	}
}

func TestStoreRejectsEscapingPath(t *testing.T) {
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := store.Path("../outside"); err == nil {
		t.Fatal("expected escaping path to fail")
	}
	if _, err := store.Path("docs%2f..%2foutside"); err == nil {
		t.Fatal("expected encoded escaping path to fail")
	}
	if _, err := store.Path("docs/.secret"); err == nil {
		t.Fatal("expected hidden artifact path to fail")
	}
}

func TestStoreRejectsEscapingPathWithoutEchoingValue(t *testing.T) {
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	secretPath := "docs/unsafe-secret-token-1234567890/../manifest.json"
	if _, err := store.WriteString(secretPath, "secret\n"); err == nil {
		t.Fatal("expected unsafe artifact path to fail")
	} else if strings.Contains(err.Error(), "unsafe-secret-token-1234567890") || strings.Contains(err.Error(), secretPath) {
		t.Fatalf("unsafe artifact error leaked path value: %v", err)
	}
}

func TestStoreWriteFileRedactsJSONByKey(t *testing.T) {
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	path, err := store.WriteFile("planning/raw.json", []byte(`{"clientSecret":"secret value with spaces","visible":"ok"}`))
	if err != nil {
		t.Fatalf("write json artifact: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json artifact: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "secret value with spaces") || !strings.Contains(got, "[jj-omitted]") || !strings.Contains(got, `"visible": "ok"`) {
		t.Fatalf("json artifact was not redacted by key:\n%s", got)
	}
	if store.RedactionCount() == 0 {
		t.Fatal("expected store redaction count to be recorded")
	}
	if kinds := strings.Join(store.RedactionKinds(), ","); !strings.Contains(kinds, "sensitive_json_key") {
		t.Fatalf("expected sensitive_json_key redaction kind, got %q", kinds)
	}
}

func TestStoreUsesPrivateRunPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not portable on Windows")
	}
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	path, err := store.WriteString("planning/draft.txt", "safe\n")
	if err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	for _, dir := range []string{filepath.Join(store.CWD, ".jj", "runs"), store.RunDir, filepath.Join(store.RunDir, "planning")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("directory %s should not be group/world accessible, mode=%#o", dir, info.Mode().Perm())
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("artifact file should not be group/world accessible, mode=%#o", info.Mode().Perm())
	}
}

func TestStoreRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root, "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	outside := t.TempDir()
	if err := os.Remove(filepath.Join(store.RunDir, "planning")); err != nil {
		t.Fatalf("remove planning dir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(store.RunDir, "planning")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.WriteString("planning/draft.json", "{}\n"); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
}

func TestStoreRejectsInternalSymlinkEscapeFromRunRoot(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root, "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	insideWorkspace := filepath.Join(root, "workspace-target")
	if err := os.MkdirAll(insideWorkspace, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.Remove(filepath.Join(store.RunDir, "planning")); err != nil {
		t.Fatalf("remove planning dir: %v", err)
	}
	if err := os.Symlink(insideWorkspace, filepath.Join(store.RunDir, "planning")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.WriteString("planning/draft.json", "{}\n"); err == nil {
		t.Fatal("expected internal run-root symlink to fail")
	}
}

func TestStoreRejectsRunRootSymlinkAfterInit(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root, "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	target := filepath.Join(root, "run-target")
	if err := os.MkdirAll(filepath.Join(target, "planning"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.RemoveAll(store.RunDir); err != nil {
		t.Fatalf("remove run dir: %v", err)
	}
	if err := os.Symlink(target, store.RunDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.WriteString("planning/draft.json", "{}\n"); err == nil {
		t.Fatal("expected symlinked run root to fail")
	}
}

func TestStoreInitRejectsExistingRunDir(t *testing.T) {
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := store.Init(); err == nil {
		t.Fatal("expected existing run directory to fail")
	}
}

func TestValidateRunIDRejectsPathTraversal(t *testing.T) {
	for _, runID := range []string{".", "..", "../foo", "/foo", "foo/bar", `foo\bar`} {
		if err := ValidateRunID(runID); err == nil {
			t.Fatalf("expected %q to fail validation", runID)
		}
	}
}

func TestValidateArtifactNamePolicy(t *testing.T) {
	valid := []string{"manifest", "planning_merge", "validation-001.stdout", "snapshot_spec_after"}
	for _, name := range valid {
		if err := ValidateArtifactName(name); err != nil {
			t.Fatalf("expected artifact name %q to validate: %v", name, err)
		}
	}

	invalid := []string{
		"",
		".hidden",
		"..",
		"../manifest",
		"planning/merge",
		`planning\merge`,
		"bad%2fmerge",
		"bad\x1fname",
		strings.Repeat("a", 129),
	}
	for _, name := range invalid {
		if err := ValidateArtifactName(name); err == nil {
			t.Fatalf("expected artifact name %q to fail validation", name)
		}
	}
}
