package jjctl

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	AppName       = "jj"
	DefaultDBName = "jj.sqlite"
)

type Paths struct {
	Home            string
	DBPath          string
	ConfigPath      string
	SecretKeyPath   string
	SecretStorePath string
}

type App struct {
	Paths   Paths
	DB      *sql.DB
	Secrets *SecretStore
	Stdout  io.Writer
	Stderr  io.Writer
	Now     func() time.Time
}

type AppOptions struct {
	Home   string
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

func DefaultPaths(homeOverride string) (Paths, error) {
	home := strings.TrimSpace(homeOverride)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("JJ_HOME"))
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		home = filepath.Join(userHome, ".jj")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Home:            home,
		DBPath:          filepath.Join(home, DefaultDBName),
		ConfigPath:      filepath.Join(home, "config.json"),
		SecretKeyPath:   filepath.Join(home, "secret.key"),
		SecretStorePath: filepath.Join(home, "secrets.json"),
	}, nil
}

func OpenApp(ctx context.Context, opts AppOptions) (*App, error) {
	paths, err := DefaultPaths(opts.Home)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		return nil, err
	}
	db, err := OpenDB(ctx, paths.DBPath)
	if err != nil {
		return nil, err
	}
	secretStore, err := OpenSecretStore(paths.SecretKeyPath, paths.SecretStorePath)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &App{
		Paths:   paths,
		DB:      db,
		Secrets: secretStore,
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
		Now:     opts.Now,
	}, nil
}

func (a *App) Close() error {
	if a == nil || a.DB == nil {
		return nil
	}
	return a.DB.Close()
}

func (a *App) timestamp() string {
	return a.Now().UTC().Format(time.RFC3339)
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	if prefix == "" {
		return hex.EncodeToString(b[:])
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func writeJSONFile(path string, value any, perm os.FileMode) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, perm)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func ensureInsideRepo(path string) (string, error) {
	out, err := runCommand(context.Background(), path, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", CodeError{
			Code:    ErrRepoNotGitDirectory,
			Message: "현재 디렉터리가 Git repository가 아닙니다.",
			Remedy:  "Git repository 안에서 실행하거나 jjctl repo add <owner/repo>를 사용하세요.",
			Err:     err,
		}
	}
	return strings.TrimSpace(out), nil
}

func parsePoolTarget(value string) (string, string, error) {
	pool, target, ok := strings.Cut(strings.TrimSpace(value), "/")
	if !ok || strings.TrimSpace(pool) == "" || strings.TrimSpace(target) == "" {
		return "", "", fmt.Errorf("expected <pool>/<target>, got %q", value)
	}
	return strings.TrimSpace(pool), strings.TrimSpace(target), nil
}

func softDeleteByName(ctx context.Context, db *sql.DB, table, userID, name, now string) (int64, error) {
	if strings.TrimSpace(table) == "" {
		return 0, errors.New("table is required")
	}
	res, err := db.ExecContext(ctx, "UPDATE "+table+" SET deleted_at = ?, updated_at = ? WHERE user_id = ? AND name = ? AND deleted_at IS NULL", now, now, userID, name)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
