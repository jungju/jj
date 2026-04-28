package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jungju/jj/internal/security"
)

const (
	PlanInputSourceFile      = "file"
	PlanInputSourceWebPrompt = "web_prompt"
	DefaultWebPromptInput    = "web prompt"
)

type PlanInput struct {
	Content string
	Path    string
	Source  string
}

func LoadPlanInput(pathArg, text, inputName, cwd string) (PlanInput, error) {
	if strings.TrimSpace(text) != "" {
		name := strings.TrimSpace(inputName)
		if name == "" {
			name = DefaultWebPromptInput
		}
		return PlanInput{
			Content: text,
			Path:    name,
			Source:  PlanInputSourceWebPrompt,
		}, nil
	}
	content, absPath, err := LoadPlan(pathArg, cwd)
	if err != nil {
		return PlanInput{Path: absPath, Source: PlanInputSourceFile}, err
	}
	return PlanInput{
		Content: content,
		Path:    absPath,
		Source:  PlanInputSourceFile,
	}, nil
}

func LoadPlan(pathArg, cwd string) (content string, absPath string, err error) {
	if strings.TrimSpace(pathArg) == "" {
		return "", "", errors.New("plan file is required")
	}
	if pathArg == "-" {
		return "", "", errors.New("stdin input is not supported yet; pass a plan file path")
	}
	if !isMarkdownLikePath(pathArg) {
		return "", "", fmt.Errorf("plan file must be Markdown-like (.md or .markdown)")
	}
	path, err := resolvePlanPath(pathArg, cwd)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", path, fmt.Errorf("read plan file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", path, errors.New("plan file must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", path, fmt.Errorf("read plan file: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", path, fmt.Errorf("plan file is empty")
	}
	return string(data), path, nil
}

func resolvePlanPath(pathArg, cwd string) (string, error) {
	if strings.ContainsRune(pathArg, 0) || containsControlCharacter(pathArg) {
		return "", security.ErrOutsideWorkspace
	}
	pathArg = strings.TrimSpace(pathArg)
	if strings.Contains(pathArg, `\`) {
		return "", security.ErrOutsideWorkspace
	}
	invocationDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(pathArg) {
		if security.ContainsEncodedPathMeta(pathArg) {
			return "", security.ErrOutsideWorkspace
		}
		clean := filepath.Clean(pathArg)
		if clean != pathArg {
			return "", security.ErrOutsideWorkspace
		}
		return safePlanPathInWorkspace(clean, invocationDir, cwd)
	}
	clean, err := security.CleanRelativePath(filepath.ToSlash(pathArg), security.PathPolicy{})
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(invocationDir, filepath.FromSlash(clean))
	return safePlanPathInWorkspace(candidate, invocationDir, cwd)
}

func safePlanPathInWorkspace(path, invocationDir, cwd string) (string, error) {
	root := strings.TrimSpace(cwd)
	if root == "" {
		root = invocationDir
	}
	resolved, err := security.SafePathInRoot(root, path, security.PathPolicy{})
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, security.ErrSymlinkOutside) {
		return "", err
	}
	return "", security.ErrOutsideWorkspace
}

func isMarkdownLikePath(pathArg string) bool {
	switch strings.ToLower(filepath.Ext(pathArg)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
