package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/secrets"
	"github.com/jungju/jj/internal/security"
	"github.com/jungju/jj/internal/serve"
)

type executor func(context.Context, run.Config) (*run.Result, error)
type serveExecutor func(context.Context, serve.Config) error
type statusExecutor func(context.Context, serve.Config) (serve.StatusSummary, error)
type runsExecutor func(context.Context, serve.Config) (serve.RecentRunsSummary, error)

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
	return newRootCommandWithExecutors(exec, serveExec, serve.LoadStatusSummary, serve.LoadRecentRunsSummary, stdout, stderr)
}

func newRootCommandWithExecutors(exec executor, serveExec serveExecutor, statusExec statusExecutor, runsExec runsExecutor, stdout, stderr io.Writer) *cobra.Command {
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
	cmd.AddCommand(newStatusCommand(statusExec, stdout))
	cmd.AddCommand(newRunsCommand(runsExec, stdout))
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
		"invalid task proposal mode",
		"invalid push mode",
		"github token not found",
		"auto continue",
		"max turns",
		"use only one",
		"requires explicit",
		"must match",
		"cwd does not exist",
		"cwd is not a directory",
		"command cwd",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func newRunCommand(exec executor, stdout, stderr io.Writer) *cobra.Command {
	cfg := run.Config{
		PlanningAgents:   run.DefaultPlanningAgents,
		OpenAIModel:      run.DefaultOpenAIModel(),
		CodexModel:       os.Getenv("JJ_CODEX_MODEL"),
		CodexBin:         run.DefaultCodexBinary,
		TaskProposalMode: run.TaskProposalModeAuto,
		Stdout:           stdout,
		Stderr:           stderr,
	}
	taskProposalMode := string(cfg.TaskProposalMode)
	autoContinue := false
	maxTurns := 10

	cmd := &cobra.Command{
		Use:   "run <plan.md>",
		Short: "Create jj JSON state, run Codex, and validate the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.PlanPath = args[0]
			if err := rejectMutuallyExclusiveFlags(cmd, "task-proposal-mode", "proposal-mode"); err != nil {
				return err
			}
			cfg.OpenAIModelExplicit = cmd.Flags().Changed("openai-model")
			cfg.CodexModelExplicit = cmd.Flags().Changed("codex-model")
			cfg.CodexBinExplicit = cmd.Flags().Changed("codex-bin") || cmd.Flags().Changed("codex-binary")
			parsedTaskProposalMode, err := run.ParseTaskProposalMode(taskProposalMode)
			if err != nil {
				return err
			}
			cfg.TaskProposalMode = parsedTaskProposalMode
			cfg.TaskProposalModeExplicit = cmd.Flags().Changed("task-proposal-mode") || cmd.Flags().Changed("proposal-mode")
			cfg.RepoURLExplicit = cmd.Flags().Changed("repo")
			cfg.RepoDirExplicit = cmd.Flags().Changed("repo-dir")
			cfg.BaseBranchExplicit = cmd.Flags().Changed("base-branch")
			cfg.WorkBranchExplicit = cmd.Flags().Changed("work-branch")
			cfg.PushExplicit = cmd.Flags().Changed("push")
			cfg.PushModeExplicit = cmd.Flags().Changed("push-mode")
			cfg.GitHubTokenEnvExplicit = cmd.Flags().Changed("github-token-env")
			cfg.RepoAllowDirtyExplicit = cmd.Flags().Changed("allow-dirty")
			cfg.CWDExplicit = cmd.Flags().Changed("cwd")
			cfg.RunIDExplicit = cmd.Flags().Changed("run-id")
			cfg.PlanningAgentsExplicit = cmd.Flags().Changed("planner-agents") || cmd.Flags().Changed("planning-agents") || cmd.Flags().Changed("agent-count") || cmd.Flags().Changed("agents")
			cfg.DryRunExplicit = cmd.Flags().Changed("dry-run")
			cfg.AllowNoGitExplicit = cmd.Flags().Changed("allow-no-git")
			if cwd, err := os.Getwd(); err == nil {
				cfg.ConfigSearchDir = cwd
			} else {
				return err
			}
			if cmd.Flags().Changed("max-turns") && !autoContinue {
				return errors.New("--max-turns requires --auto-continue")
			}
			if autoContinue {
				return executeRunLoop(cmd.Context(), exec, cfg, maxTurns, stdout)
			}
			_, err = exec(cmd.Context(), cfg)
			return err
		},
	}

	cmd.Flags().StringVar(&cfg.CWD, "cwd", "", "target repository directory (defaults to current directory)")
	cmd.Flags().StringVar(&cfg.RunID, "run-id", "", "run identifier for .jj/runs/<run-id>")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "planner-agents", run.DefaultPlanningAgents, "number of planning agents to run")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "planning-agents", run.DefaultPlanningAgents, "number of planning agents to run (alias)")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "agent-count", run.DefaultPlanningAgents, "number of planning agents to run (alias)")
	cmd.Flags().IntVar(&cfg.PlanningAgents, "agents", run.DefaultPlanningAgents, "number of planning agents to run (alias)")
	cmd.Flags().StringVar(&cfg.OpenAIModel, "openai-model", cfg.OpenAIModel, "OpenAI model for planning")
	cmd.Flags().StringVar(&cfg.CodexModel, "codex-model", cfg.CodexModel, "Codex CLI model override")
	cmd.Flags().StringVar(&cfg.CodexBin, "codex-bin", cfg.CodexBin, "Codex CLI binary path override")
	cmd.Flags().StringVar(&cfg.CodexBin, "codex-binary", cfg.CodexBin, "Codex CLI binary path override (alias)")
	cmd.Flags().StringVar(&taskProposalMode, "task-proposal-mode", taskProposalMode, "task proposal mode: "+run.ValidTaskProposalModesString())
	cmd.Flags().StringVar(&taskProposalMode, "proposal-mode", taskProposalMode, "task proposal mode (alias): "+run.ValidTaskProposalModesString())
	cmd.Flags().StringVar(&cfg.RepoURL, "repo", "", "GitHub repository URL to clone or update before running")
	cmd.Flags().StringVar(&cfg.RepoDir, "repo-dir", "", "local directory for --repo clone/update")
	cmd.Flags().StringVar(&cfg.BaseBranch, "base-branch", "", "repository base branch for --repo (defaults to origin default branch or main)")
	cmd.Flags().StringVar(&cfg.WorkBranch, "work-branch", "", "repository work branch for --repo (defaults to jj/run-<run-id>)")
	cmd.Flags().BoolVar(&cfg.Push, "push", false, "push the jj work branch after a successful repository run")
	cmd.Flags().StringVar(&cfg.PushMode, "push-mode", run.DefaultPushMode, "push mode for --repo: branch, current-branch, none")
	cmd.Flags().StringVar(&cfg.GitHubTokenEnv, "github-token-env", run.DefaultGitHubTokenEnv, "environment variable containing the GitHub token")
	cmd.Flags().BoolVar(&cfg.RepoAllowDirty, "allow-dirty", false, "allow --repo to reuse a dirty working tree")
	cmd.Flags().BoolVar(&autoContinue, "auto-continue", false, "continue running turns until failure or max turns")
	cmd.Flags().IntVar(&maxTurns, "max-turns", 10, "maximum turns for --auto-continue")
	cmd.Flags().BoolVar(&cfg.AllowNoGit, "allow-no-git", false, "allow running outside a git repository")
	cmd.Flags().BoolVar(&cfg.DryRun, "dry-run", false, "create run planning artifacts without workspace state writes or implementation Codex")

	return cmd
}

func executeRunLoop(ctx context.Context, exec executor, cfg run.Config, maxTurns int, stdout io.Writer) error {
	resolved, err := run.ResolveConfig(cfg)
	if err != nil {
		return err
	}
	if resolved.DryRun {
		return errors.New("auto continue requires full-run; disable --dry-run")
	}
	if maxTurns < 1 || maxTurns > 50 {
		return errors.New("max turns must be between 1 and 50")
	}
	baseRunID := strings.TrimSpace(resolved.RunID)
	if baseRunID == "" {
		baseRunID = artifact.NewRunID(time.Now())
	}
	resolved.RunID = baseRunID
	resolved.RunIDExplicit = true
	if strings.TrimSpace(resolved.RepoURL) != "" {
		if strings.TrimSpace(resolved.WorkBranch) == "" {
			resolved.WorkBranch = "jj/run-" + baseRunID
		}
		resolved.WorkBranchExplicit = true
	}

	nextContext := strings.TrimSpace(resolved.AdditionalPlanContext)
	for turn := 1; turn <= maxTurns; turn++ {
		turnCfg := resolved
		turnCfg.RunID = run.TurnRunID(baseRunID, turn)
		turnCfg.RunIDExplicit = true
		turnCfg.AdditionalPlanContext = nextContext
		turnCfg.LoopEnabled = true
		turnCfg.LoopBaseRunID = baseRunID
		turnCfg.LoopTurn = turn
		turnCfg.LoopMaxTurns = maxTurns
		if turn > 1 {
			turnCfg.LoopPreviousRunID = run.TurnRunID(baseRunID, turn-1)
		}
		fmt.Fprintf(stdout, "jj loop: turn %d/%d run_id=%s\n", turn, maxTurns, turnCfg.RunID)
		result, err := exec(ctx, turnCfg)
		if err != nil {
			safeErr := secrets.Redact(err.Error())
			fmt.Fprintf(stdout, "jj loop: stopped: %s\n", safeErr)
			return errors.New(safeErr)
		}
		runDir, err := trustedLoopRunDir(resolved.CWD, turnCfg.RunID, result)
		if err != nil {
			safeErr := secrets.Redact(err.Error())
			fmt.Fprintf(stdout, "jj loop: stopped: %s\n", safeErr)
			return errors.New(safeErr)
		}
		outcome := run.OutcomeForRunDir(runDir)
		if outcome.CommitFailed {
			reason := firstNonEmpty(outcome.Error, "commit failed")
			fmt.Fprintf(stdout, "jj loop: stopped: %s\n", reason)
			return errors.New(reason)
		}
		if loopOutcomeFailed(outcome) {
			reason := firstNonEmpty(outcome.Error, "turn failed")
			fmt.Fprintf(stdout, "jj loop: stopped: %s\n", reason)
			return errors.New(reason)
		}
		if turn == maxTurns {
			fmt.Fprintln(stdout, "jj loop: stopped: max turns reached")
			return nil
		}
		contextText, err := run.BuildContinuationContextFromRunDir(resolved.CWD, runDir, turnCfg.RunID)
		if err != nil {
			safeErr := secrets.Redact(err.Error())
			fmt.Fprintf(stdout, "jj loop: stopped: %s\n", safeErr)
			return errors.New(safeErr)
		}
		nextContext = contextText
	}
	return nil
}

func trustedLoopRunDir(cwd, runID string, result *run.Result) (string, error) {
	store, err := artifact.NewStore(cwd, runID)
	if err != nil {
		return "", err
	}
	reported := ""
	if result != nil {
		if result.RunID != "" && result.RunID != runID {
			return "", errors.New("reported run id does not match the expected run id")
		}
		reported = strings.TrimSpace(result.RunDir)
	}
	if reported == "" {
		return store.RunDir, nil
	}
	reportedAbs, err := filepath.Abs(reported)
	if err != nil {
		return "", err
	}
	expectedAbs, err := filepath.Abs(store.RunDir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(reportedAbs); err == nil {
		reportedAbs = resolved
	}
	if resolved, err := filepath.EvalSymlinks(expectedAbs); err == nil {
		expectedAbs = resolved
	}
	if filepath.Clean(reportedAbs) != filepath.Clean(expectedAbs) {
		return "", errors.New("reported run directory is outside the expected run root")
	}
	return store.RunDir, nil
}

func loopOutcomeFailed(outcome run.RunOutcome) bool {
	status := strings.ToLower(strings.TrimSpace(outcome.Status))
	validation := strings.ToLower(strings.TrimSpace(outcome.ValidationStatus))
	return validation == "failed" || status == run.StatusFailed || status == "cancelled" || strings.HasSuffix(status, "_failed")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func rejectMutuallyExclusiveFlags(cmd *cobra.Command, names ...string) error {
	changed := make([]string, 0, len(names))
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			changed = append(changed, "--"+name)
		}
	}
	if len(changed) <= 1 {
		return nil
	}
	return fmt.Errorf("use only one of %s", humanJoin(changed))
}

func humanJoin(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
	}
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
		Short: "Serve the jj dashboard and run artifacts locally",
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

	cmd.Flags().StringVar(&cfg.CWD, "cwd", "", "directory containing jj state and .jj/runs (defaults to current directory)")
	cmd.Flags().StringVar(&cfg.Addr, "addr", serve.DefaultAddr, "address for the local dashboard server")
	cmd.Flags().StringVar(&cfg.Host, "host", serve.DefaultHost, "host for the local dashboard server")
	cmd.Flags().IntVar(&cfg.Port, "port", serve.DefaultPort, "port for the local dashboard server")
	cmd.Flags().StringVar(&cfg.RunID, "run-id", "", "run id to highlight by default")

	return cmd
}

func newStatusCommand(exec statusExecutor, stdout io.Writer) *cobra.Command {
	cfg := serve.Config{}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print a compact sanitized jj workspace summary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.CWDExplicit = cmd.Flags().Changed("cwd")
			if cwd, err := os.Getwd(); err == nil {
				cfg.ConfigSearchDir = cwd
			} else {
				return err
			}
			summary, err := exec(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			writeStatusSummary(stdout, summary)
			return nil
		},
	}

	cmd.Flags().StringVar(&cfg.CWD, "cwd", "", "directory containing jj state and .jj/runs (defaults to current directory)")
	return cmd
}

func newRunsCommand(exec runsExecutor, stdout io.Writer) *cobra.Command {
	cfg := serve.Config{}

	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Print a compact sanitized recent-run list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.CWDExplicit = cmd.Flags().Changed("cwd")
			if cwd, err := os.Getwd(); err == nil {
				cfg.ConfigSearchDir = cwd
			} else {
				return err
			}
			summary, err := exec(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			writeRunsSummary(stdout, summary)
			return nil
		},
	}

	cmd.Flags().StringVar(&cfg.CWD, "cwd", "", "directory containing jj state and .jj/runs (defaults to current directory)")
	return cmd
}

func writeStatusSummary(w io.Writer, summary serve.StatusSummary) {
	if w == nil {
		w = io.Discard
	}
	task := summary.Task
	fmt.Fprintf(w, "TASK: state=%s total=%d done=%d in_progress=%d pending=%d blocked=%d\n",
		statusOutputValue(task.State, "unknown"),
		maxInt(task.Total, 0),
		maxInt(task.Done, 0),
		maxInt(task.InProgress, 0),
		maxInt(task.Pending, 0),
		maxInt(task.Blocked, 0),
	)
	if task.Next != nil {
		fmt.Fprintf(w, "TASK Next: id=%s status=%s category=%s\n",
			statusOutputValue(task.Next.ID, "unknown"),
			statusOutputValue(task.Next.Status, "unknown"),
			statusOutputValue(task.Next.Category, "unknown"),
		)
	}

	writeCLIRunSummaryLine(w, "Latest Run", cliLatestRunSummaryFields(summary.LatestRun))

	next := summary.NextAction
	fmt.Fprintf(w, "Next Action: state=%s label=%s",
		statusOutputValue(next.State, "unknown"),
		statusOutputValue(next.Label, "Next Action Unknown"),
	)
	if next.RunID != "" {
		fmt.Fprintf(w, " run=%s", statusOutputValue(next.RunID, "unknown"))
	}
	if next.Task != nil {
		fmt.Fprintf(w, " task=%s status=%s category=%s",
			statusOutputValue(next.Task.ID, "unknown"),
			statusOutputValue(next.Task.Status, "unknown"),
			statusOutputValue(next.Task.Category, "unknown"),
		)
	}
	if next.Message != "" {
		fmt.Fprintf(w, " message=%s", statusOutputValue(next.Message, "Next action state is unavailable."))
	}
	fmt.Fprintln(w)

	active := summary.ActiveRuns
	if len(active.Items) == 0 {
		fmt.Fprintf(w, "Active Run: state=%s\n", statusOutputValue(active.State, "none"))
	} else {
		for i, item := range active.Items {
			writeCLIRunSummaryLine(w, cliSummaryItemLabel("Active Run", len(active.Items), i), cliActiveRunSummaryFields(item))
		}
	}

	validation := summary.ValidationStatus
	if len(validation.Items) == 0 {
		fmt.Fprintf(w, "Validation Status: state=%s", statusOutputValue(validation.State, "none"))
		if validation.Message != "" {
			fmt.Fprintf(w, " message=%s", statusOutputValue(validation.Message, "Validation metadata unavailable."))
		}
		fmt.Fprintln(w)
		return
	}
	for i, item := range validation.Items {
		label := cliSummaryItemLabel("Validation Status", len(validation.Items), i)
		fmt.Fprintf(w, "%s: state=%s run=%s",
			label,
			statusOutputValue(item.ValidationState, "unknown"),
			statusOutputValue(item.RunID, "unknown"),
		)
		if item.CountsLabel != "" {
			fmt.Fprintf(w, " counts=%s", statusOutputValue(item.CountsLabel, ""))
		}
		fmt.Fprintf(w, " timestamp=%s\n", statusOutputValue(item.TimestampLabel, "unknown"))
	}
}

func writeRunsSummary(w io.Writer, summary serve.RecentRunsSummary) {
	if w == nil {
		w = io.Discard
	}
	fmt.Fprintf(w, "Runs: state=%s total=%d\n",
		statusOutputValue(summary.State, "none"),
		maxInt(len(summary.Items), 0),
	)
	for i, item := range summary.Items {
		writeCLIRunSummaryLine(w, cliSummaryItemLabel("Run", len(summary.Items), i), cliRecentRunSummaryFields(item))
	}
}

type cliRunSummaryFields struct {
	cliRunSummarySafeLabels
	RunFallback       string
	IncludeValidation bool
}

type cliRunSummarySafeLabels struct {
	State            string
	RunID            string
	Status           string
	ProviderOrResult string
	EvaluationState  string
	ValidationState  string
	TimestampLabel   string
}

type cliRunSummaryFieldOptions struct {
	RunFallback       string
	IncludeValidation bool
}

type cliRunSummaryOutputField struct {
	Key      string
	Value    string
	Fallback string
}

func cliLatestRunSummaryFields(latest serve.StatusLatestRunSummary) cliRunSummaryFields {
	return cliRunSummaryFieldsFromSafeLabels(
		cliRunSummarySafeLabels{
			State:            latest.State,
			RunID:            latest.RunID,
			Status:           latest.Status,
			ProviderOrResult: latest.ProviderOrResult,
			EvaluationState:  latest.EvaluationState,
			TimestampLabel:   latest.TimestampLabel,
		},
		cliRunSummaryFieldOptions{RunFallback: "none"},
	)
}

func cliActiveRunSummaryFields(item serve.StatusActiveRunItem) cliRunSummaryFields {
	return cliRunSummaryFieldsFromSafeLabels(
		cliRunSummarySafeLabels{
			State:            "available",
			RunID:            item.RunID,
			Status:           item.Status,
			ProviderOrResult: item.ProviderOrResult,
			EvaluationState:  item.EvaluationState,
			TimestampLabel:   item.TimestampLabel,
		},
		cliRunSummaryFieldOptions{},
	)
}

func cliRecentRunSummaryFields(item serve.RecentRunItem) cliRunSummaryFields {
	return cliRunSummaryFieldsFromSafeLabels(
		cliRunSummarySafeLabels{
			State:            item.State,
			RunID:            item.RunID,
			Status:           item.Status,
			ProviderOrResult: item.ProviderOrResult,
			EvaluationState:  item.EvaluationState,
			ValidationState:  item.ValidationState,
			TimestampLabel:   item.TimestampLabel,
		},
		cliRunSummaryFieldOptions{IncludeValidation: true},
	)
}

func cliRunSummaryFieldsFromSafeLabels(labels cliRunSummarySafeLabels, opts cliRunSummaryFieldOptions) cliRunSummaryFields {
	return cliRunSummaryFields{
		cliRunSummarySafeLabels: labels,
		RunFallback:             opts.RunFallback,
		IncludeValidation:       opts.IncludeValidation,
	}
}

func writeCLIRunSummaryLine(w io.Writer, label string, fields cliRunSummaryFields) {
	fmt.Fprintf(w, "%s:", label)
	for _, field := range cliRunSummaryOutputFields(fields) {
		fmt.Fprintf(w, " %s=%s", field.Key, statusOutputValue(field.Value, field.Fallback))
	}
	fmt.Fprintln(w)
}

func cliRunSummaryOutputFields(fields cliRunSummaryFields) []cliRunSummaryOutputField {
	runFallback := fields.RunFallback
	if runFallback == "" {
		runFallback = "unknown"
	}
	out := []cliRunSummaryOutputField{
		{Key: "state", Value: fields.State, Fallback: "unknown"},
		{Key: "run", Value: fields.RunID, Fallback: runFallback},
		{Key: "status", Value: fields.Status, Fallback: "unknown"},
		{Key: "provider_or_result", Value: fields.ProviderOrResult, Fallback: "unknown"},
		{Key: "evaluation", Value: fields.EvaluationState, Fallback: "unknown"},
	}
	if fields.IncludeValidation {
		out = append(out, cliRunSummaryOutputField{Key: "validation", Value: fields.ValidationState, Fallback: "unknown"})
	}
	out = append(out, cliRunSummaryOutputField{Key: "timestamp", Value: fields.TimestampLabel, Fallback: "unknown"})
	return out
}

func cliSummaryItemLabel(base string, total, index int) string {
	if total <= 1 {
		return base
	}
	return fmt.Sprintf("%s %d", base, index+1)
}

func statusOutputValue(value, fallback string) string {
	value = strings.TrimSpace(secrets.Redact(value))
	if value == "" {
		return fallback
	}
	lower := strings.ToLower(value)
	for _, token := range []string{
		security.RedactionMarker,
		"sensitive value removed",
		"unsafe value removed",
		"[redacted]",
		"[omitted]",
		"[hidden]",
		"[removed]",
		"{redacted}",
		"{omitted}",
		"{hidden}",
		"{removed}",
		"<redacted>",
		"<omitted>",
		"<hidden>",
		"<removed>",
		"[path]",
	} {
		if strings.Contains(lower, strings.ToLower(token)) {
			return fallback
		}
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
