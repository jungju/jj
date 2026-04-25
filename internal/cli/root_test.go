package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jungju/jj/internal/run"
)

func TestRunCommandParsesFlags(t *testing.T) {
	var got run.Config
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		got = cfg
		return &run.Result{}, nil
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"run", "plan.md",
		"--dry-run",
		"--cwd", "/tmp/repo",
		"--run-id", "run-1",
		"--planning-agents", "2",
		"--openai-model", "model-a",
		"--codex-model", "model-b",
		"--allow-no-git",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if got.PlanPath != "plan.md" || !got.DryRun || got.CWD != "/tmp/repo" || got.RunID != "run-1" {
		t.Fatalf("unexpected parsed config: %#v", got)
	}
	if got.PlanningAgents != 2 || got.OpenAIModel != "model-a" || got.CodexModel != "model-b" || !got.AllowNoGit {
		t.Fatalf("unexpected parsed flags: %#v", got)
	}
	if !got.PlanningAgentsExplicit || !got.OpenAIModelExplicit || !got.CodexModelExplicit {
		t.Fatalf("expected explicit flag markers: %#v", got)
	}
	if got.ConfigSearchDir == "" {
		t.Fatal("expected config search directory to be set")
	}
}

func TestRunCommandRequiresPlan(t *testing.T) {
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		t.Fatal("executor should not be called")
		return nil, nil
	})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"run"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Fatalf("expected argument error, got %v", err)
	}
}

func TestRunCommandHelp(t *testing.T) {
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"run", "--help"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "--dry-run") || !strings.Contains(stdout.String(), "--cwd") {
		t.Fatalf("help output missing expected flags:\n%s", stdout.String())
	}
}
