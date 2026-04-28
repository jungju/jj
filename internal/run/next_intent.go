package run

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jungju/jj/internal/security"
)

type NextIntentInput struct {
	Content string
	Path    string
}

func (n NextIntentInput) Active() bool {
	return strings.TrimSpace(n.Content) != ""
}

func LoadNextIntent(cwd string) (NextIntentInput, error) {
	path, err := nextIntentPath(cwd)
	if err != nil {
		return NextIntentInput{}, fmt.Errorf("load %s: %w", DefaultNextIntentPath, err)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NextIntentInput{Path: path}, nil
	}
	if err != nil {
		return NextIntentInput{Path: path}, fmt.Errorf("read %s: next intent file is not readable", DefaultNextIntentPath)
	}
	if strings.TrimSpace(string(data)) == "" {
		return NextIntentInput{Path: path}, nil
	}
	return NextIntentInput{
		Content: redactSecrets(string(data)),
		Path:    path,
	}, nil
}

func nextIntentPath(cwd string) (string, error) {
	return security.SafeJoinNoSymlinks(cwd, DefaultNextIntentPath, security.PathPolicy{AllowHidden: true})
}
