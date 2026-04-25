package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/jungju/jj/internal/run"
)

type executor func(context.Context, run.Config) (*run.Result, error)

// NewRootCommand builds the jj CLI. The MVP intentionally exposes only `run`.
func NewRootCommand() *cobra.Command {
	return newRootCommand(run.Execute)
}

func newRootCommand(exec executor) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "jj",
		Short:         "Run a planning-to-Codex implementation pipeline",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newRunCommand(exec))
	return cmd
}

func newRunCommand(exec executor) *cobra.Command {
	cfg := run.Config{
		PlanningAgents: run.DefaultPlanningAgents,
		OpenAIModel:    run.DefaultOpenAIModel(),
		CodexModel:     os.Getenv("JJ_CODEX_MODEL"),
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
			cfg.PlanningAgentsExplicit = cmd.Flags().Changed("planning-agents")
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
	cmd.Flags().IntVar(&cfg.PlanningAgents, "planning-agents", run.DefaultPlanningAgents, "number of planning agents to run")
	cmd.Flags().StringVar(&cfg.OpenAIModel, "openai-model", cfg.OpenAIModel, "OpenAI model for planning and evaluation")
	cmd.Flags().StringVar(&cfg.CodexModel, "codex-model", cfg.CodexModel, "Codex CLI model override")
	cmd.Flags().BoolVar(&cfg.AllowNoGit, "allow-no-git", false, "allow running outside a git repository")
	cmd.Flags().BoolVar(&cfg.DryRun, "dry-run", false, "create planning artifacts without writing SPEC/TASK to the target or running implementation Codex")

	return cmd
}
