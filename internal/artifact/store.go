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
	"strings"
	"time"
)

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Store owns the artifact directory for one jj run.
type Store struct {
	CWD    string
	RunID  string
	RunDir string
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
	return Store{
		CWD:    abs,
		RunID:  runID,
		RunDir: filepath.Join(abs, ".jj", "runs", runID),
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
	if runID == "." || runID == ".." {
		return fmt.Errorf("run id %q is reserved", runID)
	}
	if !runIDPattern.MatchString(runID) {
		return fmt.Errorf("run id %q may only contain letters, numbers, dots, underscores, and dashes", runID)
	}
	return nil
}

func (s Store) Init() error {
	if err := os.MkdirAll(filepath.Dir(s.RunDir), 0o755); err != nil {
		return fmt.Errorf("create run parent directory: %w", err)
	}
	if err := os.Mkdir(s.RunDir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("run directory already exists: %s", s.RunDir)
		}
		return fmt.Errorf("create run directory: %w", err)
	}
	for _, dir := range []string{"planning", "docs", "codex", "git"} {
		if err := os.Mkdir(filepath.Join(s.RunDir, dir), 0o755); err != nil {
			return fmt.Errorf("create artifact directory %s: %w", dir, err)
		}
	}
	return nil
}

func (s Store) Path(rel string) (string, error) {
	clean := filepath.Clean(rel)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("invalid artifact path %q", rel)
	}
	return filepath.Join(s.RunDir, clean), nil
}

func (s Store) WriteFile(rel string, data []byte) (string, error) {
	path, err := s.Path(rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := AtomicWriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write artifact %s: %w", rel, err)
	}
	return path, nil
}

func (s Store) WriteString(rel, data string) (string, error) {
	return s.WriteFile(rel, []byte(data))
}

func (s Store) WriteJSON(rel string, value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	return s.WriteFile(rel, data)
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
