package cli

import (
	"context"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/serve"
)

type executor func(context.Context, run.Config) (*run.Result, error)
type serveExecutor func(context.Context, serve.Config) error

// NewRootCommand builds the jj CLI.
func NewRootCommand() *cobra.Command {
	return newRootCommand(run.Execute)
}

func newRootCommand(exec executor) *cobra.Command {
	return newRootCommandWithServe(exec, serve.Execute)
}

func newRootCommandWithServe(exec executor, serveExec serveExecutor) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "jj",
		Short:         "Run a planning-to-Codex implementation pipeline",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newRunCommand(exec))
	cmd.AddCommand(newServeCommand(serveExec))
	return cmd
}

func newRunCommand(exec executor) *cobra.Command {
	cfg := run.Config{
		PlanningAgents: run.DefaultPlanningAgents,
		OpenAIModel:    run.DefaultOpenAIModel(),
		CodexModel:     os.Getenv("JJ_CODEX_MODEL"),
		SpecDoc:        run.DefaultSpecDoc,
		TaskDoc:        run.DefaultTaskDoc,
		EvalDoc:        run.DefaultEvalDoc,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
	}

	cmd := &cobra.Command{
		Use:   "run <plan.md>",
		Short: "Create SPEC/TASK, run Codex, and evaluate the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.PlanPath = args[0]
			cfg.OpenAIModelExplicit = cmd.Flags().Changed("openai-model")
			cfg.CodexModelExplicit = cmd.Flags().Changed("codex-model")
			cfg.PlanningAgentsExplicit = cmd.Flags().Changed("planning-agents") || cmd.Flags().Changed("agents")
			cfg.SpecDocExplicit = cmd.Flags().Changed("spec-doc")
			cfg.TaskDocExplicit = cmd.Flags().Changed("task-doc")
			cfg.EvalDocExplicit = cmd.Flags().Changed("eval-doc")
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
	cmd.Flags().IntVar(&cfg.PlanningAgents, "agents", run.DefaultPlanningAgents, "number of planning agents to run")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "planning-agents", run.DefaultPlanningAgents, "number of planning agents to run")
	cmd.Flags().StringVar(&cfg.OpenAIModel, "openai-model", cfg.OpenAIModel, "OpenAI model for planning and evaluation")
	cmd.Flags().StringVar(&cfg.CodexModel, "codex-model", cfg.CodexModel, "Codex CLI model override")
	cmd.Flags().StringVar(&cfg.SpecDoc, "spec-doc", cfg.SpecDoc, "SPEC document file name under docs/")
	cmd.Flags().StringVar(&cfg.TaskDoc, "task-doc", cfg.TaskDoc, "TASK document file name under docs/")
	cmd.Flags().StringVar(&cfg.EvalDoc, "eval-doc", cfg.EvalDoc, "EVAL document file name under docs/")
	cmd.Flags().BoolVar(&cfg.AllowNoGit, "allow-no-git", false, "allow running outside a git repository")
	cmd.Flags().BoolVar(&cfg.DryRun, "dry-run", false, "create planning artifacts without writing SPEC/TASK to the target or running implementation Codex")

	return cmd
}

func newServeCommand(exec serveExecutor) *cobra.Command {
	cfg := serve.Config{
		Addr:   serve.DefaultAddr,
		Stdout: os.Stdout,
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve jj docs and run artifacts locally",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&cfg.RunID, "run-id", "", "run id to highlight by default")

	return cmd
}
