package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/codex"
	ai "github.com/jungju/jj/internal/openai"
	"github.com/jungju/jj/internal/secrets"
)

const manifestSchemaVersion = "1"

const (
	StatusPlanning       = "planning"
	StatusDryRunComplete = "dry_run_complete"
	StatusImplementing   = "implementing"
	StatusEvaluating     = "evaluating"
	StatusComplete       = "complete"
	StatusPartialFailed  = "partial_failed"
	StatusFailed         = "failed"

	StatusPlanned              = StatusDryRunComplete
	StatusSuccess              = StatusComplete
	StatusPartial              = StatusPartialFailed
	StatusCompleted            = StatusComplete
	StatusPlanningFailed       = StatusFailed
	StatusImplementationFailed = StatusPartialFailed
	StatusEvaluationFailed     = StatusPartialFailed
	statusRunning              = StatusPlanning
)

type PlanningClient interface {
	Draft(context.Context, ai.DraftRequest) (ai.PlanningDraft, []byte, error)
	Merge(context.Context, ai.MergeRequest) (ai.MergeResult, []byte, error)
	Evaluate(context.Context, ai.EvaluationRequest) (ai.EvaluationResult, []byte, error)
}

type CodexRunner interface {
	Run(context.Context, codex.Request) (codex.Result, error)
}

type Result struct {
	RunID  string
	RunDir string
}

type Manifest struct {
	SchemaVersion    string             `json:"schema_version"`
	RunID            string             `json:"run_id"`
	StartedAt        string             `json:"started_at"`
	FinishedAt       string             `json:"finished_at,omitempty"`
	EndedAt          string             `json:"ended_at,omitempty"`
	DurationMS       int64              `json:"duration_ms,omitempty"`
	Status           string             `json:"status"`
	DryRun           bool               `json:"dry_run"`
	AllowNoGit       bool               `json:"allow_no_git"`
	NoGitMode        bool               `json:"no_git_mode"`
	CWD              string             `json:"cwd"`
	PlanPath         string             `json:"plan_path"`
	InputPath        string             `json:"input_path"`
	PlannerProvider  string             `json:"planner_provider"`
	Planner          ManifestPlanner    `json:"planner"`
	Git              ManifestGit        `json:"git"`
	Config           ManifestConfig     `json:"config"`
	Workspace        ManifestWorkspace  `json:"workspace"`
	Artifacts        map[string]string  `json:"artifacts"`
	Risks            []string           `json:"risks,omitempty"`
	Planning         ManifestPlanning   `json:"planning"`
	Codex            ManifestCodex      `json:"codex"`
	Evaluation       ManifestEvaluation `json:"evaluation"`
	Commit           ManifestCommit     `json:"commit,omitempty"`
	RiskCount        int                `json:"risk_count"`
	Errors           []string           `json:"errors"`
	FailurePhase     string             `json:"failure_phase,omitempty"`
	FailureMessage   string             `json:"failure_message,omitempty"`
	FailedStage      string             `json:"failed_stage,omitempty"`
	ErrorSummary     string             `json:"error_summary,omitempty"`
	RedactionApplied bool               `json:"redaction_applied"`
}

type ManifestGit struct {
	Available        bool     `json:"available"`
	IsRepo           bool     `json:"is_repo"`
	Root             string   `json:"root,omitempty"`
	Branch           string   `json:"branch,omitempty"`
	Head             string   `json:"head,omitempty"`
	InitialStatus    string   `json:"initial_status,omitempty"`
	FinalStatus      string   `json:"final_status,omitempty"`
	DirtyBefore      bool     `json:"dirty_before"`
	DirtyAfter       bool     `json:"dirty_after"`
	Dirty            bool     `json:"dirty"`
	NoGit            bool     `json:"no_git"`
	BaselinePath     string   `json:"baseline_path,omitempty"`
	BaselineTextPath string   `json:"baseline_text_path,omitempty"`
	StatusBeforePath string   `json:"status_before_path,omitempty"`
	StatusAfterPath  string   `json:"status_after_path,omitempty"`
	StatusPath       string   `json:"status_path,omitempty"`
	DiffPath         string   `json:"diff_path,omitempty"`
	DiffStatPath     string   `json:"diff_stat_path,omitempty"`
	DiffSummaryPath  string   `json:"diff_summary_path,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type ManifestPlanner struct {
	Provider  string            `json:"provider"`
	Model     string            `json:"model,omitempty"`
	Artifacts map[string]string `json:"artifacts,omitempty"`
}

type ManifestConfig struct {
	PlanningAgents int    `json:"planning_agents"`
	OpenAIModel    string `json:"openai_model"`
	CodexModel     string `json:"codex_model,omitempty"`
	CodexBin       string `json:"codex_bin,omitempty"`
	ConfigFile     string `json:"config_file,omitempty"`
	OpenAIKeyEnv   string `json:"openai_api_key_env,omitempty"`
	OpenAIKeySet   bool   `json:"openai_api_key_present"`
	AllowNoGit     bool   `json:"allow_no_git"`
	SpecDoc        string `json:"spec_doc"`
	TaskDoc        string `json:"task_doc"`
	EvalDoc        string `json:"eval_doc"`
	SpecPath       string `json:"spec_path"`
	TaskPath       string `json:"task_path"`
	EvalPath       string `json:"eval_path"`
}

type ManifestWorkspace struct {
	SpecPath    string `json:"spec_path"`
	TaskPath    string `json:"task_path"`
	EvalPath    string `json:"eval_path"`
	SpecWritten bool   `json:"spec_written"`
	TaskWritten bool   `json:"task_written"`
	EvalWritten bool   `json:"eval_written"`
}

type ManifestPlanning struct {
	Agents []ManifestPlanningAgent `json:"agents"`
}

type ManifestPlanningAgent struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Artifact string `json:"artifact,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ManifestCodex struct {
	Ran         bool   `json:"ran"`
	Skipped     bool   `json:"skipped"`
	Status      string `json:"status,omitempty"`
	Model       string `json:"model,omitempty"`
	ExitCode    int    `json:"exit_code"`
	DurationMS  int64  `json:"duration_ms"`
	EventsPath  string `json:"events_path,omitempty"`
	SummaryPath string `json:"summary_path,omitempty"`
	ExitPath    string `json:"exit_path,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ManifestEvaluation struct {
	Ran                   bool     `json:"ran"`
	Skipped               bool     `json:"skipped"`
	Status                string   `json:"status,omitempty"`
	Result                string   `json:"result,omitempty"`
	Summary               string   `json:"summary,omitempty"`
	Score                 int      `json:"score"`
	EvalPath              string   `json:"eval_path,omitempty"`
	Risks                 []string `json:"risks,omitempty"`
	RiskCount             int      `json:"risk_count,omitempty"`
	FailureCount          int      `json:"failure_count,omitempty"`
	RecommendedNextAction string   `json:"recommended_next_action,omitempty"`
	Error                 string   `json:"error,omitempty"`
}

type ManifestCommit struct {
	Ran     bool   `json:"ran"`
	Status  string `json:"status,omitempty"`
	SHA     string `json:"sha,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func Execute(ctx context.Context, cfg Config) (*Result, error) {
	started := time.Now().UTC()
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	var err error
	cfg, err = ResolveConfig(cfg)
	if err != nil {
		return nil, validationError(err)
	}
	if strings.TrimSpace(cfg.CWD) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cfg.CWD = cwd
	}
	absCWD, err := filepath.Abs(cfg.CWD)
	if err != nil {
		return nil, validationError(err)
	}
	cfg.CWD = absCWD
	if err := validateCWD(cfg.CWD); err != nil {
		return nil, validationError(err)
	}
	if err := validateResolvedConfig(cfg); err != nil {
		return nil, validationError(err)
	}
	if strings.TrimSpace(cfg.RunID) == "" {
		cfg.RunID = artifact.NewRunID(time.Now())
	}
	specRel := docRelPath(cfg.SpecDoc, cfg.SpecDocPathMode)
	taskRel := docRelPath(cfg.TaskDoc, cfg.TaskDocPathMode)
	evalRel := docRelPath(cfg.EvalDoc, cfg.EvalDocPathMode)

	fmt.Fprintf(cfg.Stdout, "jj: reading %s\n", cfg.PlanPath)
	plan, planPath, err := LoadPlan(cfg.PlanPath, cfg.CWD)
	if err != nil {
		return nil, validationError(err)
	}
	fmt.Fprintln(cfg.Stdout, "jj: checking git workspace")
	gitState, err := InspectGit(ctx, cfg.CWD, cfg.GitRunner)
	if err != nil {
		return nil, fmt.Errorf("inspect git state: %w", err)
	}
	if !gitState.Available && !cfg.AllowNoGit {
		return nil, validationError(errors.New("target directory is not a git repository; use --allow-no-git to override"))
	}

	store, err := artifact.NewStore(cfg.CWD, cfg.RunID)
	if err != nil {
		return nil, validationError(err)
	}
	fmt.Fprintf(cfg.Stdout, "jj: creating run directory %s\n", store.RunDir)
	if err := store.Init(); err != nil {
		return nil, validationError(err)
	}
	gitWarnings := []string(nil)
	if !gitState.Available {
		gitWarnings = append(gitWarnings, "git metadata unavailable because --allow-no-git was used outside a git repository")
	}

	manifest := Manifest{
		SchemaVersion: manifestSchemaVersion,
		RunID:         cfg.RunID,
		StartedAt:     started.Format(time.RFC3339),
		Status:        StatusPlanning,
		DryRun:        cfg.DryRun,
		AllowNoGit:    cfg.AllowNoGit,
		NoGitMode:     !gitState.Available && cfg.AllowNoGit,
		CWD:           cfg.CWD,
		PlanPath:      planPath,
		InputPath:     planPath,
		Git: ManifestGit{
			Available:     gitState.Available,
			IsRepo:        gitState.Available,
			Root:          gitState.Root,
			Branch:        gitState.Branch,
			Head:          gitState.Head,
			InitialStatus: gitState.InitialStatus,
			DirtyBefore:   gitState.Dirty,
			DirtyAfter:    gitState.Dirty,
			Dirty:         gitState.Dirty,
			NoGit:         !gitState.Available && cfg.AllowNoGit,
			Warnings:      gitWarnings,
		},
		Config: ManifestConfig{
			PlanningAgents: cfg.PlanningAgents,
			OpenAIModel:    cfg.OpenAIModel,
			CodexModel:     cfg.CodexModel,
			CodexBin:       cfg.CodexBin,
			ConfigFile:     cfg.ConfigFile,
			OpenAIKeyEnv:   cfg.OpenAIAPIKeyEnv,
			OpenAIKeySet:   strings.TrimSpace(cfg.OpenAIAPIKey) != "",
			AllowNoGit:     cfg.AllowNoGit,
			SpecDoc:        cfg.SpecDoc,
			TaskDoc:        cfg.TaskDoc,
			EvalDoc:        cfg.EvalDoc,
			SpecPath:       specRel,
			TaskPath:       taskRel,
			EvalPath:       evalRel,
		},
		Workspace: ManifestWorkspace{
			SpecPath: specRel,
			TaskPath: taskRel,
			EvalPath: evalRel,
		},
		Commit:           ManifestCommit{Ran: false, Status: "skipped"},
		Artifacts:        map[string]string{},
		RedactionApplied: true,
	}
	var manifestMu sync.Mutex
	currentStage := StatusPlanning
	record := func(name, path string) {
		manifestMu.Lock()
		defer manifestMu.Unlock()
		manifest.Artifacts[name] = relArtifactPath(store, path)
	}
	recordRel := func(name, rel string) {
		manifestMu.Lock()
		defer manifestMu.Unlock()
		manifest.Artifacts[name] = filepath.ToSlash(rel)
	}
	addError := func(err error) {
		if err == nil {
			return
		}
		manifestMu.Lock()
		defer manifestMu.Unlock()
		manifest.Errors = append(manifest.Errors, redactSecrets(err.Error()))
	}
	addRisks := func(items ...string) {
		manifestMu.Lock()
		defer manifestMu.Unlock()
		for _, item := range items {
			item = strings.TrimSpace(redactSecrets(item))
			if item == "" {
				continue
			}
			seen := false
			for _, existing := range manifest.Risks {
				if existing == item {
					seen = true
					break
				}
			}
			if !seen {
				manifest.Risks = append(manifest.Risks, item)
			}
		}
	}
	writeManifest := func(status string, final bool) {
		manifestMu.Lock()
		defer manifestMu.Unlock()
		manifest.Status = status
		if final {
			manifest.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			manifest.EndedAt = manifest.FinishedAt
			manifest.DurationMS = time.Since(started).Milliseconds()
		}
		manifest.RiskCount = len(manifest.Risks)
		manifest.Artifacts["manifest"] = "manifest.json"
		if manifest.Planner.Provider == "" {
			manifest.Planner.Provider = manifest.PlannerProvider
		}
		if manifest.Planner.Artifacts == nil {
			manifest.Planner.Artifacts = map[string]string{}
		}
		for name, path := range manifest.Artifacts {
			if strings.HasPrefix(name, "planning") {
				manifest.Planner.Artifacts[name] = path
			}
		}
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return
		}
		data = append([]byte(redactSecrets(string(data))), '\n')
		_, _ = store.WriteFile("manifest.json", data)
	}
	workspaceModified := false
	fail := func(status string, err error) (*Result, error) {
		addError(err)
		if status == "" {
			status = StatusFailed
		}
		manifestMu.Lock()
		if !manifest.Codex.Ran && !manifest.Codex.Skipped {
			manifest.Codex.Skipped = true
			manifest.Codex.Status = "skipped"
			manifest.Codex.Model = cfg.CodexModel
			manifest.Codex.Error = "skipped because " + currentStage + " did not complete"
		}
		if !manifest.Evaluation.Ran && !manifest.Evaluation.Skipped {
			manifest.Evaluation.Skipped = true
			manifest.Evaluation.Status = "not_run"
			manifest.Evaluation.Result = "SKIPPED"
			manifest.Evaluation.Error = "skipped because " + currentStage + " did not complete"
		}
		if manifest.FailedStage == "" {
			manifest.FailedStage = currentStage
		}
		if manifest.FailurePhase == "" {
			manifest.FailurePhase = currentStage
		}
		if manifest.ErrorSummary == "" && err != nil {
			manifest.ErrorSummary = redactSecrets(err.Error())
		}
		if manifest.FailureMessage == "" && err != nil {
			manifest.FailureMessage = redactSecrets(err.Error())
		}
		manifestMu.Unlock()
		writeManifest(status, true)
		safeErr := redactSecrets(err.Error())
		fmt.Fprintf(cfg.Stderr, "jj: failed: %s\nstatus=%s\nworkspace_modified=%t\npartial_artifacts=%s\n", safeErr, status, workspaceModified, store.RunDir)
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, errors.New(safeErr)
	}

	originalPlan := plan
	continuationContext := strings.TrimSpace(redactSecrets(cfg.AdditionalPlanContext))
	if continuationContext != "" {
		plan = strings.TrimRight(originalPlan, "\n") + "\n\n---\n\n# jj Continuation Context\n\n" + continuationContext + "\n"
	}

	if p, err := store.WriteString("input-original.md", originalPlan); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("input_original", p)
	}
	if p, err := store.WriteString("input-context.md", continuationContext+"\n"); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("input_context", p)
	}
	if p, err := store.WriteString("input.md", plan); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("input", p)
	}
	writeManifest(StatusPlanning, false)
	if p, err := writeRedactedJSON(store, "git/baseline.json", gitState); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("git_baseline", p)
		manifest.Git.BaselinePath = "git/baseline.json"
	}
	if p, err := store.WriteString("git/baseline.txt", renderGitBaseline(gitState)); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("git_baseline_txt", p)
		manifest.Git.BaselineTextPath = "git/baseline.txt"
	}
	statusBefore := gitState.InitialStatus
	if !gitState.Available {
		statusBefore = "git unavailable"
	}
	if p, err := store.WriteString("git/status.before.txt", redactSecrets(statusBefore)+"\n"); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("git_status_before", p)
		manifest.Git.StatusBeforePath = "git/status.before.txt"
	}
	writeManifest(StatusPlanning, false)

	plannerSelection, err := selectPlanner(cfg, store, record)
	if err != nil {
		return fail(StatusPlanningFailed, err)
	}
	planner := plannerSelection.Client
	manifest.PlannerProvider = plannerSelection.Provider
	manifest.Planner = ManifestPlanner{
		Provider:  plannerSelection.Provider,
		Model:     plannerModel(plannerSelection.Provider, cfg),
		Artifacts: map[string]string{},
	}
	writeManifest(StatusPlanning, false)

	fmt.Fprintf(cfg.Stdout, "jj: running %d planning agents\n", cfg.PlanningAgents)
	drafts, agentResults, err := runPlanningAgents(ctx, planner, store, cfg.OpenAIModel, plan, cfg.PlanningAgents, record)
	manifest.Planning.Agents = agentResults
	if err != nil {
		return fail(StatusPlanningFailed, err)
	}
	for _, draft := range drafts {
		addRisks(draft.Risks...)
	}
	writeManifest(StatusPlanning, false)

	fmt.Fprintln(cfg.Stdout, "jj: merging planning outputs")
	merged, raw, err := planner.Merge(ctx, ai.MergeRequest{
		Model:  cfg.OpenAIModel,
		Plan:   plan,
		Drafts: drafts,
	})
	if err != nil {
		if len(raw) > 0 {
			if p, writeErr := store.WriteFile("planning/raw_response_merge.txt", redactBytes(raw)); writeErr == nil {
				record("planning_merge_raw_response", p)
			}
		}
		return fail(StatusPlanningFailed, fmt.Errorf("merge planning outputs: %w", err))
	}
	if err := validateMergeResult(merged); err != nil {
		if len(raw) > 0 {
			if p, writeErr := store.WriteFile("planning/raw_response_merge.txt", redactBytes(raw)); writeErr == nil {
				record("planning_merge_raw_response", p)
			}
		}
		return fail(StatusPlanningFailed, fmt.Errorf("merge planning outputs: %w", err))
	}
	merged.Spec = ensureSpecSections(merged.Spec, plan)
	merged.Task = ensureTaskSections(merged.Task, plan)
	if p, err := store.WriteFile("planning/merge.json", redactBytes(raw)); err != nil {
		return fail(StatusPlanningFailed, err)
	} else {
		record("planning_merge", p)
	}
	if p, err := store.WriteFile("planning/merged.json", redactBytes(raw)); err != nil {
		return fail(StatusPlanningFailed, err)
	} else {
		record("planning_merged", p)
	}
	if p, err := writeRedactedJSON(store, "planning/planning.json", normalizedPlanning(plannerSelection.Provider, drafts, merged)); err != nil {
		return fail(StatusPlanningFailed, err)
	} else {
		record("planning", p)
	}
	specArtifact, err := store.WriteString("docs/SPEC.md", redactSecrets(merged.Spec))
	if err != nil {
		return fail(StatusPlanningFailed, err)
	}
	taskArtifact, err := store.WriteString("docs/TASK.md", redactSecrets(merged.Task))
	if err != nil {
		return fail(StatusPlanningFailed, err)
	}
	record("spec", specArtifact)
	record("task", taskArtifact)
	writeManifest(StatusPlanning, false)

	if cfg.DryRun {
		evalMarkdown := renderDryRunEvaluation(cfg.RunID, plannerSelection.Provider, plan, merged.Spec, merged.Task)
		evalArtifact, err := store.WriteString("docs/EVAL.md", evalMarkdown)
		if err != nil {
			return fail(StatusPartial, err)
		}
		record("eval", evalArtifact)
		manifest.Codex = ManifestCodex{Ran: false, Skipped: true, Status: "skipped", Model: cfg.CodexModel}
		manifest.Evaluation = ManifestEvaluation{
			Ran:                   true,
			Skipped:               false,
			Status:                "not_run",
			Result:                "SKIPPED",
			Summary:               "Implementation and verification were skipped in dry-run mode.",
			Score:                 0,
			EvalPath:              "docs/EVAL.md",
			Risks:                 redactList(manifest.Risks),
			RiskCount:             len(manifest.Risks),
			RecommendedNextAction: "Review generated SPEC/TASK before running implementation.",
		}
		fmt.Fprintf(cfg.Stdout, "jj: dry run complete\n")
		writeManifest(StatusDryRunComplete, true)
		fmt.Fprintf(cfg.Stdout, "run_id=%s\nrun_dir=%s\nprovider=%s\nspec=%s\ntask=%s\nimplementation=skipped\nstatus=%s\nreview=jj serve --cwd %s\n", cfg.RunID, store.RunDir, plannerSelection.Provider, filepath.ToSlash(filepath.Join(store.RunDir, "docs", "SPEC.md")), filepath.ToSlash(filepath.Join(store.RunDir, "docs", "TASK.md")), StatusDryRunComplete, cfg.CWD)
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, nil
	}

	currentStage = StatusImplementing
	specPath := filepath.Join(cfg.CWD, filepath.FromSlash(specRel))
	taskPath := filepath.Join(cfg.CWD, filepath.FromSlash(taskRel))
	if err := writeWorktreeFile(specPath, []byte(redactSecrets(merged.Spec))); err != nil {
		return fail(StatusPartial, fmt.Errorf("write %s: %w", specRel, err))
	}
	workspaceModified = true
	manifest.Workspace.SpecWritten = true
	if err := writeWorktreeFile(taskPath, []byte(redactSecrets(merged.Task))); err != nil {
		return fail(StatusPartial, fmt.Errorf("write %s: %w", taskRel, err))
	}
	manifest.Workspace.TaskWritten = true
	fmt.Fprintf(cfg.Stdout, "jj: wrote %s and %s\n", specRel, taskRel)
	recordRel("spec_worktree", specRel)
	recordRel("task_worktree", taskRel)
	writeManifest(StatusImplementing, false)

	runner := cfg.CodexRunner
	if runner == nil {
		runner = codex.Runner{}
	}
	eventsPath, _ := store.Path("codex/events.jsonl")
	summaryPath, _ := store.Path("codex/summary.md")
	fmt.Fprintln(cfg.Stdout, "jj: running codex exec")
	codexResult, codexErr := runner.Run(ctx, codex.Request{
		Bin:               cfg.CodexBin,
		CWD:               cfg.CWD,
		Model:             cfg.CodexModel,
		Prompt:            codexPrompt(specRel, taskRel),
		EventsPath:        eventsPath,
		OutputLastMessage: summaryPath,
		AllowNoGit:        cfg.AllowNoGit,
	})
	if err := ensureCodexArtifacts(eventsPath, summaryPath, codexResult, codexErr); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	if err := redactFile(eventsPath); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	if err := redactFile(summaryPath); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	codexStatus := "success"
	if codexErr != nil {
		codexStatus = "failed"
	}
	manifest.Codex = ManifestCodex{
		Ran:         true,
		Skipped:     false,
		Status:      codexStatus,
		Model:       cfg.CodexModel,
		ExitCode:    codexResult.ExitCode,
		DurationMS:  codexResult.DurationMS,
		EventsPath:  "codex/events.jsonl",
		SummaryPath: "codex/summary.md",
	}
	record("codex_events", eventsPath)
	record("codex_summary", summaryPath)
	if p, err := writeRedactedJSON(store, "codex/exit.json", codexExitArtifact(codexStatus, codexResult, codexErr)); err != nil {
		return fail(StatusImplementationFailed, err)
	} else {
		record("codex_exit", p)
		manifest.Codex.ExitPath = "codex/exit.json"
	}
	if strings.TrimSpace(codexResult.Summary) == "" {
		if data, readErr := os.ReadFile(summaryPath); readErr == nil {
			codexResult.Summary = string(data)
		}
	}
	codexResult.Summary = redactSecrets(codexResult.Summary)
	manifest.Codex.Summary = truncateString(codexResult.Summary, 2000)
	if codexErr != nil {
		safeCodexErr := redactSecrets(codexErr.Error())
		manifest.Codex.Error = safeCodexErr
		if manifest.FailedStage == "" {
			manifest.FailedStage = StatusImplementing
		}
		if manifest.FailurePhase == "" {
			manifest.FailurePhase = StatusImplementing
		}
		if manifest.ErrorSummary == "" {
			manifest.ErrorSummary = safeCodexErr
		}
		if manifest.FailureMessage == "" {
			manifest.FailureMessage = safeCodexErr
		}
		addError(codexErr)
		addRisks("Codex implementation failed: " + safeCodexErr)
		codexResult.Summary = strings.TrimSpace(codexResult.Summary + "\n\nCodex error: " + safeCodexErr)
		fmt.Fprintf(cfg.Stderr, "jj: codex failed, continuing to evaluation: %s\n", safeCodexErr)
	}
	writeManifest(StatusImplementing, false)

	recordGitDiff := func(diff GitDiff) error {
		manifest.Git.FinalStatus = diff.Status
		manifest.Git.DirtyAfter = dirtyFromGitStatus(diff.Status)
		if p, err := store.WriteString("git/diff.patch", diff.Full+"\n"); err != nil {
			return err
		} else {
			record("git_diff", p)
			manifest.Git.DiffPath = "git/diff.patch"
		}
		if p, err := store.WriteString("git/status.txt", diff.Status+"\n"); err != nil {
			return err
		} else {
			record("git_status", p)
			manifest.Git.StatusPath = "git/status.txt"
		}
		if p, err := store.WriteString("git/status.after.txt", diff.Status+"\n"); err != nil {
			return err
		} else {
			record("git_status_after", p)
			manifest.Git.StatusAfterPath = "git/status.after.txt"
		}
		if p, err := store.WriteString("git/diff-summary.txt", diff.Markdown()); err != nil {
			return err
		} else {
			record("git_diff_summary", p)
			manifest.Git.DiffSummaryPath = "git/diff-summary.txt"
		}
		if p, err := store.WriteString("git/diff.stat.txt", emptyAsNone(diff.Stat)+"\n"); err != nil {
			return err
		} else {
			record("git_diff_stat", p)
			manifest.Git.DiffStatPath = "git/diff.stat.txt"
		}
		return nil
	}

	fmt.Fprintln(cfg.Stdout, "jj: capturing git diff")
	diff, err := CaptureGitDiff(ctx, cfg.CWD, gitState.Available, cfg.GitRunner)
	if err != nil {
		return fail(StatusImplementationFailed, fmt.Errorf("capture git diff: %w", err))
	}
	diff = redactGitDiff(diff)
	if err := recordGitDiff(diff); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	writeManifest(StatusImplementing, false)
	codexEvents := ""
	if data, err := os.ReadFile(eventsPath); err == nil {
		codexEvents = string(data)
	}

	fmt.Fprintln(cfg.Stdout, "jj: evaluating result")
	currentStage = StatusEvaluating
	writeManifest(StatusEvaluating, false)
	eval, rawEval, evalErr := planner.Evaluate(ctx, ai.EvaluationRequest{
		Model:        cfg.OpenAIModel,
		Plan:         plan,
		Spec:         merged.Spec,
		Task:         merged.Task,
		CodexSummary: codexResult.Summary,
		CodexEvents:  codexEvents,
		GitDiff:      diff.Markdown(),
	})
	if evalErr != nil {
		manifest.Evaluation = ManifestEvaluation{
			Ran:                   true,
			Skipped:               false,
			Status:                "fail",
			Result:                "FAIL",
			Summary:               "Evaluation failed after implementation evidence was collected.",
			Error:                 redactSecrets(evalErr.Error()),
			EvalPath:              "docs/EVAL.md",
			Risks:                 []string{"Evaluation failed and requires manual review."},
			RiskCount:             1,
			FailureCount:          1,
			RecommendedNextAction: "Review implementation, git diff, and evaluator failure manually.",
		}
		evalMarkdown := renderEvaluationFailure(evalErr, codexResult.Summary, diff.Markdown())
		if p, writeErr := store.WriteString("docs/EVAL.md", evalMarkdown); writeErr == nil {
			record("eval", p)
		}
		evalPath := filepath.Join(cfg.CWD, filepath.FromSlash(evalRel))
		if writeErr := writeWorktreeFile(evalPath, []byte(evalMarkdown)); writeErr == nil {
			manifest.Workspace.EvalWritten = true
			recordRel("eval_worktree", evalRel)
		}
		if finalDiff, diffErr := CaptureGitDiff(ctx, cfg.CWD, gitState.Available, cfg.GitRunner); diffErr != nil {
			addError(fmt.Errorf("capture final git diff after evaluation failure: %w", diffErr))
		} else if recordErr := recordGitDiff(redactGitDiff(finalDiff)); recordErr != nil {
			addError(fmt.Errorf("write final git diff after evaluation failure: %w", recordErr))
		}
		return fail(StatusEvaluationFailed, evalErr)
	}
	ai.NormalizeEvaluation(&eval)
	addRisks(eval.Risks...)
	manifest.Evaluation = ManifestEvaluation{
		Ran:                   true,
		Skipped:               false,
		Status:                evaluationStatus(eval.Result),
		Result:                eval.Result,
		Summary:               redactSecrets(eval.Summary),
		Score:                 eval.Score,
		EvalPath:              "docs/EVAL.md",
		Risks:                 redactList(eval.Risks),
		RiskCount:             len(nonEmptyPlanningItems(eval.Risks)),
		FailureCount:          len(nonEmptyPlanningItems(eval.Regressions)),
		RecommendedNextAction: firstRecommendedAction(eval.RecommendedFollowups),
	}
	if p, err := store.WriteFile("planning/evaluation.json", redactBytes(rawEval)); err != nil {
		return fail(StatusEvaluationFailed, err)
	} else {
		record("evaluation_json", p)
	}
	evalMarkdown := renderEvaluation(eval, merged.Spec, merged.Task)
	if p, err := store.WriteString("docs/EVAL.md", evalMarkdown); err != nil {
		return fail(StatusEvaluationFailed, err)
	} else {
		record("eval", p)
	}
	evalPath := filepath.Join(cfg.CWD, filepath.FromSlash(evalRel))
	if err := writeWorktreeFile(evalPath, []byte(evalMarkdown)); err != nil {
		return fail(StatusEvaluationFailed, fmt.Errorf("write %s: %w", evalRel, err))
	}
	manifest.Workspace.EvalWritten = true
	recordRel("eval_worktree", evalRel)
	if finalDiff, err := CaptureGitDiff(ctx, cfg.CWD, gitState.Available, cfg.GitRunner); err != nil {
		return fail(StatusEvaluationFailed, fmt.Errorf("capture final git diff: %w", err))
	} else {
		finalDiff = redactGitDiff(finalDiff)
		if err := recordGitDiff(finalDiff); err != nil {
			return fail(StatusEvaluationFailed, err)
		}
	}
	status := StatusCompleted
	if codexErr != nil {
		status = StatusImplementationFailed
	} else if eval.Result != "PASS" {
		status = StatusPartial
	}
	writeManifest(status, true)
	fmt.Fprintf(cfg.Stdout, "jj: done\nrun_id=%s\nrun_dir=%s\nprovider=%s\nspec=%s\ntask=%s\neval=%s\nimplementation=ran\nstatus=%s\ncodex_exit_code=%d\nreview=jj serve --cwd %s\n", cfg.RunID, store.RunDir, plannerSelection.Provider, specRel, taskRel, evalRel, status, manifest.Codex.ExitCode, cfg.CWD)
	if codexErr != nil {
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, errors.New(redactSecrets(codexErr.Error()))
	}
	return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, nil
}

func writeWorktreeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return artifact.AtomicWriteFile(path, data, 0o644)
}

func plannerModel(provider string, cfg Config) string {
	switch provider {
	case plannerProviderCodex:
		return cfg.CodexModel
	default:
		return cfg.OpenAIModel
	}
}

func renderGitBaseline(state GitState) string {
	if !state.Available {
		return "git unavailable\n"
	}
	var b strings.Builder
	b.WriteString("repo: ")
	b.WriteString(state.Root)
	b.WriteByte('\n')
	b.WriteString("branch: ")
	b.WriteString(state.Branch)
	b.WriteByte('\n')
	b.WriteString("head: ")
	b.WriteString(state.Head)
	b.WriteByte('\n')
	b.WriteString("dirty: ")
	b.WriteString(fmt.Sprintf("%t", state.Dirty))
	b.WriteString("\n\nstatus.before:\n")
	if strings.TrimSpace(state.InitialStatus) == "" {
		b.WriteString("(none)\n")
	} else {
		b.WriteString(state.InitialStatus)
		b.WriteByte('\n')
	}
	return redactSecrets(b.String())
}

type codexExitRecord struct {
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

func codexExitArtifact(status string, result codex.Result, err error) codexExitRecord {
	record := codexExitRecord{
		Status:     status,
		ExitCode:   result.ExitCode,
		DurationMS: result.DurationMS,
	}
	if err != nil {
		record.Error = redactSecrets(err.Error())
	}
	return record
}

func dirtyFromGitStatus(status string) bool {
	status = strings.TrimSpace(status)
	return status != "" && status != "git unavailable"
}

func evaluationStatus(result string) string {
	switch strings.ToUpper(strings.TrimSpace(result)) {
	case "PASS":
		return "pass"
	case "FAIL":
		return "fail"
	case "PARTIAL":
		return "warn"
	default:
		return "not_run"
	}
}

func redactFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	redacted := []byte(redactSecrets(string(data)))
	if string(redacted) == string(data) {
		return nil
	}
	return artifact.AtomicWriteFile(path, redacted, 0o644)
}

func ensureCodexArtifacts(eventsPath, summaryPath string, result codex.Result, runErr error) error {
	if err := ensureFileIfMissing(eventsPath, `{"type":"notice","message":"codex produced no event log"}`+"\n"); err != nil {
		return err
	}
	summary := strings.TrimSpace(result.Summary)
	if runErr != nil {
		errText := redactSecrets(runErr.Error())
		if summary == "" {
			summary = "Codex failed before producing a summary."
		}
		if !strings.Contains(summary, errText) {
			summary += "\n\nCodex error: " + errText
		}
	}
	if summary == "" {
		summary = "Codex completed without producing a summary."
	}
	return ensureFileIfMissing(summaryPath, redactSecrets(summary)+"\n")
}

func ensureFileIfMissing(path, content string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return artifact.AtomicWriteFile(path, []byte(redactSecrets(content)), 0o644)
}

func redactBytes(data []byte) []byte {
	return []byte(redactSecrets(string(data)))
}

func writeRedactedJSON(store artifact.Store, rel string, value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	data = append([]byte(redactSecrets(string(data))), '\n')
	return store.WriteFile(rel, data)
}

type normalizedPlanningResult struct {
	Provider           string             `json:"provider"`
	Spec               string             `json:"spec"`
	Task               string             `json:"task"`
	Risks              []string           `json:"risks"`
	Assumptions        []string           `json:"assumptions"`
	AcceptanceCriteria []string           `json:"acceptance_criteria"`
	TestGuidance       []string           `json:"test_guidance"`
	Drafts             []ai.PlanningDraft `json:"drafts"`
	Merge              ai.MergeResult     `json:"merge"`
}

func normalizedPlanning(provider string, drafts []ai.PlanningDraft, merged ai.MergeResult) normalizedPlanningResult {
	out := normalizedPlanningResult{
		Provider: provider,
		Spec:     merged.Spec,
		Task:     merged.Task,
		Drafts:   drafts,
		Merge:    merged,
	}
	seenRisks := map[string]bool{}
	seenAssumptions := map[string]bool{}
	seenAcceptance := map[string]bool{}
	seenTests := map[string]bool{}
	for _, draft := range drafts {
		out.Risks = appendUniquePlanning(out.Risks, seenRisks, draft.Risks...)
		out.Assumptions = appendUniquePlanning(out.Assumptions, seenAssumptions, draft.Assumptions...)
		out.AcceptanceCriteria = appendUniquePlanning(out.AcceptanceCriteria, seenAcceptance, draft.AcceptanceCriteria...)
		out.TestGuidance = appendUniquePlanning(out.TestGuidance, seenTests, draft.TestingGuidance...)
		out.TestGuidance = appendUniquePlanning(out.TestGuidance, seenTests, draft.TestPlan...)
	}
	return out
}

func appendUniquePlanning(dst []string, seen map[string]bool, items ...string) []string {
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		dst = append(dst, item)
	}
	return dst
}

func redactList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(redactSecrets(item))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstRecommendedAction(items []string) string {
	for _, item := range items {
		item = strings.TrimSpace(redactSecrets(item))
		if item != "" {
			return item
		}
	}
	return "Review generated artifacts and git diff before considering the run complete."
}

func redactGitDiff(diff GitDiff) GitDiff {
	diff.Status = redactSecrets(diff.Status)
	diff.Stat = redactSecrets(diff.Stat)
	diff.NameStatus = redactSecrets(diff.NameStatus)
	diff.Full = redactSecrets(diff.Full)
	return diff
}

func validateCWD(cwd string) error {
	info, err := os.Stat(cwd)
	if err != nil {
		return fmt.Errorf("cwd does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cwd is not a directory: %s", cwd)
	}
	return nil
}

func runPlanningAgents(ctx context.Context, planner PlanningClient, store artifact.Store, model, plan string, count int, record func(string, string)) ([]ai.PlanningDraft, []ManifestPlanningAgent, error) {
	agents := planningAgents(count)
	drafts := make([]ai.PlanningDraft, len(agents))
	results := make([]ManifestPlanningAgent, len(agents))
	errs := make(chan error, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		i, agent := i, agent
		results[i] = ManifestPlanningAgent{Name: agent.Name, Status: "failed"}
		wg.Add(1)
		go func() {
			defer wg.Done()
			draft, raw, err := planner.Draft(ctx, ai.DraftRequest{
				Model: model,
				Plan:  plan,
				Agent: agent,
			})
			name := fmt.Sprintf("planning/%s.json", agent.Name)
			if err != nil {
				errText := fmt.Sprintf("agent %s failed: %v", agent.Name, err)
				if len(raw) > 0 {
					errText += "\n\nraw response excerpt:\n" + truncateString(string(raw), 4000)
					if path, writeErr := store.WriteFile(fmt.Sprintf("planning/raw_response_%s.txt", agent.Name), redactBytes(raw)); writeErr == nil {
						record("planning_"+agent.Name+"_raw_response", path)
					}
				}
				path, writeErr := store.WriteString(fmt.Sprintf("planning/%s.error.txt", agent.Name), redactSecrets(errText)+"\n")
				if writeErr == nil {
					record("planning_"+agent.Name+"_error", path)
					results[i].Artifact = relArtifactPath(store, path)
				}
				results[i].Error = redactSecrets(err.Error())
				errs <- fmt.Errorf("%s planning failed: %w", agent.Name, err)
				return
			}
			if strings.TrimSpace(draft.Agent) == "" {
				draft.Agent = agent.Name
			}
			if err := validatePlanningDraft(agent.Name, draft); err != nil {
				errText := fmt.Sprintf("agent %s returned incomplete planning draft: %v", agent.Name, err)
				path, writeErr := store.WriteString(fmt.Sprintf("planning/%s.error.txt", agent.Name), redactSecrets(errText)+"\n")
				if writeErr == nil {
					record("planning_"+agent.Name+"_error", path)
					results[i].Artifact = relArtifactPath(store, path)
				}
				results[i].Error = redactSecrets(err.Error())
				errs <- fmt.Errorf("%s planning failed: %w", agent.Name, err)
				return
			}
			path, err := store.WriteFile(name, redactBytes(raw))
			if err != nil {
				errs <- err
				results[i].Error = redactSecrets(err.Error())
				return
			}
			record("planning_"+agent.Name, path)
			results[i] = ManifestPlanningAgent{Name: agent.Name, Status: "success", Artifact: relArtifactPath(store, path)}
			drafts[i] = draft
		}()
	}
	wg.Wait()
	close(errs)
	var failures []error
	for err := range errs {
		if err != nil {
			failures = append(failures, err)
		}
	}
	successful := make([]ai.PlanningDraft, 0, len(drafts))
	for _, draft := range drafts {
		if strings.TrimSpace(draft.Agent) != "" {
			successful = append(successful, draft)
		}
	}
	if len(successful) == 0 {
		if len(failures) > 0 {
			return nil, results, fmt.Errorf("all planning agents failed; first error: %w", failures[0])
		}
		return nil, results, errors.New("all planning agents failed")
	}
	return successful, results, nil
}

func validatePlanningDraft(agentName string, draft ai.PlanningDraft) error {
	if strings.TrimSpace(draft.Agent) == "" {
		return errors.New("agent is required")
	}
	if strings.TrimSpace(draft.Summary) == "" {
		return errors.New("summary is required")
	}
	if strings.TrimSpace(draft.SpecDraft) == "" && strings.TrimSpace(draft.SpecMarkdown) == "" {
		return errors.New("spec draft is required")
	}
	if strings.TrimSpace(draft.TaskDraft) == "" && strings.TrimSpace(draft.TaskMarkdown) == "" {
		return errors.New("task draft is required")
	}
	if len(nonEmptyPlanningItems(draft.AcceptanceCriteria)) == 0 {
		return errors.New("acceptance criteria are required")
	}
	if len(nonEmptyPlanningItems(draft.TestingGuidance)) == 0 && len(nonEmptyPlanningItems(draft.TestPlan)) == 0 {
		return errors.New("test guidance is required")
	}
	if strings.TrimSpace(agentName) != "" && strings.TrimSpace(draft.Agent) != agentName {
		return fmt.Errorf("agent mismatch: got %q, expected %q", draft.Agent, agentName)
	}
	return nil
}

func validateMergeResult(merged ai.MergeResult) error {
	if strings.TrimSpace(merged.Spec) == "" {
		return errors.New("merged spec is required")
	}
	if strings.TrimSpace(merged.Task) == "" {
		return errors.New("merged task is required")
	}
	return nil
}

func nonEmptyPlanningItems(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	return out
}

func planningAgents(count int) []ai.Agent {
	base := []ai.Agent{
		{Name: "product_spec", Focus: "turn the request into product behavior, user experience, CLI behavior, artifacts, and acceptance criteria"},
		{Name: "implementation_task", Focus: "turn the request into Go implementation steps, package structure, interfaces, and test strategy"},
		{Name: "qa_eval", Focus: "identify risks, edge cases, failure scenarios, test plans, and evaluation criteria"},
	}
	if count == 1 {
		return []ai.Agent{{Name: "product_spec", Focus: "create one comprehensive product, implementation, and QA planning draft"}}
	}
	if count <= len(base) {
		return base[:count]
	}
	out := append([]ai.Agent{}, base...)
	for i := len(base) + 1; i <= count; i++ {
		out = append(out, ai.Agent{
			Name:  fmt.Sprintf("reviewer_%d", i),
			Focus: "review the plan from an additional implementation and quality perspective",
		})
	}
	return out
}

func relArtifactPath(store artifact.Store, path string) string {
	if rel, err := filepath.Rel(store.RunDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]..."
}

func redactSecrets(s string) string {
	return secrets.Redact(s)
}
