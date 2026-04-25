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
		"--planner-agents", "2",
		"--openai-model", "model-a",
		"--codex-model", "model-b",
		"--codex-binary", "/tmp/codex",
		"--spec-path", "docs/PRODUCT_SPEC.md",
		"--task-path", "docs/IMPLEMENTATION_TASK.md",
		"--eval-path", "docs/REVIEW.md",
		"--allow-no-git",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if got.PlanPath != "plan.md" || !got.DryRun || got.CWD != "/tmp/repo" || got.RunID != "run-1" {
		t.Fatalf("unexpected parsed config: %#v", got)
	}
	if got.PlanningAgents != 2 || got.OpenAIModel != "model-a" || got.CodexModel != "model-b" || got.CodexBin != "/tmp/codex" || !got.AllowNoGit {
		t.Fatalf("unexpected parsed flags: %#v", got)
	}
	if got.SpecDoc != "docs/PRODUCT_SPEC.md" || got.TaskDoc != "docs/IMPLEMENTATION_TASK.md" || got.EvalDoc != "docs/REVIEW.md" {
		t.Fatalf("unexpected document flags: %#v", got)
	}
	if !got.SpecDocPathMode || !got.TaskDocPathMode || !got.EvalDocPathMode {
		t.Fatalf("expected --*-path flags to use path mode: %#v", got)
	}
	if !got.PlanningAgentsExplicit || !got.OpenAIModelExplicit || !got.CodexModelExplicit || !got.CodexBinExplicit {
		t.Fatalf("expected explicit flag markers: %#v", got)
	}
	if !got.SpecDocExplicit || !got.TaskDocExplicit || !got.EvalDocExplicit || !got.DryRunExplicit || !got.AllowNoGitExplicit {
		t.Fatalf("expected explicit document/boolean flag markers: %#v", got)
	}
	if got.ConfigSearchDir == "" {
		t.Fatal("expected config search directory to be set")
	}
}

func TestRunCommandRejectsMixedPathAndLegacyDocFlags(t *testing.T) {
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		t.Fatal("executor should not be called")
		return nil, nil
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "plan.md", "--spec-path", "SPEC.md", "--spec-doc", "PRODUCT.md"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--spec-path") {
		t.Fatalf("expected mixed flag error, got %v", err)
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

func TestMainReturnsValidationExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main([]string{"run"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected validation exit code 2, got %d stderr=%s", code, stderr.String())
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
	if !strings.Contains(stdout.String(), "--dry-run") || !strings.Contains(stdout.String(), "--cwd") || !strings.Contains(stdout.String(), "--spec-path") || !strings.Contains(stdout.String(), "--planner-agents") {
		t.Fatalf("help output missing expected flags:\n%s", stdout.String())
	}
}

func TestServeCommandParsesFlags(t *testing.T) {
	var gotCWD, gotAddr, gotRunID string
	var gotAddrExplicit bool
	cmd := newRootCommandWithServe(
		func(_ context.Context, cfg run.Config) (*run.Result, error) {
			t.Fatal("run executor should not be called")
			return nil, nil
		},
		func(_ context.Context, cfg serve.Config) error {
			gotCWD = cfg.CWD
			gotAddr = cfg.Addr
			gotRunID = cfg.RunID
			gotAddrExplicit = cfg.AddrExplicit
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
	if !gotAddrExplicit {
		t.Fatalf("expected addr explicit marker")
	}
}

func TestServeCommandParsesHostPort(t *testing.T) {
	var gotHost string
	var gotPort int
	var gotHostExplicit, gotPortExplicit bool
	cmd := newRootCommandWithServe(
		func(_ context.Context, cfg run.Config) (*run.Result, error) {
			t.Fatal("run executor should not be called")
			return nil, nil
		},
		func(_ context.Context, cfg serve.Config) error {
			gotHost = cfg.Host
			gotPort = cfg.Port
			gotHostExplicit = cfg.HostExplicit
			gotPortExplicit = cfg.PortExplicit
			return nil
		},
	)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"serve", "--cwd", "/tmp/repo", "--host", "localhost", "--port", "0"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if gotHost != "localhost" || gotPort != 0 || !gotHostExplicit || !gotPortExplicit {
		t.Fatalf("unexpected host/port config: host=%q port=%d explicit=%t/%t", gotHost, gotPort, gotHostExplicit, gotPortExplicit)
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
