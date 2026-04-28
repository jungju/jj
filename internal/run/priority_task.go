package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/security"
)

type PriorityTaskInput struct {
	Content string
	Path    string
}

func (p PriorityTaskInput) Active() bool {
	return strings.TrimSpace(p.Content) != ""
}

func LoadPriorityTask(cwd string) (PriorityTaskInput, error) {
	path, err := priorityTaskPath(cwd)
	if err != nil {
		return PriorityTaskInput{}, fmt.Errorf("load %s: %w", DefaultPriorityTaskPath, err)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return PriorityTaskInput{Path: path}, nil
	}
	if err != nil {
		return PriorityTaskInput{Path: path}, fmt.Errorf("read %s: priority task file is not readable", DefaultPriorityTaskPath)
	}
	if strings.TrimSpace(string(data)) == "" {
		return PriorityTaskInput{Path: path}, nil
	}
	return PriorityTaskInput{
		Content: redactSecrets(string(data)),
		Path:    path,
	}, nil
}

func clearPriorityTask(cwd string) error {
	path, err := priorityTaskPath(cwd)
	if err != nil {
		return fmt.Errorf("clear %s: %w", DefaultPriorityTaskPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), artifact.PrivateDirMode); err != nil {
		return fmt.Errorf("clear %s: %w", DefaultPriorityTaskPath, err)
	}
	path, err = priorityTaskPath(cwd)
	if err != nil {
		return fmt.Errorf("clear %s: %w", DefaultPriorityTaskPath, err)
	}
	if err := artifact.AtomicWriteFile(path, nil, artifact.PrivateFileMode); err != nil {
		return fmt.Errorf("clear %s: %w", DefaultPriorityTaskPath, err)
	}
	return nil
}

func priorityTaskPath(cwd string) (string, error) {
	return security.SafeJoinNoSymlinks(cwd, DefaultPriorityTaskPath, security.PathPolicy{AllowHidden: true})
}
