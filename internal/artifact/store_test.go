package artifact

import (
	"database/sql"
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

func TestStoreRejectsDocumentDatabaseOverwrite(t *testing.T) {
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if _, err := store.WriteString(legacyDocumentsDBRel, "not a database\n"); err == nil {
		t.Fatal("expected legacy document database artifact path to be reserved")
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

func TestStoreMirrorsWrittenArtifactsToSQLite(t *testing.T) {
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	secret := "sk-proj-documentmirror1234567890"
	if _, err := store.WriteString("planning/rule.json", `{"api_key":"`+secret+`","visible":"ok"}`+"\n"); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	dbPath, err := store.DocumentsDBPath()
	if err != nil {
		t.Fatalf("document db path: %v", err)
	}
	if want := filepath.Join(store.CWD, "data", "documents.sqlite3"); dbPath != want {
		t.Fatalf("document db should be workspace scoped: got %s want %s", dbPath, want)
	}
	if _, err := os.Stat(filepath.Join(store.RunDir, legacyDocumentsDBRel)); !os.IsNotExist(err) {
		t.Fatalf("legacy per-run document db should not exist, stat err=%v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("open document db: %v", err)
	}
	defer db.Close()

	var kind, mediaType string
	var content string
	var bytes, redacted int
	err = db.QueryRow(
		`SELECT kind, media_type, content, bytes, redacted FROM documents WHERE run_id = ? AND rel_path = ?`,
		store.RunID,
		"planning/rule.json",
	).Scan(&kind, &mediaType, &content, &bytes, &redacted)
	if err != nil {
		t.Fatalf("query mirrored document: %v", err)
	}
	if kind != "rule" || mediaType != "application/json" || redacted != 1 || bytes != len(content) {
		t.Fatalf("unexpected mirrored metadata kind=%q media=%q redacted=%d bytes=%d len=%d", kind, mediaType, redacted, bytes, len(content))
	}
	if strings.Contains(content, secret) || !strings.Contains(content, "[jj-omitted]") || !strings.Contains(content, `"visible": "ok"`) {
		t.Fatalf("mirrored document was not redacted:\n%s", content)
	}
}

func TestStoreRecordsExternalGeneratedFilesAndWorkspaceDocumentsToSQLite(t *testing.T) {
	store, err := NewStore(t.TempDir(), "run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	eventsPath, err := store.Path("codex/events.jsonl")
	if err != nil {
		t.Fatalf("events path: %v", err)
	}
	if err := AtomicWriteFile(eventsPath, []byte(`{"type":"log","message":"ok"}`+"\n"), PrivateFileMode); err != nil {
		t.Fatalf("write external artifact: %v", err)
	}
	if _, err := store.RecordFile("codex/events.jsonl"); err != nil {
		t.Fatalf("record external artifact: %v", err)
	}
	if err := store.SaveDocument(".jj/spec.json", []byte(`{"title":"Spec"}`+"\n")); err != nil {
		t.Fatalf("save workspace spec: %v", err)
	}

	dbPath, err := store.DocumentsDBPath()
	if err != nil {
		t.Fatalf("document db path: %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("open document db: %v", err)
	}
	defer db.Close()

	kinds := map[string]string{}
	rows, err := db.Query(`SELECT rel_path, kind FROM documents WHERE run_id = ?`, store.RunID)
	if err != nil {
		t.Fatalf("query documents: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rel, kind string
		if err := rows.Scan(&rel, &kind); err != nil {
			t.Fatalf("scan document: %v", err)
		}
		kinds[rel] = kind
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate documents: %v", err)
	}
	if kinds["codex/events.jsonl"] != "log" {
		t.Fatalf("expected codex events to be stored as log, got %#v", kinds)
	}
	if kinds[".jj/spec.json"] != "spec" {
		t.Fatalf("expected workspace spec to be stored, got %#v", kinds)
	}
}

func TestStoreImportsExistingJJFilesToWorkspaceSQLite(t *testing.T) {
	root := t.TempDir()
	runID := "20260425-120000-existing"
	writeTestFile(t, filepath.Join(root, ".jj", "spec.json"), `{"title":"Spec"}`+"\n")
	writeTestFile(t, filepath.Join(root, ".jj", "tasks.json"), `{"tasks":[]}`+"\n")
	writeTestFile(t, filepath.Join(root, ".jj", "next-intent.md"), "Ship the next thing.\n")
	writeTestFile(t, filepath.Join(root, ".jj", "autopilot-logs", "autopilot-20260428-160202.log"), "autopilot log\n")
	writeTestFile(t, filepath.Join(root, ".jj", "runs", runID, "manifest.json"), `{"run_id":"`+runID+`"}`+"\n")
	writeTestFile(t, filepath.Join(root, ".jj", "runs", runID, "input.md"), "input\n")

	store, err := NewStore(root, "new-run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}

	dbPath, err := store.DocumentsDBPath()
	if err != nil {
		t.Fatalf("document db path: %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("open document db: %v", err)
	}
	defer db.Close()

	got := map[string]string{}
	rows, err := db.Query(`SELECT run_id, rel_path FROM documents`)
	if err != nil {
		t.Fatalf("query documents: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID, rel string
		if err := rows.Scan(&runID, &rel); err != nil {
			t.Fatalf("scan document: %v", err)
		}
		got[runID+" "+rel] = runID
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate documents: %v", err)
	}

	for _, want := range []string{
		workspaceDocumentsRunID + " .jj/spec.json",
		workspaceDocumentsRunID + " .jj/tasks.json",
		workspaceDocumentsRunID + " .jj/next-intent.md",
		"autopilot-20260428-160202 .jj/autopilot-logs/autopilot-20260428-160202.log",
		runID + " manifest.json",
		runID + " input.md",
	} {
		if _, ok := got[want]; !ok {
			t.Fatalf("missing imported document %q in %#v", want, got)
		}
	}
	for key := range got {
		if strings.Contains(key, "documents.sqlite3") {
			t.Fatalf("document database imported itself: %#v", got)
		}
	}
}

func TestStoreMigratesLegacyRunDocumentDatabasesToWorkspaceSQLite(t *testing.T) {
	root := t.TempDir()
	legacyRunID := "legacy-run"
	legacyDir := filepath.Join(root, ".jj", "runs", legacyRunID)
	if err := os.MkdirAll(legacyDir, PrivateDirMode); err != nil {
		t.Fatalf("mkdir legacy run: %v", err)
	}
	legacyPath := filepath.Join(legacyDir, legacyDocumentsDBRel)
	legacyDB, err := sql.Open("sqlite", sqliteDSN(legacyPath))
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacyDB.Exec(documentStoreSchema); err != nil {
		t.Fatalf("init legacy db: %v", err)
	}
	if _, err := legacyDB.Exec(
		`INSERT INTO documents (run_id, rel_path, kind, media_type, content, sha256, bytes, redacted, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		legacyRunID,
		"codex/summary.md",
		"codex",
		"text/markdown; charset=utf-8",
		[]byte("legacy summary\n"),
		"abc123",
		len("legacy summary\n"),
		1,
		"2026-04-25T00:00:00Z",
	); err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(root, "new-run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy document db should be deleted after migration, stat err=%v", err)
	}

	dbPath, err := store.DocumentsDBPath()
	if err != nil {
		t.Fatalf("document db path: %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("open document db: %v", err)
	}
	defer db.Close()

	var content string
	err = db.QueryRow(`SELECT content FROM documents WHERE run_id = ? AND rel_path = ?`, legacyRunID, "codex/summary.md").Scan(&content)
	if err != nil {
		t.Fatalf("query migrated document: %v", err)
	}
	if content != "legacy summary\n" {
		t.Fatalf("unexpected migrated content %q", content)
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
	for _, dir := range []string{filepath.Join(store.CWD, ".jj"), filepath.Join(store.CWD, ".jj", "runs"), store.RunDir, filepath.Join(store.RunDir, "planning")} {
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
	dbPath, err := store.DocumentsDBPath()
	if err != nil {
		t.Fatalf("document db path: %v", err)
	}
	info, err = os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat document db: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("document db should not be group/world accessible, mode=%#o", info.Mode().Perm())
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

func TestValidateRunIDRejectsSecretLookingValuesWithoutEcho(t *testing.T) {
	for _, runID := range []string{
		"sk-proj-runidsecret1234567890",
		"github_pat_1234567890abcdefghijklmnopqrstuvwxyz",
	} {
		err := ValidateRunID(runID)
		if err == nil {
			t.Fatalf("expected secret-looking run id %q to fail validation", runID)
		}
		if strings.Contains(err.Error(), runID) || strings.Contains(err.Error(), "sk-proj") || strings.Contains(err.Error(), "github_pat") || strings.Contains(err.Error(), "[jj-omitted]") {
			t.Fatalf("run id validation error leaked unsafe value: %v", err)
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

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), PrivateDirMode); err != nil {
		t.Fatalf("mkdir test file parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), PrivateFileMode); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}
