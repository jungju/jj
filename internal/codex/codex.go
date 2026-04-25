package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Request struct {
	Bin               string
	CWD               string
	Model             string
	Prompt            string
	EventsPath        string
	OutputLastMessage string
	AllowNoGit        bool
}

type Result struct {
	Summary    string
	ExitCode   int
	DurationMS int64
}

type Runner struct{}

func (Runner) Run(ctx context.Context, req Request) (Result, error) {
	bin := strings.TrimSpace(req.Bin)
	if bin == "" {
		bin = "codex"
	}
	resolved, err := lookPath(bin)
	if err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("codex executable not found in PATH; set JJ_CODEX_BIN to override")
	}

	args := BuildArgs(req)
	if err := os.MkdirAll(filepath.Dir(req.EventsPath), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(req.OutputLastMessage), 0o755); err != nil {
		return Result{}, err
	}

	events, err := os.Create(req.EventsPath)
	if err != nil {
		return Result{}, fmt.Errorf("create codex events file: %w", err)
	}
	defer events.Close()

	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Stdout = events
	cmd.Stderr = events

	start := time.Now()
	err = cmd.Run()
	summaryBytes, readErr := os.ReadFile(req.OutputLastMessage)
	result := Result{
		Summary:    string(summaryBytes),
		ExitCode:   exitCode(err),
		DurationMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return result, fmt.Errorf("codex exec failed: %w; additionally failed to read summary: %v", err, readErr)
		}
		return result, fmt.Errorf("codex exec failed: %w", err)
	}
	if readErr != nil {
		return result, fmt.Errorf("read codex summary: %w", readErr)
	}
	return result, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func BuildArgs(req Request) []string {
	args := []string{
		"exec",
		"--cd", req.CWD,
		"--json",
		"--output-last-message", req.OutputLastMessage,
		"--sandbox", "workspace-write",
		"--ask-for-approval", "never",
	}
	if strings.TrimSpace(req.Model) != "" {
		args = append(args, "--model", req.Model)
	}
	if req.AllowNoGit {
		args = append(args, "--skip-git-repo-check")
	}
	return append(args, "-")
}

func lookPath(bin string) (string, error) {
	if strings.ContainsRune(bin, os.PathSeparator) {
		if info, err := os.Stat(bin); err == nil && !info.IsDir() {
			return bin, nil
		}
		return "", os.ErrNotExist
	}
	return exec.LookPath(bin)
}
