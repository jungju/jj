package serve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jungju/jj/internal/security"
)

var dotenvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func loadServeEnvFiles(cfg Config, resolved bool) error {
	if cfg.NoEnvFile {
		return nil
	}
	if strings.TrimSpace(cfg.EnvFile) != "" {
		path, err := resolveEnvFilePath(cfg.EnvFile, cfg.ConfigSearchDir)
		if err != nil {
			return err
		}
		return loadDotenvFile(path, true)
	}
	var dirs []string
	if resolved {
		dirs = append(dirs, cfg.CWD)
	} else {
		dirs = append(dirs, cfg.ConfigSearchDir)
		if cfg.CWDExplicit {
			dirs = append(dirs, cfg.CWD)
		}
		if envCWD := firstEnv("JJ_CWD", "JJ_WORKSPACE_CWD"); envCWD != "" {
			dirs = append(dirs, envCWD)
		}
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		path, err := defaultEnvFilePath(dir)
		if err != nil {
			return err
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		if err := loadDotenvFile(path, false); err != nil {
			return err
		}
	}
	return nil
}

func resolveEnvFilePath(path, baseDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("env file path is required")
	}
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(baseDir) == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			baseDir = cwd
		}
		path = filepath.Join(baseDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

func defaultEnvFilePath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return security.SafeJoinNoSymlinks(abs, ".env", security.PathPolicy{AllowHidden: true})
}

func loadDotenvFile(path string, required bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load .env: %w", err)
	}
	values, err := parseDotenv(data)
	if err != nil {
		return fmt.Errorf("parse .env: %w", err)
	}
	for name, value := range values {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set .env variable %s: %w", name, err)
		}
	}
	return nil
}

func parseDotenv(data []byte) (map[string]string, error) {
	values := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d missing '='", i+1)
		}
		name = strings.TrimSpace(name)
		if !dotenvNamePattern.MatchString(name) {
			return nil, fmt.Errorf("line %d has invalid variable name", i+1)
		}
		parsed, err := parseDotenvValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		values[name] = parsed
	}
	return values, nil
}

func parseDotenvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	switch value[0] {
	case '"':
		if !strings.HasSuffix(value, `"`) || len(value) == 1 {
			return "", errors.New("unterminated double-quoted value")
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		return unquoted, nil
	case '\'':
		if !strings.HasSuffix(value, `'`) || len(value) == 1 {
			return "", errors.New("unterminated single-quoted value")
		}
		return value[1 : len(value)-1], nil
	default:
		return strings.TrimSpace(stripDotenvInlineComment(value)), nil
	}
}

func stripDotenvInlineComment(value string) string {
	for i, r := range value {
		if r == '#' && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t') {
			return value[:i]
		}
	}
	return value
}

func applyServeEnvAliases() {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		if value := strings.TrimSpace(os.Getenv("OPENAI_KEY")); value != "" {
			_ = os.Setenv("OPENAI_API_KEY", value)
		}
	}
}
