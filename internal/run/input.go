package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

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

type guardedPlanPath struct {
	Path      string
	Requested string
	Root      string
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
	pathArg = strings.TrimSpace(pathArg)
	if !isMarkdownLikePath(pathArg) {
		return "", "", fmt.Errorf("plan file must be Markdown-like (.md or .markdown)")
	}
	guarded, err := guardPlanPath(pathArg, cwd)
	if err != nil {
		return "", "", err
	}
	path, err := revalidateGuardedPlanPath(guarded)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", path, errors.New("read plan file: unavailable")
	}
	if !info.Mode().IsRegular() {
		return "", path, errors.New("plan file must be a regular file")
	}
	path, err = revalidateGuardedPlanPath(guarded)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", path, errors.New("read plan file: unavailable")
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", path, fmt.Errorf("plan file is empty")
	}
	return string(data), path, nil
}

// ResolvePlanPath returns the canonical plan file path when it stays inside cwd.
func ResolvePlanPath(pathArg, cwd string) (string, error) {
	guarded, err := guardPlanPath(pathArg, cwd)
	if err != nil {
		return "", err
	}
	return guarded.Path, nil
}

func guardPlanPath(pathArg, cwd string) (guardedPlanPath, error) {
	if strings.ContainsRune(pathArg, 0) || containsControlCharacter(pathArg) {
		return guardedPlanPath{}, security.ErrOutsideWorkspace
	}
	pathArg = strings.TrimSpace(pathArg)
	if !utf8.ValidString(pathArg) || strings.Contains(pathArg, `\`) || planPathLooksSensitive(pathArg) {
		return guardedPlanPath{}, errors.New("plan path is not allowed")
	}
	invocationDir, err := os.Getwd()
	if err != nil {
		return guardedPlanPath{}, errors.New("plan path is not readable")
	}
	root, err := resolvePlanWorkspaceRoot(cwd, invocationDir)
	if err != nil {
		return guardedPlanPath{}, err
	}
	var candidate string
	if filepath.IsAbs(pathArg) {
		if security.ContainsEncodedPathMeta(pathArg) {
			return guardedPlanPath{}, security.ErrOutsideWorkspace
		}
		clean := filepath.Clean(pathArg)
		if clean != pathArg {
			return guardedPlanPath{}, security.ErrOutsideWorkspace
		}
		candidate = clean
	} else {
		clean, err := security.CleanRelativePath(filepath.ToSlash(pathArg), security.PathPolicy{})
		if err != nil {
			return guardedPlanPath{}, err
		}
		candidate = filepath.Join(invocationDir, filepath.FromSlash(clean))
	}
	requested := filepath.Clean(candidate)
	path, err := canonicalPlanPathInWorkspace(root, requested)
	if err != nil {
		return guardedPlanPath{}, err
	}
	return guardedPlanPath{Path: path, Requested: requested, Root: root}, nil
}

func resolvePlanWorkspaceRoot(cwd, invocationDir string) (string, error) {
	root := strings.TrimSpace(cwd)
	if root == "" {
		root = invocationDir
	}
	if strings.ContainsRune(root, 0) || containsControlCharacter(root) || security.ContainsEncodedPathMeta(root) {
		return "", security.ErrOutsideWorkspace
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", errors.New("plan workspace is not readable")
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return "", errors.New("plan workspace is not readable")
	}
	if !info.IsDir() {
		return "", errors.New("plan workspace is not a directory")
	}
	resolved, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", errors.New("plan workspace is not readable")
	}
	return filepath.Clean(resolved), nil
}

func canonicalPlanPathInWorkspace(root, path string) (string, error) {
	if _, err := security.SafePathInRoot(root, path, security.PathPolicy{}); err != nil {
		return "", planBoundaryError(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Clean(path), nil
		}
		return "", errors.New("plan path is not readable")
	}
	finalPath, err := security.SafePathInRoot(root, resolved, security.PathPolicy{})
	if err != nil {
		if errors.Is(err, security.ErrOutsideWorkspace) {
			return "", security.ErrSymlinkOutside
		}
		return "", planBoundaryError(err)
	}
	return filepath.Clean(finalPath), nil
}

func revalidateGuardedPlanPath(guarded guardedPlanPath) (string, error) {
	if strings.TrimSpace(guarded.Path) == "" || strings.TrimSpace(guarded.Requested) == "" || strings.TrimSpace(guarded.Root) == "" {
		return "", errors.New("plan path is not readable")
	}
	current, err := canonicalPlanPathInWorkspace(guarded.Root, guarded.Requested)
	if err != nil {
		return "", err
	}
	if filepath.Clean(current) != filepath.Clean(guarded.Path) {
		return "", security.ErrOutsideWorkspace
	}
	return canonicalPlanPathInWorkspace(guarded.Root, guarded.Path)
}

func planBoundaryError(err error) error {
	if errors.Is(err, security.ErrSymlinkOutside) || errors.Is(err, security.ErrSymlinkPath) || errors.Is(err, security.ErrOutsideWorkspace) {
		return err
	}
	return security.ErrOutsideWorkspace
}

func planPathLooksSensitive(pathArg string) bool {
	base := filepath.Base(pathArg)
	redacted := security.RedactString(base)
	if redacted != base || strings.Contains(redacted, security.RedactionMarker) {
		return true
	}
	lower := strings.ToLower(pathArg)
	for _, marker := range []string{
		strings.ToLower(security.RedactionMarker),
		"[redacted]",
		"[omitted]",
		"[hidden]",
		"[removed]",
		"<redacted>",
		"<omitted>",
		"<hidden>",
		"<removed>",
		"{redacted}",
		"{omitted}",
		"{hidden}",
		"{removed}",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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
