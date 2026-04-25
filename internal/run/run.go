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
	SchemaVersion   string             `json:"schema_version"`
	RunID           string             `json:"run_id"`
	StartedAt       string             `json:"started_at"`
	FinishedAt      string             `json:"finished_at,omitempty"`
	Status          string             `json:"status"`
	DryRun          bool               `json:"dry_run"`
	NoGitMode       bool               `json:"no_git_mode"`
	CWD             string             `json:"cwd"`
	PlanPath        string             `json:"plan_path"`
	PlannerProvider string             `json:"planner_provider"`
	Git             ManifestGit        `json:"git"`
	Config          ManifestConfig     `json:"config"`
	Artifacts       map[string]string  `json:"artifacts"`
	Planning        ManifestPlanning   `json:"planning"`
	Codex           ManifestCodex      `json:"codex"`
	Evaluation      ManifestEvaluation `json:"evaluation"`
	Errors          []string           `json:"errors"`
}

type ManifestGit struct {
	IsRepo          bool   `json:"is_repo"`
	Root            string `json:"root,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Head            string `json:"head,omitempty"`
	InitialStatus   string `json:"initial_status,omitempty"`
	FinalStatus     string `json:"final_status,omitempty"`
	BaselinePath    string `json:"baseline_path,omitempty"`
	StatusPath      string `json:"status_path,omitempty"`
	DiffPath        string `json:"diff_path,omitempty"`
	DiffSummaryPath string `json:"diff_summary_path,omitempty"`
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
	Ran        bool   `json:"ran"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type ManifestEvaluation struct {
	Ran    bool   `json:"ran"`
	Result string `json:"result,omitempty"`
	Score  int    `json:"score"`
	Error  string `json:"error,omitempty"`
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
		return nil, err
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
		return nil, err
	}
	cfg.CWD = absCWD
	if err := validateCWD(cfg.CWD); err != nil {
		return nil, err
	}
	if err := validateResolvedConfig(cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.RunID) == "" {
		cfg.RunID = artifact.NewRunID(time.Now())
	}

	fmt.Fprintf(cfg.Stdout, "jj: reading %s\n", cfg.PlanPath)
	plan, planPath, err := LoadPlan(cfg.PlanPath, cfg.CWD)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(cfg.Stdout, "jj: checking git workspace")
	gitState, err := InspectGit(ctx, cfg.CWD, cfg.GitRunner)
	if err != nil {
		return nil, fmt.Errorf("inspect git state: %w", err)
	}
	if !gitState.Available && !cfg.AllowNoGit {
		return nil, errors.New("target directory is not a git repository; use --allow-no-git to override")
	}

	store, err := artifact.NewStore(cfg.CWD, cfg.RunID)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(cfg.Stdout, "jj: creating run directory %s\n", store.RunDir)
	if err := store.Init(); err != nil {
		return nil, err
	}

	manifest := Manifest{
		SchemaVersion: manifestSchemaVersion,
		RunID:         cfg.RunID,
		StartedAt:     started.Format(time.RFC3339),
		Status:        "running",
		DryRun:        cfg.DryRun,
		NoGitMode:     !gitState.Available && cfg.AllowNoGit,
		CWD:           cfg.CWD,
		PlanPath:      planPath,
		Git: ManifestGit{
			IsRepo:        gitState.Available,
			Root:          gitState.Root,
			Branch:        gitState.Branch,
			Head:          gitState.Head,
			InitialStatus: gitState.InitialStatus,
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
		},
		Artifacts: map[string]string{},
	}
	var manifestMu sync.Mutex
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
	writeManifest := func(status string) {
		manifestMu.Lock()
		defer manifestMu.Unlock()
		manifest.Status = status
		manifest.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		manifest.Artifacts["manifest"] = "manifest.json"
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return
		}
		data = append([]byte(redactSecrets(string(data))), '\n')
		_, _ = store.WriteFile("manifest.json", data)
	}
	fail := func(err error) (*Result, error) {
		addError(err)
		status := "failed"
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
		}
		writeManifest(status)
		safeErr := redactSecrets(err.Error())
		fmt.Fprintf(cfg.Stderr, "jj: failed: %s\n", safeErr)
		return nil, errors.New(safeErr)
	}

	if p, err := store.WriteString("input.md", plan); err != nil {
		return fail(err)
	} else {
		record("input", p)
	}
	if p, err := store.WriteJSON("git-baseline.json", gitState); err != nil {
		return fail(err)
	} else {
		record("git_baseline", p)
		manifest.Git.BaselinePath = "git-baseline.json"
	}

	plannerSelection, err := selectPlanner(cfg, store, record)
	if err != nil {
		return fail(err)
	}
	planner := plannerSelection.Client
	manifest.PlannerProvider = plannerSelection.Provider

	fmt.Fprintf(cfg.Stdout, "jj: running %d planning agents\n", cfg.PlanningAgents)
	drafts, agentResults, err := runPlanningAgents(ctx, planner, store, cfg.OpenAIModel, plan, cfg.PlanningAgents, record)
	manifest.Planning.Agents = agentResults
	if err != nil {
		return fail(err)
	}

	fmt.Fprintln(cfg.Stdout, "jj: merging planning outputs")
	merged, raw, err := planner.Merge(ctx, ai.MergeRequest{
		Model:  cfg.OpenAIModel,
		Plan:   plan,
		Drafts: drafts,
	})
	if err != nil {
		return fail(fmt.Errorf("merge planning outputs: %w", err))
	}
	merged.Spec = ensureSpecSections(merged.Spec, plan)
	merged.Task = ensureTaskSections(merged.Task, plan)
	specRel := docRelPath(cfg.SpecDoc)
	taskRel := docRelPath(cfg.TaskDoc)
	evalRel := docRelPath(cfg.EvalDoc)
	if p, err := store.WriteFile("planning/merge.json", redactBytes(raw)); err != nil {
		return fail(err)
	} else {
		record("planning_merge", p)
	}
	specArtifact, err := store.WriteString(specRel, merged.Spec)
	if err != nil {
		return fail(err)
	}
	taskArtifact, err := store.WriteString(taskRel, merged.Task)
	if err != nil {
		return fail(err)
	}
	record("spec", specArtifact)
	record("task", taskArtifact)

	if cfg.DryRun {
		fmt.Fprintf(cfg.Stdout, "jj: dry run complete\n")
		fmt.Fprintf(cfg.Stdout, "run_id=%s\nrun_dir=%s\nspec=%s\ntask=%s\n", cfg.RunID, store.RunDir, filepath.ToSlash(filepath.Join(store.RunDir, filepath.FromSlash(specRel))), filepath.ToSlash(filepath.Join(store.RunDir, filepath.FromSlash(taskRel))))
		writeManifest("success")
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, nil
	}

	specPath := filepath.Join(cfg.CWD, filepath.FromSlash(specRel))
	taskPath := filepath.Join(cfg.CWD, filepath.FromSlash(taskRel))
	if err := writeWorktreeFile(specPath, []byte(merged.Spec)); err != nil {
		return fail(fmt.Errorf("write %s: %w", specRel, err))
	}
	if err := writeWorktreeFile(taskPath, []byte(merged.Task)); err != nil {
		return fail(fmt.Errorf("write %s: %w", taskRel, err))
	}
	fmt.Fprintf(cfg.Stdout, "jj: wrote %s and %s\n", specRel, taskRel)
	recordRel("spec_worktree", specRel)
	recordRel("task_worktree", taskRel)

	runner := cfg.CodexRunner
	if runner == nil {
		runner = codex.Runner{}
	}
	eventsPath, _ := store.Path("codex-events.jsonl")
	summaryPath, _ := store.Path("codex-summary.md")
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
	if err := redactFile(eventsPath); err != nil {
		return fail(err)
	}
	if err := redactFile(summaryPath); err != nil {
		return fail(err)
	}
	manifest.Codex = ManifestCodex{
		Ran:        true,
		ExitCode:   codexResult.ExitCode,
		DurationMS: codexResult.DurationMS,
	}
	record("codex_events", eventsPath)
	record("codex_summary", summaryPath)
	if strings.TrimSpace(codexResult.Summary) == "" {
		if data, readErr := os.ReadFile(summaryPath); readErr == nil {
			codexResult.Summary = string(data)
		}
	}
	codexResult.Summary = redactSecrets(codexResult.Summary)
	if codexErr != nil {
		safeCodexErr := redactSecrets(codexErr.Error())
		manifest.Codex.Error = safeCodexErr
		addError(codexErr)
		codexResult.Summary = strings.TrimSpace(codexResult.Summary + "\n\nCodex error: " + safeCodexErr)
		fmt.Fprintf(cfg.Stderr, "jj: codex failed, continuing to evaluation: %s\n", safeCodexErr)
	}

	fmt.Fprintln(cfg.Stdout, "jj: capturing git diff")
	diff, err := CaptureGitDiff(ctx, cfg.CWD, gitState.Available, cfg.GitRunner)
	if err != nil {
		return fail(fmt.Errorf("capture git diff: %w", err))
	}
	manifest.Git.FinalStatus = diff.Status
	if p, err := store.WriteString("git-diff.patch", diff.Full+"\n"); err != nil {
		return fail(err)
	} else {
		record("git_diff", p)
		manifest.Git.DiffPath = "git-diff.patch"
	}
	if p, err := store.WriteString("git-status.txt", diff.Status+"\n"); err != nil {
		return fail(err)
	} else {
		record("git_status", p)
		manifest.Git.StatusPath = "git-status.txt"
	}
	if p, err := store.WriteString("git-diff-summary.txt", diff.Markdown()); err != nil {
		return fail(err)
	} else {
		record("git_diff_summary", p)
		manifest.Git.DiffSummaryPath = "git-diff-summary.txt"
	}
	codexEvents := ""
	if data, err := os.ReadFile(eventsPath); err == nil {
		codexEvents = string(data)
	}

	fmt.Fprintln(cfg.Stdout, "jj: evaluating result")
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
		manifest.Evaluation = ManifestEvaluation{Ran: true, Error: redactSecrets(evalErr.Error())}
		return fail(evalErr)
	}
	ai.NormalizeEvaluation(&eval)
	manifest.Evaluation = ManifestEvaluation{Ran: true, Result: eval.Result, Score: eval.Score}
	if p, err := store.WriteFile("planning/eval.json", redactBytes(rawEval)); err != nil {
		return fail(err)
	} else {
		record("evaluation_json", p)
	}
	evalMarkdown := renderEvaluation(eval)
	if p, err := store.WriteString(evalRel, evalMarkdown); err != nil {
		return fail(err)
	} else {
		record("eval", p)
	}
	evalPath := filepath.Join(cfg.CWD, filepath.FromSlash(evalRel))
	if err := writeWorktreeFile(evalPath, []byte(evalMarkdown)); err != nil {
		return fail(fmt.Errorf("write %s: %w", evalRel, err))
	}
	recordRel("eval_worktree", evalRel)
	if finalDiff, err := CaptureGitDiff(ctx, cfg.CWD, gitState.Available, cfg.GitRunner); err != nil {
		return fail(fmt.Errorf("capture final git diff: %w", err))
	} else {
		manifest.Git.FinalStatus = finalDiff.Status
		if p, err := store.WriteString("git-diff.patch", finalDiff.Full+"\n"); err != nil {
			return fail(err)
		} else {
			record("git_diff", p)
			manifest.Git.DiffPath = "git-diff.patch"
		}
		if p, err := store.WriteString("git-status.txt", finalDiff.Status+"\n"); err != nil {
			return fail(err)
		} else {
			record("git_status", p)
			manifest.Git.StatusPath = "git-status.txt"
		}
		if p, err := store.WriteString("git-diff-summary.txt", finalDiff.Markdown()); err != nil {
			return fail(err)
		} else {
			record("git_diff_summary", p)
			manifest.Git.DiffSummaryPath = "git-diff-summary.txt"
		}
	}
	status := "success"
	if codexErr != nil || eval.Result != "PASS" {
		status = "partial"
	}
	writeManifest(status)
	fmt.Fprintf(cfg.Stdout, "jj: done\nrun_id=%s\nrun_dir=%s\nspec=%s\ntask=%s\neval=%s\ncodex_exit_code=%d\nreview=jj serve --cwd %s\n", cfg.RunID, store.RunDir, specRel, taskRel, evalRel, manifest.Codex.ExitCode, cfg.CWD)
	return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, nil
}

func writeWorktreeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return artifact.AtomicWriteFile(path, data, 0o644)
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

func redactBytes(data []byte) []byte {
	return []byte(redactSecrets(string(data)))
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

func planningAgents(count int) []ai.Agent {
	base := []ai.Agent{
		{Name: "product_spec", Focus: "turn the request into product behavior, user experience, CLI behavior, artifacts, and acceptance criteria"},
		{Name: "implementation_tasking", Focus: "turn the request into Go implementation steps, package structure, interfaces, and test strategy"},
		{Name: "qa_evaluation", Focus: "identify risks, edge cases, failure scenarios, test plans, and evaluation criteria"},
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
