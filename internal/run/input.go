package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadPlan(pathArg, cwd string) (content string, absPath string, err error) {
	if strings.TrimSpace(pathArg) == "" {
		return "", "", errors.New("plan file is required")
	}
	if pathArg == "-" {
		return "", "", errors.New("stdin input is not supported yet; pass a plan file path")
	}
	if !isMarkdownLikePath(pathArg) {
		return "", "", fmt.Errorf("plan file must be Markdown-like (.md or .markdown): %s", pathArg)
	}
	path, err := resolvePlanPath(pathArg, cwd)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", path, fmt.Errorf("read plan file %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", path, fmt.Errorf("plan file is empty: %s", path)
	}
	return string(data), path, nil
}

func resolvePlanPath(pathArg, cwd string) (string, error) {
	if filepath.IsAbs(pathArg) {
		return filepath.Abs(pathArg)
	}
	_ = cwd
	return filepath.Abs(pathArg)
}

func isMarkdownLikePath(pathArg string) bool {
	switch strings.ToLower(filepath.Ext(pathArg)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}
