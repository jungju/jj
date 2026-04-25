package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type GitRunner interface {
	Output(ctx context.Context, cwd string, args ...string) (string, error)
}

type ExecGitRunner struct{}

func (ExecGitRunner) Output(ctx context.Context, cwd string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.TrimSpace(stderr.String()) != "" {
			return "", errors.New(strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return stdout.String(), nil
}

type GitState struct {
	Available     bool   `json:"available"`
	Root          string `json:"root"`
	Branch        string `json:"branch"`
	Head          string `json:"head"`
	InitialStatus string `json:"initial_status"`
	FinalStatus   string `json:"final_status,omitempty"`
	DiffPath      string `json:"diff_path,omitempty"`
	Dirty         bool   `json:"dirty"`
}

type GitDiff struct {
	Status     string `json:"status"`
	Stat       string `json:"stat"`
	NameStatus string `json:"name_status"`
	Full       string `json:"full"`
}

func InspectGit(ctx context.Context, cwd string, runners ...GitRunner) (GitState, error) {
	runner := chooseGitRunner(runners...)
	root, err := runner.Output(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitState{}, nil
	}
	head, err := runner.Output(ctx, cwd, "rev-parse", "HEAD")
	if err != nil {
		head = "unborn"
	}
	branch, err := runner.Output(ctx, cwd, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) == "" {
		branch, err = runner.Output(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			branch = "unknown"
		}
	}
	status, err := runner.Output(ctx, cwd, "status", "--short")
	if err != nil {
		return GitState{}, fmt.Errorf("git status: %w", err)
	}
	status = strings.TrimRight(status, "\n")
	return GitState{
		Available:     true,
		Root:          strings.TrimSpace(root),
		Branch:        strings.TrimSpace(branch),
		Head:          strings.TrimSpace(head),
		InitialStatus: status,
		Dirty:         strings.TrimSpace(status) != "",
	}, nil
}

func CaptureGitDiff(ctx context.Context, cwd string, available bool, runners ...GitRunner) (GitDiff, error) {
	if !available {
		return GitDiff{Status: "git unavailable"}, nil
	}
	runner := chooseGitRunner(runners...)
	status, err := runner.Output(ctx, cwd, "status", "--short")
	if err != nil {
		return GitDiff{}, err
	}
	stat, err := runner.Output(ctx, cwd, "diff", "--stat")
	if err != nil {
		return GitDiff{}, err
	}
	nameStatus, err := runner.Output(ctx, cwd, "diff", "--name-status")
	if err != nil {
		return GitDiff{}, err
	}
	full, err := runner.Output(ctx, cwd, "diff", "--binary")
	if err != nil {
		return GitDiff{}, err
	}
	return GitDiff{
		Status:     strings.TrimRight(status, "\n"),
		Stat:       strings.TrimRight(stat, "\n"),
		NameStatus: strings.TrimRight(nameStatus, "\n"),
		Full:       strings.TrimRight(full, "\n"),
	}, nil
}

func HasNonJJDirtyStatus(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path := line
		if len(line) > 3 {
			path = strings.TrimSpace(line[3:])
		}
		if path == ".jj" || strings.HasPrefix(path, ".jj/") || strings.Contains(path, " -> .jj/") {
			continue
		}
		return true
	}
	return false
}

func (d GitDiff) Markdown() string {
	return fmt.Sprintf("## git status --short\n%s\n\n## git diff --stat\n%s\n\n## git diff --name-status\n%s\n\n## git diff --binary\n%s\n", emptyAsNone(d.Status), emptyAsNone(d.Stat), emptyAsNone(d.NameStatus), emptyAsNone(d.Full))
}

func chooseGitRunner(runners ...GitRunner) GitRunner {
	if len(runners) > 0 && runners[0] != nil {
		return runners[0]
	}
	return ExecGitRunner{}
}

func emptyAsNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}
