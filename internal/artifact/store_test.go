package artifact

import (
	"os"
	"path/filepath"
	"regexp"
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
