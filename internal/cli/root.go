package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/secrets"
	"github.com/jungju/jj/internal/serve"
)

type executor func(context.Context, run.Config) (*run.Result, error)
type serveExecutor func(context.Context, serve.Config) error

// NewRootCommand builds the jj CLI.
func NewRootCommand() *cobra.Command {
	return newRootCommandWithServeAndIO(run.Execute, serve.Execute, os.Stdout, os.Stderr)
}

func newRootCommand(exec executor) *cobra.Command {
	return newRootCommandWithServeAndIO(exec, serve.Execute, os.Stdout, os.Stderr)
}

func newRootCommandWithServe(exec executor, serveExec serveExecutor) *cobra.Command {
	return newRootCommandWithServeAndIO(exec, serveExec, os.Stdout, os.Stderr)
}

func newRootCommandWithServeAndIO(exec executor, serveExec serveExecutor, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "jj",
		Short:         "Run a planning-to-Codex implementation pipeline",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.AddCommand(newRunCommand(exec, stdout, stderr))
	cmd.AddCommand(newServeCommand(serveExec, stdout))
	return cmd
}

func Main(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := newRootCommandWithServeAndIO(run.Execute, serve.Execute, stdout, stderr)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", secrets.Redact(err.Error()))
		if isValidationError(err) {
			return 2
		}
		return 1
	}
	return 0
}

func isValidationError(err error) bool {
	if run.IsValidationError(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"accepts ",
		"unknown command",
		"unknown flag",
		"invalid argument",
		"use only one",
		"requires explicit",
		"must match",
		"cwd does not exist",
		"cwd is not a directory",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func newRunCommand(exec executor, stdout, stderr io.Writer) *cobra.Command {
	cfg := run.Config{
		PlanningAgents: run.DefaultPlanningAgents,
		OpenAIModel:    run.DefaultOpenAIModel(),
		CodexModel:     os.Getenv("JJ_CODEX_MODEL"),
		CodexBin:       run.DefaultCodexBinary,
		SpecDoc:        run.DefaultSpecDoc,
		TaskDoc:        run.DefaultTaskDoc,
		EvalDoc:        run.DefaultEvalDoc,
		Stdout:         stdout,
		Stderr:         stderr,
	}

	cmd := &cobra.Command{
		Use:   "run <plan.md>",
		Short: "Create SPEC/TASK, run Codex, and evaluate the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.PlanPath = args[0]
			specPathChanged := cmd.Flags().Changed("spec-path")
			specDocChanged := cmd.Flags().Changed("spec-doc")
			taskPathChanged := cmd.Flags().Changed("task-path")
			taskDocChanged := cmd.Flags().Changed("task-doc")
			evalPathChanged := cmd.Flags().Changed("eval-path")
			evalDocChanged := cmd.Flags().Changed("eval-doc")
			if specPathChanged && specDocChanged {
				return errors.New("use only one of --spec-path or --spec-doc")
			}
			if taskPathChanged && taskDocChanged {
				return errors.New("use only one of --task-path or --task-doc")
			}
			if evalPathChanged && evalDocChanged {
				return errors.New("use only one of --eval-path or --eval-doc")
			}
			cfg.OpenAIModelExplicit = cmd.Flags().Changed("openai-model")
			cfg.CodexModelExplicit = cmd.Flags().Changed("codex-model")
			cfg.CodexBinExplicit = cmd.Flags().Changed("codex-bin") || cmd.Flags().Changed("codex-binary")
			cfg.CWDExplicit = cmd.Flags().Changed("cwd")
			cfg.RunIDExplicit = cmd.Flags().Changed("run-id")
			cfg.PlanningAgentsExplicit = cmd.Flags().Changed("planner-agents") || cmd.Flags().Changed("planning-agents") || cmd.Flags().Changed("agent-count") || cmd.Flags().Changed("agents")
			cfg.SpecDocExplicit = specPathChanged || specDocChanged
			cfg.TaskDocExplicit = taskPathChanged || taskDocChanged
			cfg.EvalDocExplicit = evalPathChanged || evalDocChanged
			cfg.SpecDocPathMode = specPathChanged
			cfg.TaskDocPathMode = taskPathChanged
			cfg.EvalDocPathMode = evalPathChanged
			cfg.DryRunExplicit = cmd.Flags().Changed("dry-run")
			cfg.AllowNoGitExplicit = cmd.Flags().Changed("allow-no-git")
			if cwd, err := os.Getwd(); err == nil {
				cfg.ConfigSearchDir = cwd
			} else {
				return err
			}
			_, err := exec(cmd.Context(), cfg)
			return err
		},
	}

	cmd.Flags().StringVar(&cfg.CWD, "cwd", "", "target repository directory (defaults to current directory)")
	cmd.Flags().StringVar(&cfg.RunID, "run-id", "", "run identifier for .jj/runs/<run-id>")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "planner-agents", run.DefaultPlanningAgents, "number of planning agents to run")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "planning-agents", run.DefaultPlanningAgents, "number of planning agents to run (alias)")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "agent-count", run.DefaultPlanningAgents, "number of planning agents to run (alias)")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "agents", run.DefaultPlanningAgents, "number of planning agents to run (alias)")
	cmd.Flags().StringVar(&cfg.OpenAIModel, "openai-model", cfg.OpenAIModel, "OpenAI model for planning and evaluation")
	cmd.Flags().StringVar(&cfg.CodexModel, "codex-model", cfg.CodexModel, "Codex CLI model override")
	cmd.Flags().StringVar(&cfg.CodexBin, "codex-bin", cfg.CodexBin, "Codex CLI binary path override")
	cmd.Flags().StringVar(&cfg.CodexBin, "codex-binary", cfg.CodexBin, "Codex CLI binary path override (alias)")
	cmd.Flags().StringVar(&cfg.SpecDoc, "spec-path", cfg.SpecDoc, "workspace-relative SPEC path")
	cmd.Flags().StringVar(&cfg.TaskDoc, "task-path", cfg.TaskDoc, "workspace-relative TASK path")
	cmd.Flags().StringVar(&cfg.EvalDoc, "eval-path", cfg.EvalDoc, "workspace-relative EVAL path")
	cmd.Flags().StringVar(&cfg.SpecDoc, "spec-doc", cfg.SpecDoc, "SPEC document file name under docs/ (alias)")
	cmd.Flags().StringVar(&cfg.TaskDoc, "task-doc", cfg.TaskDoc, "TASK document file name under docs/ (alias)")
	cmd.Flags().StringVar(&cfg.EvalDoc, "eval-doc", cfg.EvalDoc, "EVAL document file name under docs/ (alias)")
	cmd.Flags().BoolVar(&cfg.AllowNoGit, "allow-no-git", false, "allow running outside a git repository")
	cmd.Flags().BoolVar(&cfg.DryRun, "dry-run", false, "create planning artifacts without writing SPEC/TASK to the target or running implementation Codex")

	return cmd
}

func newServeCommand(exec serveExecutor, stdout io.Writer) *cobra.Command {
	cfg := serve.Config{
		Host:   serve.DefaultHost,
		Port:   serve.DefaultPort,
		Addr:   serve.DefaultAddr,
		Stdout: stdout,
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve jj docs and run artifacts locally",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			addrChanged := cmd.Flags().Changed("addr")
			hostChanged := cmd.Flags().Changed("host")
			portChanged := cmd.Flags().Changed("port")
			if (hostChanged || portChanged) && addrChanged {
				return errors.New("use either --addr or --host/--port")
			}
			cfg.CWDExplicit = cmd.Flags().Changed("cwd")
			cfg.AddrExplicit = addrChanged
			cfg.HostExplicit = hostChanged
			cfg.PortExplicit = portChanged
			if cwd, err := os.Getwd(); err == nil {
				cfg.ConfigSearchDir = cwd
			} else {
				return err
			}
			if strings.TrimSpace(cfg.CWD) == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				cfg.CWD = cwd
			}
			return exec(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.CWD, "cwd", "", "directory containing docs and .jj/runs (defaults to current directory)")
	cmd.Flags().StringVar(&cfg.Addr, "addr", serve.DefaultAddr, "address for the local documentation server")
	cmd.Flags().StringVar(&cfg.Host, "host", serve.DefaultHost, "host for the local documentation server")
	cmd.Flags().IntVar(&cfg.Port, "port", serve.DefaultPort, "port for the local documentation server")
	cmd.Flags().StringVar(&cfg.RunID, "run-id", "", "run id to highlight by default")

	return cmd
}
