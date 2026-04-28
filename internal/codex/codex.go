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

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/security"
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

const defaultCommandTimeout = 30 * time.Minute

type Runner struct{}

func (Runner) Run(ctx context.Context, req Request) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()

	bin := strings.TrimSpace(req.Bin)
	if bin == "" {
		bin = "codex"
	}
	cwd, err := security.ResolveCommandCWD(req.CWD)
	if err != nil {
		return Result{}, fmt.Errorf("invalid codex cwd: %w", err)
	}
	req.CWD = cwd
	eventsPath, err := safeOutputPath(req.CWD, req.EventsPath)
	if err != nil {
		return Result{}, fmt.Errorf("invalid codex events path: %w", err)
	}
	lastMessagePath, err := safeOutputPath(req.CWD, req.OutputLastMessage)
	if err != nil {
		return Result{}, fmt.Errorf("invalid codex summary path: %w", err)
	}
	req.EventsPath = eventsPath
	req.OutputLastMessage = lastMessagePath
	resolved, err := lookPath(bin)
	if err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("codex executable not found in PATH; set JJ_CODEX_BIN to override")
	}

	if err := os.MkdirAll(filepath.Dir(req.EventsPath), artifact.PrivateDirMode); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(req.OutputLastMessage), artifact.PrivateDirMode); err != nil {
		return Result{}, err
	}
	eventsPath, err = safeOutputPath(req.CWD, req.EventsPath)
	if err != nil {
		return Result{}, fmt.Errorf("invalid codex events path: %w", err)
	}
	lastMessagePath, err = safeOutputPath(req.CWD, req.OutputLastMessage)
	if err != nil {
		return Result{}, fmt.Errorf("invalid codex summary path: %w", err)
	}
	req.EventsPath = eventsPath
	req.OutputLastMessage = lastMessagePath

	eventsTempDir, eventsTempPath, err := quarantineOutputPath(req.EventsPath)
	if err != nil {
		return Result{}, fmt.Errorf("create codex events quarantine directory: %w", err)
	}
	defer os.RemoveAll(eventsTempDir)
	events, err := os.Create(eventsTempPath)
	if err != nil {
		return Result{}, fmt.Errorf("create codex events quarantine file: %w", err)
	}

	lastMessageTempDir, lastMessageTempPath, err := quarantineOutputPath(req.OutputLastMessage)
	if err != nil {
		_ = events.Close()
		return Result{}, fmt.Errorf("create codex summary quarantine directory: %w", err)
	}
	defer os.RemoveAll(lastMessageTempDir)

	commandReq := req
	commandReq.EventsPath = eventsTempPath
	commandReq.OutputLastMessage = lastMessageTempPath
	args := BuildArgs(commandReq)

	cmd := exec.CommandContext(cmdCtx, resolved, args...)
	cmd.Dir = req.CWD
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Stdout = events
	cmd.Stderr = events
	cmd.Env = scrubImplementationEnv(os.Environ())

	start := time.Now()
	err = cmd.Run()
	closeErr := events.Close()
	if closeErr != nil && err == nil {
		err = closeErr
	}

	if publishErr := publishRedactedQuarantine(eventsTempPath, req.EventsPath); publishErr != nil {
		return Result{ExitCode: exitCode(err), DurationMS: time.Since(start).Milliseconds()}, publishErr
	}
	summaryBytes, readErr := os.ReadFile(lastMessageTempPath)
	if readErr == nil {
		redactedSummary := security.SanitizeHandoffContent(req.OutputLastMessage, summaryBytes)
		if writeErr := artifact.AtomicWriteFile(req.OutputLastMessage, redactedSummary, artifact.PrivateFileMode); writeErr != nil {
			return Result{ExitCode: exitCode(err), DurationMS: time.Since(start).Milliseconds()}, fmt.Errorf("write redacted codex summary: %w", writeErr)
		}
		summaryBytes = redactedSummary
	}
	result := Result{
		Summary:    string(summaryBytes),
		ExitCode:   exitCode(err),
		DurationMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return result, fmt.Errorf("codex exec failed: %w; additionally failed to read summary: %v", err, readErr)
		}
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return result, errors.New("codex exec failed: command timed out")
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return result, context.Canceled
		}
		return result, fmt.Errorf("codex exec failed: %w", err)
	}
	if readErr != nil {
		return result, fmt.Errorf("read codex summary: %w", readErr)
	}
	return result, nil
}

func scrubImplementationEnv(env []string) []string {
	return security.FilterEnv(env)
}

func safeOutputPath(cwd, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	absRoot, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	cleanRel, err := security.CleanRelativePath(filepath.ToSlash(rel), security.PathPolicy{AllowHidden: true})
	if err != nil {
		return "", err
	}
	parts := strings.Split(cleanRel, "/")
	if len(parts) < 4 || parts[0] != ".jj" || parts[1] != "runs" {
		return "", security.ErrOutsideWorkspace
	}
	if err := artifact.ValidateRunID(parts[2]); err != nil {
		return "", err
	}
	for _, part := range parts[3:] {
		if strings.HasPrefix(part, ".") {
			return "", security.ErrOutsideWorkspace
		}
	}
	return security.SafeJoinNoSymlinks(absRoot, cleanRel, security.PathPolicy{AllowHidden: true})
}

func publishRedactedQuarantine(tempPath, finalPath string) error {
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("read codex quarantine output: %w", err)
	}
	if err := artifact.AtomicWriteFile(finalPath, security.SanitizeHandoffContent(finalPath, data), artifact.PrivateFileMode); err != nil {
		return fmt.Errorf("write redacted codex output: %w", err)
	}
	return nil
}

func quarantineOutputPath(finalPath string) (string, string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(finalPath), "."+filepath.Base(finalPath)+".raw-*")
	if err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, filepath.Base(finalPath)), nil
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
		"--full-auto",
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
