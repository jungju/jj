package artifact

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jungju/jj/internal/security"
	_ "modernc.org/sqlite"
)

const (
	DocumentsDBRel          = ".jj/documents.sqlite3"
	legacyDocumentsDBRel    = "documents.sqlite3"
	workspaceDocumentsRunID = "workspace"
)

// InitDocumentStore prepares the workspace SQLite document mirror. File
// artifacts remain the compatibility surface, while this database stores the
// same redacted generated documents for structured local retention and search.
func (s Store) InitDocumentStore() error {
	dbPath, err := s.DocumentsDBPath()
	if err != nil {
		return err
	}
	if err := ensureDocumentDBDir(dbPath); err != nil {
		return err
	}
	if err := s.withDocumentDB(func(db *sql.DB) error {
		if err := s.migrateLegacyDocumentStores(db); err != nil {
			return err
		}
		return s.importJJDocuments(db)
	}); err != nil {
		return err
	}
	return os.Chmod(dbPath, PrivateFileMode)
}

func (s Store) DocumentsDBPath() (string, error) {
	return security.SafeJoinNoSymlinks(s.CWD, DocumentsDBRel, security.PathPolicy{AllowHidden: true})
}

// SaveDocument records a redacted generated document in the workspace SQLite
// mirror. It accepts run artifact paths and workspace state paths such as
// .jj/spec.json as logical document names.
func (s Store) SaveDocument(rel string, data []byte) error {
	clean, err := cleanDocumentRel(rel)
	if err != nil {
		return err
	}
	if isDocumentDBFileRel(clean) {
		return nil
	}
	sum := sha256.Sum256(data)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	content := append([]byte{}, data...)
	doc := documentRecord{
		RunID:     s.RunID,
		RelPath:   clean,
		Kind:      documentKind(clean),
		MediaType: documentMediaType(clean),
		Content:   content,
		SHA256:    hex.EncodeToString(sum[:]),
		Bytes:     int64(len(data)),
		Redacted:  1,
		UpdatedAt: now,
	}
	return s.withDocumentDB(func(db *sql.DB) error {
		return upsertDocument(context.Background(), db, doc)
	})
}

// RecordFile imports an already-redacted run artifact into the SQLite mirror.
func (s Store) RecordFile(rel string) (string, error) {
	path, err := s.Path(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := s.SaveDocument(rel, data); err != nil {
		return "", err
	}
	return path, nil
}

type documentRecord struct {
	RunID     string
	RelPath   string
	Kind      string
	MediaType string
	Content   []byte
	SHA256    string
	Bytes     int64
	Redacted  int
	UpdatedAt string
}

const documentStoreSchema = `
PRAGMA user_version = 1;

CREATE TABLE IF NOT EXISTS documents (
	run_id TEXT NOT NULL,
	rel_path TEXT NOT NULL,
	kind TEXT NOT NULL,
	media_type TEXT NOT NULL,
	content BLOB NOT NULL,
	sha256 TEXT NOT NULL,
	bytes INTEGER NOT NULL,
	redacted INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (run_id, rel_path)
);

CREATE INDEX IF NOT EXISTS documents_run_kind_idx ON documents(run_id, kind, rel_path);
CREATE INDEX IF NOT EXISTS documents_kind_idx ON documents(kind, run_id, rel_path);
CREATE INDEX IF NOT EXISTS documents_rel_path_idx ON documents(rel_path);
`

func (s Store) withDocumentDB(fn func(*sql.DB) error) error {
	if fn == nil {
		return nil
	}
	dbPath, err := s.DocumentsDBPath()
	if err != nil {
		return err
	}
	if err := ensureDocumentDBDir(dbPath); err != nil {
		return err
	}
	if s.dbMu != nil {
		s.dbMu.Lock()
		defer s.dbMu.Unlock()
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), `PRAGMA busy_timeout = 5000`); err != nil {
		return err
	}
	if _, err := db.ExecContext(context.Background(), documentStoreSchema); err != nil {
		return err
	}
	if err := fn(db); err != nil {
		return err
	}
	if err := os.Chmod(dbPath, PrivateFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type documentExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func upsertDocument(ctx context.Context, execer documentExecer, doc documentRecord) error {
	if doc.Redacted == 0 {
		doc.Redacted = 1
	}
	_, err := execer.ExecContext(
		ctx,
		`INSERT INTO documents (
			run_id, rel_path, kind, media_type, content, sha256, bytes, redacted, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, rel_path) DO UPDATE SET
			kind = excluded.kind,
			media_type = excluded.media_type,
			content = excluded.content,
			sha256 = excluded.sha256,
			bytes = excluded.bytes,
			redacted = excluded.redacted,
			updated_at = excluded.updated_at`,
		doc.RunID,
		doc.RelPath,
		doc.Kind,
		doc.MediaType,
		doc.Content,
		doc.SHA256,
		doc.Bytes,
		doc.Redacted,
		doc.UpdatedAt,
	)
	return err
}

func ensureDocumentDBDir(dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), PrivateDirMode); err != nil {
		return err
	}
	return ensurePrivateDir(filepath.Dir(dbPath))
}

func (s Store) importJJDocuments(target *sql.DB) error {
	jjDir, err := security.SafeJoinNoSymlinks(s.CWD, ".jj", security.PathPolicy{AllowHidden: true})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(jjDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}

	ctx := context.Background()
	importedAt, err := documentImportTimestamps(ctx, target)
	if err != nil {
		return err
	}
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = filepath.WalkDir(jjDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == jjDir {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(s.CWD, path)
		if err != nil {
			return err
		}
		doc, ok, err := s.documentRecordForWorkspaceRel(filepath.ToSlash(rel))
		if err != nil || !ok {
			return err
		}
		updatedAt := info.ModTime().UTC().Format(time.RFC3339Nano)
		if importedAt[documentKey(doc.RunID, doc.RelPath)] == updatedAt {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		data, report := security.RedactContentWithReport(doc.RelPath, data)
		s.RecordRedactionReport(report)
		sum := sha256.Sum256(data)
		doc.Content = append([]byte{}, data...)
		doc.SHA256 = hex.EncodeToString(sum[:])
		doc.Bytes = int64(len(data))
		doc.Redacted = 1
		doc.UpdatedAt = updatedAt
		return upsertDocument(ctx, tx, doc)
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

func documentImportTimestamps(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT run_id, rel_path, updated_at FROM documents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var runID, relPath, updatedAt string
		if err := rows.Scan(&runID, &relPath, &updatedAt); err != nil {
			return nil, err
		}
		out[documentKey(runID, relPath)] = updatedAt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func documentKey(runID, relPath string) string {
	return runID + "\x00" + relPath
}

func (s Store) documentRecordForWorkspaceRel(workspaceRel string) (documentRecord, bool, error) {
	clean, err := cleanDocumentRel(workspaceRel)
	if err != nil {
		return documentRecord{}, false, err
	}
	if isDocumentDBFileRel(clean) {
		return documentRecord{}, false, nil
	}
	parts := strings.Split(clean, "/")
	if len(parts) >= 4 && parts[0] == ".jj" && parts[1] == "runs" {
		runID := parts[2]
		if err := ValidateRunID(runID); err != nil {
			return documentRecord{}, false, nil
		}
		artifactRel := strings.Join(parts[3:], "/")
		artifactRel, err = cleanDocumentRel(artifactRel)
		if err != nil {
			return documentRecord{}, false, nil
		}
		if isDocumentDBFileRel(artifactRel) {
			return documentRecord{}, false, nil
		}
		return newDocumentRecord(runID, artifactRel), true, nil
	}

	runID := workspaceDocumentsRunID
	if len(parts) >= 3 && parts[0] == ".jj" && parts[1] == "autopilot-logs" {
		candidate := strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))
		if err := ValidateRunID(candidate); err == nil {
			runID = candidate
		}
	}
	return newDocumentRecord(runID, clean), true, nil
}

func newDocumentRecord(runID, relPath string) documentRecord {
	return documentRecord{
		RunID:     runID,
		RelPath:   relPath,
		Kind:      documentKind(relPath),
		MediaType: documentMediaType(relPath),
	}
}

func (s Store) migrateLegacyDocumentStores(target *sql.DB) error {
	runsDir, err := security.SafeJoinNoSymlinks(s.CWD, ".jj/runs", security.PathPolicy{AllowHidden: true})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(runsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		if err := ValidateRunID(runID); err != nil {
			continue
		}
		legacyPath, err := security.SafeJoinNoSymlinks(runsDir, filepath.ToSlash(filepath.Join(runID, legacyDocumentsDBRel)), security.PathPolicy{})
		if err != nil {
			return err
		}
		info, err := os.Lstat(legacyPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			return security.ErrSymlinkPath
		}
		if err := migrateLegacyDocumentStore(target, legacyPath, runID); err != nil {
			return fmt.Errorf("migrate legacy document store: %w", err)
		}
		if err := removeLegacyDocumentStoreFiles(legacyPath); err != nil {
			return err
		}
	}
	return nil
}

func migrateLegacyDocumentStore(target *sql.DB, legacyPath, fallbackRunID string) error {
	ctx := context.Background()
	legacy, err := sql.Open("sqlite", sqliteDSN(legacyPath))
	if err != nil {
		return err
	}
	defer legacy.Close()
	legacy.SetMaxOpenConns(1)
	if _, err := legacy.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return err
	}
	rows, err := legacy.QueryContext(ctx, `SELECT run_id, rel_path, kind, media_type, content, sha256, bytes, redacted, updated_at FROM documents`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for rows.Next() {
		var doc documentRecord
		if err := rows.Scan(&doc.RunID, &doc.RelPath, &doc.Kind, &doc.MediaType, &doc.Content, &doc.SHA256, &doc.Bytes, &doc.Redacted, &doc.UpdatedAt); err != nil {
			return err
		}
		if strings.TrimSpace(doc.RunID) == "" {
			doc.RunID = fallbackRunID
		}
		if err := upsertDocument(ctx, tx, doc); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func removeLegacyDocumentStoreFiles(legacyPath string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		err := os.Remove(legacyPath + suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func sqliteDSN(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func isDocumentDBFileRel(rel string) bool {
	base := filepath.Base(rel)
	return rel == DocumentsDBRel ||
		strings.HasPrefix(rel, DocumentsDBRel+"-") ||
		base == legacyDocumentsDBRel ||
		strings.HasPrefix(base, legacyDocumentsDBRel+"-")
}

func cleanDocumentRel(rel string) (string, error) {
	clean, err := security.CleanRelativePath(rel, security.PathPolicy{AllowHidden: true})
	if err != nil {
		return "", err
	}
	return clean, nil
}

func documentKind(rel string) string {
	lower := strings.ToLower(rel)
	base := filepath.Base(lower)
	switch {
	case strings.Contains(lower, "spec"):
		return "spec"
	case strings.Contains(lower, "task"):
		return "task"
	case strings.Contains(lower, "intent"):
		return "intent"
	case strings.Contains(lower, "rule") || strings.Contains(lower, "policy") || strings.Contains(lower, "guardrail"):
		return "rule"
	case strings.HasSuffix(base, ".log") || strings.HasSuffix(base, ".jsonl") ||
		strings.Contains(base, "stdout") || strings.Contains(base, "stderr") ||
		strings.Contains(base, "event"):
		return "log"
	case strings.Contains(lower, "validation"):
		return "validation"
	case strings.Contains(lower, "manifest"):
		return "manifest"
	case strings.Contains(lower, "planning"):
		return "planning"
	case strings.Contains(lower, "codex"):
		return "codex"
	case strings.Contains(lower, "git"):
		return "git"
	case strings.Contains(lower, "input"):
		return "input"
	default:
		return "artifact"
	}
}

func documentMediaType(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".json":
		return "application/json"
	case ".jsonl":
		return "application/x-ndjson"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log", ".patch":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
