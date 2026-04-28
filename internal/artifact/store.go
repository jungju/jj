package artifact

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jungju/jj/internal/security"
)

var (
	runIDPattern        = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	artifactNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

const (
	PrivateDirMode  os.FileMode = 0o700
	PrivateFileMode os.FileMode = 0o600
)

// Store owns the artifact directory for one jj run.
type Store struct {
	CWD    string
	RunID  string
	RunDir string
	stats  *storeStats
}

type storeStats struct {
	redactionCount atomic.Int64
	mu             sync.Mutex
	redactionKinds map[string]int64
}

// NewStore creates a Store rooted at <cwd>/.jj/runs/<run-id>.
func NewStore(cwd, runID string) (Store, error) {
	if strings.TrimSpace(cwd) == "" {
		return Store{}, errors.New("cwd is required")
	}
	if err := ValidateRunID(runID); err != nil {
		return Store{}, err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return Store{}, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	runDir, err := security.SafeJoinNoSymlinks(abs, filepath.ToSlash(filepath.Join(".jj", "runs", runID)), security.PathPolicy{AllowHidden: true})
	if err != nil {
		return Store{}, err
	}
	return Store{
		CWD:    abs,
		RunID:  runID,
		RunDir: runDir,
		stats:  &storeStats{},
	}, nil
}

// NewRunID returns a filesystem-safe run ID with UTC time and random suffix.
func NewRunID(now time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", now.UTC().Format("20060102-150405"), now.UnixNano())
	}
	return fmt.Sprintf("%s-%s", now.UTC().Format("20060102-150405"), hex.EncodeToString(b[:]))
}

func ValidateRunID(runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("run id is required")
	}
	if runID == "." || runID == ".." || strings.HasPrefix(runID, ".") || strings.Contains(runID, "..") {
		return errors.New("run id is reserved")
	}
	if !runIDPattern.MatchString(runID) {
		return errors.New("run id may only contain letters, numbers, dots, underscores, and dashes")
	}
	redacted := security.RedactString(runID)
	if redacted != runID || strings.Contains(redacted, security.RedactionMarker) {
		return errors.New("run id is not allowed")
	}
	return nil
}

func ValidateArtifactName(name string) error {
	if strings.ContainsRune(name, 0) || containsControlCharacter(name) {
		return errors.New("artifact name is not allowed")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("artifact name is required")
	}
	if len(name) > 128 || security.ContainsEncodedPathMeta(name) {
		return errors.New("artifact name is not allowed")
	}
	if name == "." || name == ".." || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return errors.New("artifact name is not allowed")
	}
	if strings.HasPrefix(name, ".") || !artifactNamePattern.MatchString(name) {
		return errors.New("artifact name may only contain letters, numbers, dots, underscores, and dashes")
	}
	return nil
}

func (s Store) Init() error {
	runsDir, err := security.SafeJoinNoSymlinks(s.CWD, ".jj/runs", security.PathPolicy{AllowHidden: true})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runsDir, PrivateDirMode); err != nil {
		return fmt.Errorf("create run parent directory: %w", err)
	}
	if err := ensurePrivateDir(runsDir); err != nil {
		return fmt.Errorf("secure run parent directory: %w", err)
	}
	if err := os.Mkdir(s.RunDir, PrivateDirMode); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("run directory already exists: %s", s.RunDir)
		}
		return fmt.Errorf("create run directory: %w", err)
	}
	if err := ensurePrivateDir(s.RunDir); err != nil {
		return fmt.Errorf("secure run directory: %w", err)
	}
	for _, dir := range []string{"input", "planning", "snapshots", "codex", "git", "validation"} {
		path, err := s.Path(dir)
		if err != nil {
			return err
		}
		if err := os.Mkdir(path, PrivateDirMode); err != nil {
			return fmt.Errorf("create artifact directory %s: %w", dir, err)
		}
		if err := ensurePrivateDir(path); err != nil {
			return fmt.Errorf("secure artifact directory %s: %w", dir, err)
		}
	}
	return nil
}

func (s Store) Path(rel string) (string, error) {
	path, err := security.SafeJoinNoSymlinks(s.RunDir, filepath.ToSlash(rel), security.PathPolicy{})
	if err != nil {
		return "", fmt.Errorf("invalid artifact path: %w", err)
	}
	return path, nil
}

func (s Store) WriteFile(rel string, data []byte) (string, error) {
	path, err := s.Path(rel)
	if err != nil {
		return "", err
	}
	var report security.RedactionReport
	data, report = security.RedactContentWithReport(rel, data)
	s.RecordRedactionReport(report)
	if err := os.MkdirAll(filepath.Dir(path), PrivateDirMode); err != nil {
		return "", err
	}
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return "", err
	}
	path, err = s.Path(rel)
	if err != nil {
		return "", err
	}
	if err := AtomicWriteFile(path, data, PrivateFileMode); err != nil {
		return "", fmt.Errorf("write artifact: %w", err)
	}
	return path, nil
}

func (s Store) WriteString(rel, data string) (string, error) {
	return s.WriteFile(rel, []byte(data))
}

func (s Store) WriteJSON(rel string, value any) (string, error) {
	redacted, report := security.RedactJSONValueWithReport(value)
	s.RecordRedactionReport(report)
	data, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	return s.WriteFile(rel, data)
}

func (s Store) RecordRedactions(count int) {
	if count <= 0 || s.stats == nil {
		return
	}
	s.stats.redactionCount.Add(int64(count))
}

func (s Store) RecordRedactionReport(report security.RedactionReport) {
	if report.Count <= 0 || s.stats == nil {
		return
	}
	s.stats.redactionCount.Add(int64(report.Count))
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	if s.stats.redactionKinds == nil {
		s.stats.redactionKinds = map[string]int64{}
	}
	if len(report.Kinds) == 0 {
		s.stats.redactionKinds["unknown"] += int64(report.Count)
		return
	}
	for kind, count := range report.Kinds {
		if strings.TrimSpace(kind) != "" && count > 0 {
			s.stats.redactionKinds[kind] += int64(count)
		}
	}
}

func (s Store) RedactionCount() int64 {
	if s.stats == nil {
		return 0
	}
	return s.stats.redactionCount.Load()
}

func (s Store) RedactionKindCounts() map[string]int64 {
	out := map[string]int64{}
	if s.stats == nil {
		return out
	}
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	for kind, count := range s.stats.redactionKinds {
		if strings.TrimSpace(kind) != "" && count > 0 {
			out[kind] = count
		}
	}
	return out
}

func (s Store) RedactionKinds() []string {
	counts := s.RedactionKindCounts()
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func ensurePrivateDir(path string) error {
	return os.Chmod(path, PrivateDirMode)
}

func AtomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false

	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
