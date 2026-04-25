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

	fromInvocation, err := filepath.Abs(pathArg)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(fromInvocation); statErr == nil {
		return fromInvocation, nil
	}

	if strings.TrimSpace(cwd) == "" {
		return fromInvocation, nil
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	fromTarget := filepath.Join(absCWD, pathArg)
	if _, statErr := os.Stat(fromTarget); statErr == nil {
		return filepath.Abs(fromTarget)
	}
	return fromInvocation, nil
}
