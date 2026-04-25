package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/serve"
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
		"--spec-doc", "PRODUCT_SPEC.md",
		"--task-doc", "IMPLEMENTATION_TASK.md",
		"--eval-doc", "REVIEW.md",
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
	if got.SpecDoc != "PRODUCT_SPEC.md" || got.TaskDoc != "IMPLEMENTATION_TASK.md" || got.EvalDoc != "REVIEW.md" {
		t.Fatalf("unexpected document flags: %#v", got)
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
	if !strings.Contains(stdout.String(), "--dry-run") || !strings.Contains(stdout.String(), "--cwd") || !strings.Contains(stdout.String(), "--spec-doc") {
		t.Fatalf("help output missing expected flags:\n%s", stdout.String())
	}
}

func TestServeCommandParsesFlags(t *testing.T) {
	var gotCWD, gotAddr, gotRunID string
	cmd := newRootCommandWithServe(
		func(_ context.Context, cfg run.Config) (*run.Result, error) {
			t.Fatal("run executor should not be called")
			return nil, nil
		},
		func(_ context.Context, cfg serve.Config) error {
			gotCWD = cfg.CWD
			gotAddr = cfg.Addr
			gotRunID = cfg.RunID
			return nil
		},
	)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"serve", "--cwd", "/tmp/repo", "--addr", "127.0.0.1:0", "--run-id", "run-1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if gotCWD != "/tmp/repo" || gotAddr != "127.0.0.1:0" || gotRunID != "run-1" {
		t.Fatalf("unexpected serve config: cwd=%q addr=%q runID=%q", gotCWD, gotAddr, gotRunID)
	}
}

func TestServeCommandHelp(t *testing.T) {
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"serve", "--help"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "--addr") || !strings.Contains(stdout.String(), "--run-id") {
		t.Fatalf("help output missing expected flags:\n%s", stdout.String())
	}
}
