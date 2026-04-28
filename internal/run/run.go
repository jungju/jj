package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/codex"
	ai "github.com/jungju/jj/internal/openai"
	"github.com/jungju/jj/internal/secrets"
	"github.com/jungju/jj/internal/security"
)

const manifestSchemaVersion = "1"

const (
	StatusPlanning       = "planning"
	StatusDryRunComplete = "dry_run_complete"
	StatusImplementing   = "implementing"
	StatusValidating     = "validating"
	StatusComplete       = "complete"
	StatusWarnings       = "completed_with_warnings"
	StatusPartialFailed  = "partial_failed"
	StatusFailed         = "failed"

	StatusPlanned              = StatusDryRunComplete
	StatusSuccess              = StatusComplete
	StatusPartial              = StatusPartialFailed
	StatusCompleted            = StatusComplete
	StatusPlanningFailed       = StatusFailed
	StatusImplementationFailed = StatusPartialFailed
	statusRunning              = StatusPlanning
)

type PlanningClient interface {
	Draft(context.Context, ai.DraftRequest) (ai.PlanningDraft, []byte, error)
	Merge(context.Context, ai.MergeRequest) (ai.MergeResult, []byte, error)
	ReconcileSpec(context.Context, ai.ReconcileSpecRequest) (ai.ReconcileSpecResult, []byte, error)
}

type CodexRunner interface {
	Run(context.Context, codex.Request) (codex.Result, error)
}

type Result struct {
	RunID  string
	RunDir string
}

type ManifestLoop struct {
	Enabled       bool   `json:"enabled"`
	BaseRunID     string `json:"base_run_id,omitempty"`
	Turn          int    `json:"turn,omitempty"`
	MaxTurns      int    `json:"max_turns,omitempty"`
	PreviousRunID string `json:"previous_run_id,omitempty"`
}

type Manifest struct {
	SchemaVersion              string             `json:"schema_version"`
	RunID                      string             `json:"run_id"`
	StartedAt                  string             `json:"started_at"`
	FinishedAt                 string             `json:"finished_at,omitempty"`
	EndedAt                    string             `json:"ended_at,omitempty"`
	DurationMS                 int64              `json:"duration_ms,omitempty"`
	Status                     string             `json:"status"`
	DryRun                     bool               `json:"dry_run"`
	AllowNoGit                 bool               `json:"allow_no_git"`
	NoGitMode                  bool               `json:"no_git_mode"`
	CWD                        string             `json:"cwd"`
	PlanPath                   string             `json:"plan_path"`
	InputPath                  string             `json:"input_path"`
	InputSource                string             `json:"input_source,omitempty"`
	TaskProposalMode           TaskProposalMode   `json:"task_proposal_mode"`
	ResolvedTaskProposalMode   TaskProposalMode   `json:"resolved_task_proposal_mode,omitempty"`
	TaskProposalReason         string             `json:"task_proposal_reason,omitempty"`
	SelectedTaskID             string             `json:"selected_task_id,omitempty"`
	Repository                 ManifestRepository `json:"repository,omitempty"`
	Loop                       *ManifestLoop      `json:"loop,omitempty"`
	PlannerProvider            string             `json:"planner_provider"`
	Planner                    ManifestPlanner    `json:"planner"`
	Git                        ManifestGit        `json:"git"`
	Config                     ManifestConfig     `json:"config"`
	Workspace                  ManifestWorkspace  `json:"workspace"`
	Artifacts                  map[string]string  `json:"artifacts"`
	Risks                      []string           `json:"risks,omitempty"`
	Planning                   ManifestPlanning   `json:"planning"`
	Codex                      ManifestCodex      `json:"codex"`
	Validation                 ManifestValidation `json:"validation"`
	Commit                     ManifestCommit     `json:"commit,omitempty"`
	Security                   ManifestSecurity   `json:"security"`
	RiskCount                  int                `json:"risk_count"`
	Errors                     []string           `json:"errors"`
	FailurePhase               string             `json:"failure_phase,omitempty"`
	FailureMessage             string             `json:"failure_message,omitempty"`
	FailedStage                string             `json:"failed_stage,omitempty"`
	ErrorSummary               string             `json:"error_summary,omitempty"`
	RedactionApplied           bool               `json:"redaction_applied"`
	WorkspaceGuardrailsApplied bool               `json:"workspace_guardrails_applied"`
	RedactionCount             int64              `json:"redaction_count,omitempty"`
	RedactionKinds             []string           `json:"redaction_kinds,omitempty"`
	RedactionKindCounts        map[string]int64   `json:"redaction_kind_counts,omitempty"`
}

type ManifestSecurity struct {
	RedactionApplied           bool                        `json:"redaction_applied"`
	WorkspaceGuardrailsApplied bool                        `json:"workspace_guardrails_applied"`
	RedactionCount             int64                       `json:"redaction_count,omitempty"`
	RedactionKinds             []string                    `json:"redaction_kinds,omitempty"`
	RedactionKindCounts        map[string]int64            `json:"redaction_kind_counts,omitempty"`
	RedactionPolicy            string                      `json:"redaction_policy"`
	PathPolicy                 string                      `json:"path_policy"`
	ServePolicy                string                      `json:"serve_policy"`
	CommandPolicy              string                      `json:"command_policy"`
	EnvironmentPolicy          string                      `json:"environment_policy"`
	Diagnostics                ManifestSecurityDiagnostics `json:"diagnostics"`
}

type ManifestSecurityDiagnostics struct {
	Version                        string                 `json:"version"`
	Redacted                       bool                   `json:"redacted"`
	SecretMaterialPresent          bool                   `json:"secret_material_present"`
	GuardedRoots                   []ManifestSecurityRoot `json:"guarded_roots"`
	RootLabels                     []string               `json:"root_labels"`
	DeniedPathCount                int                    `json:"denied_path_count"`
	DeniedPathCategories           []string               `json:"denied_path_categories"`
	DeniedPathCategoryCounts       map[string]int         `json:"denied_path_category_counts"`
	FailureCategories              []string               `json:"failure_categories"`
	FailureCategoryCounts          map[string]int         `json:"failure_category_counts"`
	CommandRecordCount             int                    `json:"command_record_count"`
	CommandMetadataSanitized       bool                   `json:"command_metadata_sanitized"`
	CommandArgvSanitized           bool                   `json:"command_argv_sanitized"`
	CommandCWDLabel                string                 `json:"command_cwd_label"`
	CommandSanitizationStatus      string                 `json:"command_sanitization_status"`
	RawCommandTextPersisted        bool                   `json:"raw_command_text_persisted"`
	RawEnvironmentPersisted        bool                   `json:"raw_environment_persisted"`
	DryRunParityApplied            bool                   `json:"dry_run_parity_applied"`
	DryRunParityStatus             string                 `json:"dry_run_parity_status"`
	GitDiffArtifactsAvailable      bool                   `json:"git_diff_artifacts_available"`
	GitDiffRedactionApplied        bool                   `json:"git_diff_redaction_applied"`
	GitDiffRedactionCount          int                    `json:"git_diff_redaction_count,omitempty"`
	GitDiffRedactionCategories     []string               `json:"git_diff_redaction_categories,omitempty"`
	GitDiffRedactionCategoryCounts map[string]int         `json:"git_diff_redaction_category_counts,omitempty"`
	GitDiffArtifactLabels          []string               `json:"git_diff_artifact_labels,omitempty"`
}

type ManifestSecurityRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type ManifestGit struct {
	Available                   bool           `json:"available"`
	IsRepo                      bool           `json:"is_repo"`
	Root                        string         `json:"root,omitempty"`
	Branch                      string         `json:"branch,omitempty"`
	Head                        string         `json:"head,omitempty"`
	InitialStatus               string         `json:"initial_status,omitempty"`
	FinalStatus                 string         `json:"final_status,omitempty"`
	DirtyBefore                 bool           `json:"dirty_before"`
	DirtyAfter                  bool           `json:"dirty_after"`
	Dirty                       bool           `json:"dirty"`
	NoGit                       bool           `json:"no_git"`
	BaselinePath                string         `json:"baseline_path,omitempty"`
	BaselineTextPath            string         `json:"baseline_text_path,omitempty"`
	StatusBeforePath            string         `json:"status_before_path,omitempty"`
	StatusAfterPath             string         `json:"status_after_path,omitempty"`
	StatusPath                  string         `json:"status_path,omitempty"`
	DiffPath                    string         `json:"diff_path,omitempty"`
	DiffStatPath                string         `json:"diff_stat_path,omitempty"`
	DiffSummaryPath             string         `json:"diff_summary_path,omitempty"`
	DiffRedactionApplied        bool           `json:"diff_redaction_applied"`
	DiffRedactionCount          int            `json:"diff_redaction_count,omitempty"`
	DiffRedactionCategories     []string       `json:"diff_redaction_categories,omitempty"`
	DiffRedactionCategoryCounts map[string]int `json:"diff_redaction_category_counts,omitempty"`
	DiffArtifactLabels          []string       `json:"diff_artifact_labels,omitempty"`
	UntrackedAvailable          bool           `json:"untracked_available"`
	UntrackedCount              int            `json:"untracked_count,omitempty"`
	UntrackedCapturedCount      int            `json:"untracked_captured_count,omitempty"`
	UntrackedSkippedCount       int            `json:"untracked_skipped_count,omitempty"`
	UntrackedFilesPath          string         `json:"untracked_files_path,omitempty"`
	UntrackedPatchPath          string         `json:"untracked_patch_path,omitempty"`
	UntrackedSummaryPath        string         `json:"untracked_summary_path,omitempty"`
	Warnings                    []string       `json:"warnings,omitempty"`
}

type ManifestPlanner struct {
	Provider  string            `json:"provider"`
	Model     string            `json:"model,omitempty"`
	Artifacts map[string]string `json:"artifacts,omitempty"`
}

type ManifestConfig = security.SafeConfig

type ManifestWorkspace struct {
	SpecPath    string `json:"spec_path"`
	TaskPath    string `json:"task_path"`
	SpecWritten bool   `json:"spec_written"`
	TaskWritten bool   `json:"task_written"`
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
	if strings.TrimSpace(cfg.RunID) == "" {
		cfg.RunID = artifact.NewRunID(time.Now())
	}
	var preloadedPlanInput *PlanInput
	if strings.TrimSpace(cfg.RepoURL) != "" && strings.TrimSpace(cfg.PlanText) == "" {
		planInput, err := LoadPlanInput(cfg.PlanPath, "", cfg.PlanInputName, cfg.CWD)
		if err != nil {
			return nil, validationError(err)
		}
		preloadedPlanInput = &planInput
	}
	var repoRuntime *repositoryRuntime
	cfg, repoRuntime, err = prepareRepositoryWorkspace(ctx, cfg)
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
	if resolved, err := filepath.EvalSymlinks(cfg.CWD); err == nil {
		cfg.CWD = resolved
	}
	if err := validateResolvedConfig(cfg); err != nil {
		return nil, validationError(err)
	}
	specRel := DefaultSpecStatePath
	taskRel := DefaultTasksStatePath

	var planInput PlanInput
	if preloadedPlanInput != nil {
		planInput = *preloadedPlanInput
	} else {
		planInput, err = LoadPlanInput(cfg.PlanPath, cfg.PlanText, cfg.PlanInputName, cfg.CWD)
		if err != nil {
			return nil, validationError(err)
		}
	}
	fmt.Fprintf(cfg.Stdout, "jj: reading %s\n", planInput.Path)
	originalPlan := planInput.Content
	continuationContext := strings.TrimSpace(redactSecrets(cfg.AdditionalPlanContext))
	stateBefore := loadStateSnapshot(cfg.CWD)
	existingSelectedTask, useExistingTask := selectExistingRunnableTask(stateBefore.TasksBefore)
	nextIntent := NextIntentInput{}
	intentContent := ""
	if !useExistingTask {
		nextIntent, err = LoadNextIntent(cfg.CWD)
		if err != nil {
			return nil, validationError(err)
		}
		intentContent = nextIntent.Content
	}
	planningContext := buildPlanningContext(originalPlan, stateBefore.SpecBefore, stateBefore.TasksBefore, continuationContext, intentContent)
	providerPlan := sanitizeHandoffText(planningContext)
	proposalEvidence := buildTaskProposalEvidence(stateBefore.SpecBefore, stateBefore.TasksBefore, continuationContext, intentContent)
	proposal := ResolveTaskProposalMode(cfg.TaskProposalMode, proposalEvidence)
	if useExistingTask {
		proposal.Resolved = taskMode(existingSelectedTask, proposal)
		proposal.SelectedTaskID = existingSelectedTask.ID
		proposal.Reason = firstNonEmptyString(proposal.Reason, "selected existing runnable task before proposing new work")
	}
	proposalPrompt := TaskProposalPromptContext(proposal, intentContent)
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
	manifestPlanPath := planInput.Path
	if planInput.Source == PlanInputSourceWebPrompt {
		manifestPlanPath = ""
	}

	var manifestLoop *ManifestLoop
	if cfg.LoopEnabled {
		baseRunID := strings.TrimSpace(cfg.LoopBaseRunID)
		if baseRunID == "" {
			baseRunID = cfg.RunID
		}
		manifestLoop = &ManifestLoop{
			Enabled:       true,
			BaseRunID:     baseRunID,
			Turn:          cfg.LoopTurn,
			MaxTurns:      cfg.LoopMaxTurns,
			PreviousRunID: strings.TrimSpace(cfg.LoopPreviousRunID),
		}
	}

	manifest := Manifest{
		SchemaVersion:            manifestSchemaVersion,
		RunID:                    cfg.RunID,
		StartedAt:                started.Format(time.RFC3339),
		Status:                   StatusPlanning,
		DryRun:                   cfg.DryRun,
		AllowNoGit:               cfg.AllowNoGit,
		NoGitMode:                !gitState.Available && cfg.AllowNoGit,
		CWD:                      cfg.CWD,
		PlanPath:                 manifestPlanPath,
		InputPath:                planInput.Path,
		InputSource:              planInput.Source,
		TaskProposalMode:         proposal.Selected,
		ResolvedTaskProposalMode: proposal.Resolved,
		TaskProposalReason:       sanitizeHandoffText(proposal.Reason),
		SelectedTaskID:           proposal.SelectedTaskID,
		Repository: func() ManifestRepository {
			if repoRuntime == nil {
				return ManifestRepository{}
			}
			return repoRuntime.Manifest
		}(),
		Loop: manifestLoop,
		Git: ManifestGit{
			Available:          gitState.Available,
			IsRepo:             gitState.Available,
			Root:               gitState.Root,
			Branch:             gitState.Branch,
			Head:               gitState.Head,
			InitialStatus:      gitState.InitialStatus,
			DirtyBefore:        gitState.Dirty,
			DirtyAfter:         gitState.Dirty,
			Dirty:              gitState.Dirty,
			NoGit:              !gitState.Available && cfg.AllowNoGit,
			UntrackedAvailable: gitState.Available,
			Warnings:           gitWarnings,
		},
		Config: security.NewSafeConfig(ManifestConfig{
			PlanningAgents:   cfg.PlanningAgents,
			OpenAIModel:      cfg.OpenAIModel,
			CodexModel:       cfg.CodexModel,
			CodexBin:         cfg.CodexBin,
			TaskProposalMode: string(cfg.TaskProposalMode),
			ConfigFile:       cfg.ConfigFile,
			OpenAIKeyEnv:     cfg.OpenAIAPIKeyEnv,
			OpenAIKeySet:     strings.TrimSpace(cfg.OpenAIAPIKey) != "",
			AllowNoGit:       cfg.AllowNoGit,
			SpecPath:         specRel,
			TaskPath:         taskRel,
		}),
		Workspace: ManifestWorkspace{
			SpecPath: specRel,
			TaskPath: taskRel,
		},
		Commit:                     ManifestCommit{Ran: false, Status: "skipped"},
		Artifacts:                  map[string]string{},
		RedactionApplied:           true,
		WorkspaceGuardrailsApplied: true,
		Security:                   newManifestSecurityDiagnosticsEnvelope(),
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
		manifest.Errors = append(manifest.Errors, sanitizeHandoffText(err.Error()))
	}
	addRisks := func(items ...string) {
		manifestMu.Lock()
		defer manifestMu.Unlock()
		for _, item := range items {
			item = strings.TrimSpace(sanitizeHandoffText(item))
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
	recordSecurityFailure := func(err error) {
		if err == nil {
			return
		}
		category := securityFailureDiagnosticCategory(err)
		if category == "" {
			return
		}
		manifestMu.Lock()
		defer manifestMu.Unlock()
		recordDeniedPathDiagnostic(&manifest.Security.Diagnostics, category)
		recordSecurityFailureDiagnostic(&manifest.Security.Diagnostics, category)
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
			if err := artifact.ValidateArtifactName(name); err != nil {
				delete(manifest.Artifacts, name)
				delete(manifest.Planner.Artifacts, name)
				continue
			}
			if strings.HasPrefix(name, "planning") {
				manifest.Planner.Artifacts[name] = path
			}
		}
		_, manifestReport := security.RedactJSONValueWithReport(manifest)
		redactionKindCounts := store.RedactionKindCounts()
		for kind, count := range manifestReport.Kinds {
			if count > 0 {
				redactionKindCounts[kind] += int64(count)
			}
		}
		manifest.RedactionCount = store.RedactionCount() + int64(manifestReport.Count)
		manifest.Security.RedactionCount = manifest.RedactionCount
		manifest.RedactionKindCounts = redactionKindCounts
		manifest.RedactionKinds = sortedRedactionKinds(redactionKindCounts)
		manifest.Security.RedactionKindCounts = redactionKindCounts
		manifest.Security.RedactionKinds = manifest.RedactionKinds
		refreshManifestSecurityDiagnostics(&manifest, manifest.RedactionCount)
		redactedManifest, redactionReport := security.RedactJSONValueWithReport(manifest)
		store.RecordRedactionReport(redactionReport)
		data, err := json.MarshalIndent(redactedManifest, "", "  ")
		if err != nil {
			return
		}
		data = append([]byte(redactSecrets(string(data))), '\n')
		_, _ = store.WriteFile("manifest.json", data)
	}
	workspaceModified := false
	fail := func(status string, err error) (*Result, error) {
		recordSecurityFailure(err)
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
		if !manifest.Validation.Ran && !manifest.Validation.Skipped && manifest.Validation.Status == "" {
			manifest.Validation.Skipped = true
			manifest.Validation.Status = validationStatusSkipped
			manifest.Validation.EvidenceStatus = validationEvidenceSkipped
			manifest.Validation.Reason = "skipped because " + currentStage + " did not complete"
			manifest.Validation.Summary = "Validation did not run because " + currentStage + " did not complete."
		}
		if manifest.FailedStage == "" {
			manifest.FailedStage = currentStage
		}
		if manifest.FailurePhase == "" {
			manifest.FailurePhase = currentStage
		}
		if manifest.ErrorSummary == "" && err != nil {
			manifest.ErrorSummary = sanitizeHandoffText(err.Error())
		}
		if manifest.FailureMessage == "" && err != nil {
			manifest.FailureMessage = sanitizeHandoffText(err.Error())
		}
		manifestMu.Unlock()
		if !cfg.DryRun {
			manifestMu.Lock()
			_, hasSpecAfter := manifest.Artifacts["snapshot_spec_after"]
			manifestMu.Unlock()
			if !hasSpecAfter {
				if p, writeErr := writeSnapshotJSON(store, "snapshots/spec.after.json", stateBefore.SpecBefore); writeErr == nil {
					record("snapshot_spec_after", p)
					record("spec_state", filepath.Join(store.RunDir, "snapshots", "spec.after.json"))
				}
			}
		}
		writeManifest(status, true)
		safeErr := sanitizeHandoffText(err.Error())
		fmt.Fprintf(cfg.Stderr, "jj: failed: %s\nstatus=%s\nworkspace_modified=%t\npartial_artifacts=%s\n", safeErr, status, workspaceModified, store.RunDir)
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, errors.New(safeErr)
	}
	runEvents := []map[string]string{}
	writeRunEvent := func(eventType string, fields map[string]string) error {
		event := map[string]string{
			"type": eventType,
			"time": time.Now().UTC().Format(time.RFC3339),
		}
		for key, value := range fields {
			value = strings.TrimSpace(sanitizeHandoffText(value))
			if value != "" {
				event[key] = value
			}
		}
		runEvents = append(runEvents, event)
		var b strings.Builder
		for _, item := range runEvents {
			data, err := json.Marshal(item)
			if err != nil {
				return err
			}
			b.Write(data)
			b.WriteByte('\n')
		}
		p, err := store.WriteString("events.jsonl", b.String())
		if err != nil {
			return err
		}
		record("events", p)
		return nil
	}
	if repoRuntime != nil {
		for _, event := range repoRuntime.Events {
			if err := writeRunEvent(event.Type, event.Fields); err != nil {
				return fail(StatusPartial, err)
			}
		}
	}
	if err := writeRunEvent("task_proposal_mode.selected", map[string]string{
		"mode": string(proposal.Selected),
	}); err != nil {
		return fail(StatusPartial, err)
	}
	if err := writeRunEvent("task_proposal_mode.resolved", map[string]string{
		"selected_mode": string(proposal.Selected),
		"resolved_mode": string(proposal.Resolved),
		"reason":        proposal.Reason,
	}); err != nil {
		return fail(StatusPartial, err)
	}
	if p, err := writeSnapshotJSON(store, "snapshots/spec.before.json", stateBefore.SpecBefore); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("snapshot_spec_before", p)
	}
	if p, err := writeSnapshotJSON(store, "snapshots/tasks.before.json", stateBefore.TasksBefore); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("snapshot_tasks_before", p)
	}

	if p, err := store.WriteString("input-original.md", sanitizeHandoffText(originalPlan)); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("input_original", p)
	}
	if p, err := store.WriteString("input-context.md", sanitizeHandoffText(continuationContext)+"\n"); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("input_context", p)
	}
	if p, err := store.WriteString("input.md", sanitizeHandoffText(planningContext)); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("input", p)
	}
	if p, err := store.WriteString("input/plan.md", sanitizeHandoffText(originalPlan)); err != nil {
		return fail(StatusPartial, err)
	} else {
		record("input_plan", p)
	}
	if nextIntent.Active() {
		if p, err := store.WriteString("input/next-intent.md", sanitizeHandoffText(nextIntent.Content)); err != nil {
			return fail(StatusPartial, err)
		} else {
			record("input_next_intent", p)
		}
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

	recordPlanningOutput := func(planningArtifact normalizedPlanningResult) error {
		if p, err := writeHandoffJSON(store, "planning.json", planningArtifact); err != nil {
			return err
		} else {
			record("planning", p)
		}
		if p, err := writeHandoffJSON(store, "planning/planning.json", planningArtifact); err != nil {
			return err
		} else {
			record("planning_normalized", p)
		}
		return nil
	}

	stateNow := time.Now().UTC()
	var plannedSpecState SpecState
	var tasksState TaskState
	var selectedTask TaskRecord
	if useExistingTask {
		var ok bool
		plannedSpecState = stateBefore.SpecBefore
		tasksState, selectedTask, ok = buildExistingRunnableTaskState(stateBefore.TasksBefore, proposal, !cfg.DryRun, stateNow)
		if !ok {
			return fail(StatusPlanningFailed, errors.New("selected existing runnable task disappeared before execution"))
		}
		manifest.SelectedTaskID = selectedTask.ID
		if err := writeRunEvent("task.selected", map[string]string{
			"task_id": selectedTask.ID,
			"mode":    selectedTask.Mode,
			"source":  "existing",
		}); err != nil {
			return fail(StatusPlanningFailed, err)
		}
		if err := recordPlanningOutput(normalizedExistingTaskPlanning(selectedTask, plannedSpecState, tasksState, proposal)); err != nil {
			return fail(StatusPlanningFailed, err)
		}
	} else {
		fmt.Fprintf(cfg.Stdout, "jj: running %d planning agents\n", cfg.PlanningAgents)
		drafts, agentResults, err := runPlanningAgents(ctx, planner, store, cfg.OpenAIModel, providerPlan, proposal, proposalPrompt, cfg.PlanningAgents, record)
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
			Model:                    cfg.OpenAIModel,
			Plan:                     providerPlan,
			Drafts:                   sanitizePlanningDrafts(drafts),
			TaskProposalMode:         string(proposal.Selected),
			ResolvedTaskProposalMode: string(proposal.Resolved),
			TaskProposalInstruction:  proposalPrompt,
		})
		recordMergeOutput := func(raw []byte) error {
			if p, err := writeHandoffFile(store, "planning/merge.json", raw); err != nil {
				return err
			} else {
				record("planning_merge", p)
			}
			if p, err := writeHandoffFile(store, "planning/merged.json", raw); err != nil {
				return err
			} else {
				record("planning_merged", p)
			}
			return nil
		}
		if err != nil {
			if len(raw) > 0 {
				if p, writeErr := writeHandoffFile(store, "planning/raw_response_merge.txt", raw); writeErr == nil {
					record("planning_merge_raw_response", p)
				}
			}
			return fail(StatusPlanningFailed, fmt.Errorf("merge planning outputs: %w", err))
		}
		if err := recordMergeOutput(raw); err != nil {
			return fail(StatusPlanningFailed, err)
		}
		if err := validateMergeResult(merged); err != nil {
			if len(raw) > 0 {
				if p, writeErr := writeHandoffFile(store, "planning/raw_response_merge.txt", raw); writeErr == nil {
					record("planning_merge_raw_response", p)
				}
			}
			if writeErr := recordPlanningOutput(normalizedPlanning(plannerSelection.Provider, drafts, merged, proposal)); writeErr != nil {
				return fail(StatusPlanningFailed, writeErr)
			}
			return fail(StatusPlanningFailed, fmt.Errorf("merge planning outputs: %w", err))
		}
		merged = sanitizeMergeResult(merged)
		drafts = sanitizePlanningDrafts(drafts)
		plannedSpecState = buildSpecState(originalPlan, merged, drafts, proposal, stateBefore.SpecBefore, stateNow)
		tasksState, selectedTask = buildTaskState(stateBefore.TasksBefore, originalPlan, merged, drafts, proposal, cfg.RunID, !cfg.DryRun, stateNow)
		manifest.SelectedTaskID = selectedTask.ID
		if err := writeRunEvent("task.proposed", map[string]string{
			"task_id": selectedTask.ID,
			"mode":    string(proposal.Resolved),
		}); err != nil {
			return fail(StatusPlanningFailed, err)
		}
		planningArtifact := normalizedPlanning(plannerSelection.Provider, drafts, merged, proposal)
		if err := recordPlanningOutput(planningArtifact); err != nil {
			return fail(StatusPlanningFailed, err)
		}
	}
	if p, err := writeSnapshotJSON(store, "snapshots/spec.planned.json", plannedSpecState); err != nil {
		return fail(StatusPlanningFailed, err)
	} else {
		record("snapshot_spec_planned", p)
	}
	if p, err := writeSnapshotJSON(store, "snapshots/tasks.after.json", tasksState); err != nil {
		return fail(StatusPlanningFailed, err)
	} else {
		record("snapshot_tasks_after", p)
	}
	record("spec_state", filepath.Join(store.RunDir, "snapshots", "spec.planned.json"))
	record("tasks_state", filepath.Join(store.RunDir, "snapshots", "tasks.after.json"))
	writeManifest(StatusPlanning, false)

	recordGitDiff := func(diff GitDiff, report security.RedactionReport) error {
		manifest.Git.FinalStatus = diff.Status
		manifest.Git.DirtyAfter = dirtyFromGitStatus(diff.Status)
		manifest.Git.Dirty = manifest.Git.DirtyAfter
		manifest.Git.DiffRedactionApplied = true
		manifest.Git.DiffRedactionCount = report.Count
		manifest.Git.DiffRedactionCategoryCounts = redactionCategoryCounts(report.Kinds)
		manifest.Git.DiffRedactionCategories = sortedSecurityCategories(manifest.Git.DiffRedactionCategoryCounts)
		manifest.Git.DiffArtifactLabels = gitDiffArtifactLabels()
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
		manifest.Security.Diagnostics.GitDiffArtifactsAvailable = true
		manifest.Security.Diagnostics.GitDiffRedactionApplied = true
		manifest.Security.Diagnostics.GitDiffRedactionCount = report.Count
		manifest.Security.Diagnostics.GitDiffRedactionCategoryCounts = manifest.Git.DiffRedactionCategoryCounts
		manifest.Security.Diagnostics.GitDiffRedactionCategories = manifest.Git.DiffRedactionCategories
		manifest.Security.Diagnostics.GitDiffArtifactLabels = manifest.Git.DiffArtifactLabels
		return nil
	}
	captureAndRecordGitDiff := func(label string) (GitDiff, error) {
		diff, err := CaptureGitDiff(ctx, cfg.CWD, gitState.Available, cfg.GitRunner)
		if err != nil {
			return GitDiff{}, fmt.Errorf("capture %s git diff: %w", label, err)
		}
		redacted, report := redactGitDiff(diff, cfg.CWD, store.RunDir)
		store.RecordRedactionReport(report)
		if err := recordGitDiff(redacted, report); err != nil {
			return GitDiff{}, err
		}
		return redacted, nil
	}

	if cfg.DryRun {
		if p, err := writeSnapshotJSON(store, "snapshots/spec.after.json", plannedSpecState); err != nil {
			return fail(StatusPlanningFailed, err)
		} else {
			record("snapshot_spec_after", p)
			record("spec_state", filepath.Join(store.RunDir, "snapshots", "spec.after.json"))
		}
		fmt.Fprintln(cfg.Stdout, "jj: capturing git diff")
		if _, err := captureAndRecordGitDiff("dry-run"); err != nil {
			return fail(StatusPlanningFailed, err)
		}
		manifest.Codex = ManifestCodex{Ran: false, Skipped: true, Status: "skipped", Model: cfg.CodexModel}
		manifest.Validation = ManifestValidation{
			Ran:            false,
			Skipped:        true,
			Status:         validationStatusSkipped,
			EvidenceStatus: validationEvidenceSkipped,
			Reason:         "skipped in dry-run mode",
			Summary:        "Validation was skipped in dry-run mode.",
		}
		fmt.Fprintf(cfg.Stdout, "jj: dry run complete\n")
		writeManifest(StatusDryRunComplete, true)
		fmt.Fprintf(cfg.Stdout, "run_id=%s\nrun_dir=%s\nprovider=%s\nspec_snapshot=%s\ntasks_snapshot=%s\nworkspace_state=skipped\nimplementation=skipped\nstatus=%s\nreview=jj serve --cwd %s\n", cfg.RunID, store.RunDir, plannerSelection.Provider, "snapshots/spec.after.json", "snapshots/tasks.after.json", StatusDryRunComplete, cfg.CWD)
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, nil
	}

	if err := writeWorkspaceJSON(cfg.CWD, taskRel, tasksState); err != nil {
		return fail(StatusPlanningFailed, fmt.Errorf("write %s: %w", taskRel, err))
	}
	workspaceModified = true
	manifest.Workspace.TaskWritten = true

	currentStage = StatusImplementing
	fmt.Fprintf(cfg.Stdout, "jj: wrote %s and planned %s\n", taskRel, specRel)
	writeManifest(StatusImplementing, false)

	runner := cfg.CodexRunner
	if runner == nil {
		runner = codex.Runner{}
	}
	const codexEventsRel = "codex/events.jsonl"
	const codexSummaryRel = "codex/summary.md"

	eventsPath, err := store.Path(codexEventsRel)
	if err != nil {
		return fail(StatusImplementationFailed, err)
	}
	summaryPath, err := store.Path(codexSummaryRel)
	if err != nil {
		return fail(StatusImplementationFailed, err)
	}
	fmt.Fprintln(cfg.Stdout, "jj: running codex exec")
	codexRequest := codex.Request{
		Bin:               cfg.CodexBin,
		CWD:               cfg.CWD,
		Model:             cfg.CodexModel,
		Prompt:            codexJSONPrompt(plannedSpecState, selectedTask, proposal),
		EventsPath:        eventsPath,
		OutputLastMessage: summaryPath,
		AllowNoGit:        cfg.AllowNoGit,
	}
	codexResult, codexErr := runner.Run(ctx, codexRequest)
	if err := ensureCodexArtifacts(store, codexResult, codexErr); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	if err := redactArtifactFile(store, codexEventsRel); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	if err := redactArtifactFile(store, codexSummaryRel); err != nil {
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
	if p, err := writeRedactedJSON(store, "codex/exit.json", codexExitArtifact(cfg.RunID, store.RunDir, codexRequest, codexStatus, codexResult, codexErr)); err != nil {
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
	codexResult.Summary = sanitizeHandoffText(codexResult.Summary,
		security.CommandPathRoot{Path: store.RunDir, Label: "[run]"},
		security.CommandPathRoot{Path: cfg.CWD, Label: "[workspace]"},
	)
	manifest.Codex.Summary = truncateString(codexResult.Summary, 2000)
	if codexErr != nil {
		safeCodexErr := sanitizeHandoffText(codexErr.Error())
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
		fmt.Fprintf(cfg.Stderr, "jj: codex failed, continuing to validation: %s\n", safeCodexErr)
	}
	writeManifest(StatusImplementing, false)

	currentStage = StatusValidating
	fmt.Fprintln(cfg.Stdout, "jj: running validation")
	writeManifest(StatusValidating, false)
	validationCommand := strings.TrimSpace(selectedTask.ValidationCommand)
	if validationCommand == "" {
		validationCommand = defaultValidationCommand
	}
	validation, validationErr := runValidationEvidenceCommands(ctx, cfg, store, []string{validationCommand})
	if validationErr != nil {
		return fail(StatusImplementationFailed, fmt.Errorf("record validation evidence: %w", validationErr))
	}
	manifest.Validation = validation
	recordValidationArtifacts(validation, recordRel)
	switch validation.Status {
	case validationStatusFailed:
		addRisks("Validation failed: " + validation.Summary)
	case validationStatusMissing, validationStatusSkipped:
		addRisks("Raw validation evidence unavailable: " + emptyFallback(validation.Reason, validation.Summary))
	}
	writeManifest(StatusImplementing, false)
	recordUntrackedEvidence := func(evidence UntrackedEvidence) error {
		manifest.Git.UntrackedAvailable = evidence.Available
		manifest.Git.UntrackedCount = len(evidence.Files)
		manifest.Git.UntrackedCapturedCount = len(evidence.Captured)
		manifest.Git.UntrackedSkippedCount = len(evidence.Skipped)
		setUntrackedDeniedPathDiagnostics(&manifest.Security.Diagnostics, evidence.Skipped)
		filesText := strings.Join(evidence.Files, "\n")
		if filesText != "" {
			filesText += "\n"
		}
		if p, err := store.WriteString("git/untracked-files.txt", redactSecrets(filesText)); err != nil {
			return err
		} else {
			record("git_untracked_files", p)
			manifest.Git.UntrackedFilesPath = "git/untracked-files.txt"
		}
		if p, err := store.WriteString("git/untracked.patch", redactSecrets(evidence.Patch)); err != nil {
			return err
		} else {
			record("git_untracked_patch", p)
			manifest.Git.UntrackedPatchPath = "git/untracked.patch"
		}
		if p, err := store.WriteString("git/untracked-summary.txt", redactSecrets(evidence.Summary)); err != nil {
			return err
		} else {
			record("git_untracked_summary", p)
			manifest.Git.UntrackedSummaryPath = "git/untracked-summary.txt"
		}
		return nil
	}

	fmt.Fprintln(cfg.Stdout, "jj: capturing git diff")
	diff, err := captureAndRecordGitDiff("current")
	if err != nil {
		return fail(StatusImplementationFailed, err)
	}
	untracked, err := CaptureUntrackedEvidence(ctx, cfg.CWD, gitState.Available, cfg.GitRunner)
	if err != nil {
		return fail(StatusImplementationFailed, fmt.Errorf("capture untracked evidence: %w", err))
	}
	if err := recordUntrackedEvidence(untracked); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	writeManifest(StatusValidating, false)
	tasksState = updateTaskAfterValidation(tasksState, selectedTask.ID, cfg.RunID, manifest.Validation, "", time.Now().UTC())
	if p, err := writeSnapshotJSON(store, "snapshots/tasks.after.json", tasksState); err != nil {
		return fail(StatusImplementationFailed, err)
	} else {
		record("snapshot_tasks_after", p)
	}
	if err := writeWorkspaceJSON(cfg.CWD, taskRel, tasksState); err != nil {
		return fail(StatusImplementationFailed, fmt.Errorf("write %s: %w", taskRel, err))
	}
	manifest.Workspace.TaskWritten = true
	finalSpecState := stateBefore.SpecBefore
	if codexErr == nil && manifest.Validation.Status == validationStatusPassed {
		fmt.Fprintln(cfg.Stdout, "jj: reconciling spec from validated result")
		reconciled, raw, err := planner.ReconcileSpec(ctx, ai.ReconcileSpecRequest{
			Model:             cfg.OpenAIModel,
			PreviousSpec:      sanitizeHandoffText(mustCompactJSON(stateBefore.SpecBefore)),
			PlannedSpec:       sanitizeHandoffText(mustCompactJSON(plannedSpecState)),
			SelectedTask:      sanitizeHandoffText(mustCompactJSON(selectedTask)),
			CodexSummary:      truncateString(sanitizeHandoffText(codexResult.Summary), 12000),
			GitDiffSummary:    truncateString(sanitizeHandoffText(diff.Markdown()), 12000),
			ValidationSummary: truncateString(sanitizeHandoffText(validationEvidenceForPrompt(manifest.Validation)), 12000),
		})
		if len(raw) > 0 {
			if p, writeErr := writeHandoffFile(store, "planning/spec-reconcile.json", raw); writeErr == nil {
				record("planning_spec_reconcile", p)
			}
		}
		if err != nil {
			return fail(StatusPartial, fmt.Errorf("reconcile spec from validated result: %w", err))
		}
		finalSpecState, err = buildReconciledSpecState(stateBefore.SpecBefore, plannedSpecState, reconciled, time.Now().UTC())
		if err != nil {
			return fail(StatusPartial, err)
		}
		if err := writeWorkspaceJSON(cfg.CWD, specRel, finalSpecState); err != nil {
			return fail(StatusPartial, fmt.Errorf("write %s: %w", specRel, err))
		}
		workspaceModified = true
		manifest.Workspace.SpecWritten = true
	} else {
		finalSpecState = stateBefore.SpecBefore
	}
	if p, err := writeSnapshotJSON(store, "snapshots/spec.after.json", finalSpecState); err != nil {
		return fail(StatusImplementationFailed, err)
	} else {
		record("snapshot_spec_after", p)
		record("spec_state", filepath.Join(store.RunDir, "snapshots", "spec.after.json"))
	}
	if _, err := captureAndRecordGitDiff("final"); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	if finalUntracked, err := CaptureUntrackedEvidence(ctx, cfg.CWD, gitState.Available, cfg.GitRunner); err != nil {
		return fail(StatusImplementationFailed, fmt.Errorf("capture final untracked evidence: %w", err))
	} else if err := recordUntrackedEvidence(finalUntracked); err != nil {
		return fail(StatusImplementationFailed, err)
	}
	if nextIntent.Active() && codexErr == nil && manifest.Validation.Status == validationStatusPassed {
		if err := clearNextIntent(cfg.CWD); err != nil {
			return fail(StatusPartial, err)
		}
		if err := writeRunEvent("next_intent.cleared", map[string]string{
			"path": DefaultNextIntentPath,
		}); err != nil {
			return fail(StatusPartial, err)
		}
	}
	status := StatusCompleted
	validationResultForCommit := strings.ToUpper(emptyFallback(manifest.Validation.Status, "unknown"))
	if codexErr != nil {
		status = StatusImplementationFailed
	} else if manifest.Validation.Status == validationStatusFailed {
		status = StatusPartial
	} else if manifest.Validation.Status != validationStatusPassed {
		status = StatusPartial
	}
	var finalErr error
	if gitState.Available && !cfg.DryRun {
		if status == StatusCompleted {
			if gitState.Dirty {
				manifest.Commit = ManifestCommit{Ran: false, Status: "skipped", Error: "skipped because workspace was dirty before run"}
			} else {
				commit := commitRepositoryTurn(ctx, cfg.CWD, proposal, selectedTask, cfg.RunID, validationResultForCommit)
				manifest.Commit = commit
				if repoRuntime != nil {
					manifest.Repository.HeadAfter = strings.TrimSpace(mustGitOutput(ctx, cfg.CWD, "rev-parse", "HEAD"))
				}
				if commit.Status == "success" {
					if repoRuntime != nil && cfg.Push && cfg.PushMode != PushModeNone {
						if err := writeRunEvent("github.push.started", map[string]string{
							"branch": manifest.Repository.WorkBranch,
							"remote": "origin",
						}); err != nil {
							return fail(StatusPartial, err)
						}
						pushedRef, err := pushRepositoryBranch(ctx, cfg.CWD, repoRuntime.Token, cfg.PushMode, manifest.Repository.WorkBranch)
						if err != nil {
							safeErr := redactSecrets(err.Error())
							manifest.Repository.PushStatus = "failed"
							manifest.Repository.Error = safeErr
							addError(fmt.Errorf("push failed for branch %s: %s", manifest.Repository.WorkBranch, safeErr))
							_ = writeRunEvent("github.push.failed", map[string]string{
								"branch": manifest.Repository.WorkBranch,
								"remote": "origin",
								"status": "failed",
								"error":  safeErr,
							})
							status = "completed_with_push_failure"
							finalErr = fmt.Errorf("push failed for branch %s: %s", manifest.Repository.WorkBranch, safeErr)
						} else {
							manifest.Repository.Pushed = true
							manifest.Repository.PushedRef = pushedRef
							manifest.Repository.PushStatus = "pushed"
							_ = writeRunEvent("github.push.completed", map[string]string{
								"branch":     manifest.Repository.WorkBranch,
								"remote":     "origin",
								"status":     "success",
								"pushed_ref": pushedRef,
							})
						}
					}
				} else if commit.Status == "failed" {
					status = StatusPartial
					finalErr = fmt.Errorf("git commit failed: %s", commit.Error)
					addError(finalErr)
				}
			}
		} else {
			manifest.Commit = ManifestCommit{Ran: false, Status: "skipped", Error: "skipped because run status was " + status}
		}
	}
	writeManifest(status, true)
	fmt.Fprintf(cfg.Stdout, "jj: done\nrun_id=%s\nrun_dir=%s\nprovider=%s\nspec=%s\ntasks=%s\nvalidation=%s\nimplementation=ran\nstatus=%s\ncodex_exit_code=%d\nreview=jj serve --cwd %s\n", cfg.RunID, store.RunDir, plannerSelection.Provider, specRel, taskRel, manifest.Validation.Status, status, manifest.Codex.ExitCode, cfg.CWD)
	if finalErr != nil {
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, finalErr
	}
	if codexErr != nil {
		return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, errors.New(sanitizeHandoffText(codexErr.Error()))
	}
	return &Result{RunID: cfg.RunID, RunDir: store.RunDir}, nil
}

func writeWorktreeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), artifact.PrivateDirMode); err != nil {
		return err
	}
	return artifact.AtomicWriteFile(path, data, artifact.PrivateFileMode)
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
	Provider   string   `json:"provider"`
	Name       string   `json:"name"`
	Model      string   `json:"model,omitempty"`
	CWD        string   `json:"cwd"`
	RunID      string   `json:"run_id"`
	Argv       []string `json:"argv"`
	Status     string   `json:"status"`
	ExitCode   int      `json:"exit_code"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error,omitempty"`
}

func codexExitArtifact(runID, runDir string, req codex.Request, status string, result codex.Result, err error) codexExitRecord {
	record := codexExitRecord{
		Provider: "codex",
		Name:     commandName(firstNonEmptyString(req.Bin, DefaultCodexBinary)),
		Model:    redactSecrets(req.Model),
		CWD:      "[workspace]",
		RunID:    redactSecrets(runID),
		Argv: security.SanitizeCommandArgv(
			append([]string{firstNonEmptyString(req.Bin, DefaultCodexBinary)}, codex.BuildArgs(req)...),
			security.CommandPathRoot{Path: runDir, Label: "[run]"},
			security.CommandPathRoot{Path: req.CWD, Label: "[workspace]"},
		),
		Status:     status,
		ExitCode:   result.ExitCode,
		DurationMS: result.DurationMS,
	}
	if err != nil {
		record.Error = sanitizeHandoffText(err.Error())
	}
	return record
}

func commandName(command string) string {
	name := filepath.Base(strings.TrimSpace(command))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "command"
	}
	return name
}

func dirtyFromGitStatus(status string) bool {
	status = strings.TrimSpace(status)
	return status != "" && status != "git unavailable"
}

func redactArtifactFile(store artifact.Store, rel string) error {
	if strings.TrimSpace(rel) == "" {
		return nil
	}
	path, err := store.Path(rel)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	redacted, report := security.SanitizeHandoffContentWithReport(rel, data,
		security.CommandPathRoot{Path: store.RunDir, Label: "[run]"},
		security.CommandPathRoot{Path: store.CWD, Label: "[workspace]"},
	)
	store.RecordRedactionReport(report)
	if string(redacted) == string(data) {
		return nil
	}
	path, err = store.Path(rel)
	if err != nil {
		return err
	}
	return artifact.AtomicWriteFile(path, redacted, artifact.PrivateFileMode)
}

func ensureCodexArtifacts(store artifact.Store, result codex.Result, runErr error) error {
	if err := ensureArtifactFileIfMissing(store, "codex/events.jsonl", `{"type":"notice","message":"codex produced no event log"}`+"\n"); err != nil {
		return err
	}
	summary := strings.TrimSpace(result.Summary)
	if runErr != nil {
		errText := sanitizeHandoffText(runErr.Error())
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
	return ensureArtifactFileIfMissing(store, "codex/summary.md", sanitizeHandoffText(summary,
		security.CommandPathRoot{Path: store.RunDir, Label: "[run]"},
		security.CommandPathRoot{Path: store.CWD, Label: "[workspace]"},
	)+"\n")
}

func ensureArtifactFileIfMissing(store artifact.Store, rel, content string) error {
	if strings.TrimSpace(rel) == "" {
		return nil
	}
	path, err := store.Path(rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return security.ErrSymlinkPath
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), artifact.PrivateDirMode); err != nil {
		return err
	}
	path, err = store.Path(rel)
	if err != nil {
		return err
	}
	safeContent := sanitizeHandoffText(content,
		security.CommandPathRoot{Path: store.RunDir, Label: "[run]"},
		security.CommandPathRoot{Path: store.CWD, Label: "[workspace]"},
	)
	return artifact.AtomicWriteFile(path, []byte(safeContent), artifact.PrivateFileMode)
}

func redactBytes(data []byte) []byte {
	return []byte(redactSecrets(string(data)))
}

func sanitizeHandoffText(s string, roots ...security.CommandPathRoot) string {
	return security.SanitizeHandoffString(s, roots...)
}

func writeHandoffFile(store artifact.Store, rel string, data []byte) (string, error) {
	sanitized, report := security.SanitizeHandoffContentWithReport(rel, data,
		security.CommandPathRoot{Path: store.RunDir, Label: "[run]"},
		security.CommandPathRoot{Path: store.CWD, Label: "[workspace]"},
	)
	store.RecordRedactionReport(report)
	return store.WriteFile(rel, sanitized)
}

func writeHandoffJSON(store artifact.Store, rel string, value any) (string, error) {
	sanitized, report := security.SanitizeHandoffJSONValueWithReport(value,
		security.CommandPathRoot{Path: store.RunDir, Label: "[run]"},
		security.CommandPathRoot{Path: store.CWD, Label: "[workspace]"},
	)
	store.RecordRedactionReport(report)
	data, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return "", err
	}
	return store.WriteFile(rel, append(data, '\n'))
}

func writeRedactedJSON(store artifact.Store, rel string, value any) (string, error) {
	redacted, report := security.RedactJSONValueWithReport(value)
	store.RecordRedactionReport(report)
	data, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return "", err
	}
	data = append([]byte(redactSecrets(string(data))), '\n')
	return store.WriteFile(rel, data)
}

func sanitizePlanningDraft(draft ai.PlanningDraft) ai.PlanningDraft {
	var out ai.PlanningDraft
	data, err := json.Marshal(security.SanitizeHandoffJSONValue(draft))
	if err != nil || json.Unmarshal(data, &out) != nil {
		return draft
	}
	return out
}

func sanitizePlanningDrafts(drafts []ai.PlanningDraft) []ai.PlanningDraft {
	out := make([]ai.PlanningDraft, len(drafts))
	for i, draft := range drafts {
		out[i] = sanitizePlanningDraft(draft)
	}
	return out
}

func sanitizeMergeResult(merged ai.MergeResult) ai.MergeResult {
	var out ai.MergeResult
	data, err := json.Marshal(security.SanitizeHandoffJSONValue(merged))
	if err != nil || json.Unmarshal(data, &out) != nil {
		return merged
	}
	return out
}

type normalizedPlanningResult struct {
	Provider                 string             `json:"provider"`
	Source                   string             `json:"source,omitempty"`
	TaskProposalMode         string             `json:"task_proposal_mode,omitempty"`
	ResolvedTaskProposalMode string             `json:"resolved_task_proposal_mode,omitempty"`
	TaskProposalReason       string             `json:"task_proposal_reason,omitempty"`
	SelectedTaskID           string             `json:"selected_task_id,omitempty"`
	Spec                     string             `json:"spec"`
	Task                     string             `json:"task"`
	Risks                    []string           `json:"risks"`
	Assumptions              []string           `json:"assumptions"`
	AcceptanceCriteria       []string           `json:"acceptance_criteria"`
	TestGuidance             []string           `json:"test_guidance"`
	Drafts                   []ai.PlanningDraft `json:"drafts"`
	Merge                    ai.MergeResult     `json:"merge"`
}

func normalizedPlanning(provider string, drafts []ai.PlanningDraft, merged ai.MergeResult, proposal TaskProposalResolution) normalizedPlanningResult {
	out := normalizedPlanningResult{
		Provider:                 provider,
		TaskProposalMode:         string(proposal.Selected),
		ResolvedTaskProposalMode: string(proposal.Resolved),
		TaskProposalReason:       proposal.Reason,
		SelectedTaskID:           proposal.SelectedTaskID,
		Spec:                     merged.Spec,
		Task:                     merged.Task,
		Drafts:                   drafts,
		Merge:                    merged,
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

func normalizedExistingTaskPlanning(selected TaskRecord, spec SpecState, tasks TaskState, proposal TaskProposalResolution) normalizedPlanningResult {
	specJSON := mustCompactJSON(spec)
	taskJSON := mustCompactJSON(TaskState{
		Version:      tasks.Version,
		ActiveTaskID: tasks.ActiveTaskID,
		Tasks:        []TaskRecord{selected},
	})
	return normalizedPlanningResult{
		Provider:                 "existing_task",
		Source:                   "existing_task",
		TaskProposalMode:         string(proposal.Selected),
		ResolvedTaskProposalMode: string(proposal.Resolved),
		TaskProposalReason:       proposal.Reason,
		SelectedTaskID:           selected.ID,
		Spec:                     specJSON,
		Task:                     taskJSON,
		Merge: ai.MergeResult{
			Spec:  specJSON,
			Task:  taskJSON,
			Notes: []string{"Selected existing runnable task before proposing new work."},
		},
	}
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

func redactGitDiff(diff GitDiff, roots ...string) (GitDiff, security.RedactionReport) {
	commandRoots := make([]security.CommandPathRoot, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		label := "[path]"
		switch len(commandRoots) {
		case 0:
			label = "[workspace]"
		case 1:
			label = "[run]"
		}
		commandRoots = append(commandRoots, security.CommandPathRoot{Path: root, Label: label})
	}
	report := security.RedactionReport{}
	diff.Status = sanitizeGitDiffText(diff.Status, &report, commandRoots...)
	diff.Stat = sanitizeGitDiffText(diff.Stat, &report, commandRoots...)
	diff.NameStatus = sanitizeGitDiffText(diff.NameStatus, &report, commandRoots...)
	diff.Full = sanitizeGitDiffText(diff.Full, &report, commandRoots...)
	return diff, report
}

func sanitizeGitDiffText(text string, report *security.RedactionReport, roots ...security.CommandPathRoot) string {
	redacted, textReport := security.SanitizeDisplayStringWithReport(text, roots...)
	if report != nil {
		report.Merge(textReport)
	}
	return redacted
}

func redactionCategoryCounts(kinds map[string]int) map[string]int {
	out := map[string]int{}
	for kind, count := range kinds {
		if count <= 0 {
			continue
		}
		category := safeSecurityCategory(kind, "redaction")
		if category == "" {
			category = "redaction"
		}
		out[category] += count
	}
	return out
}

func gitDiffArtifactLabels() []string {
	return []string{
		"git_diff",
		"git_diff_stat",
		"git_diff_summary",
		"git_status",
		"git_status_after",
	}
}

func validateCWD(cwd string) error {
	_, err := security.ResolveCommandCWD(cwd)
	return err
}

func runPlanningAgents(ctx context.Context, planner PlanningClient, store artifact.Store, model, plan string, proposal TaskProposalResolution, proposalPrompt string, count int, record func(string, string)) ([]ai.PlanningDraft, []ManifestPlanningAgent, error) {
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
				Model:                    model,
				Plan:                     plan,
				Agent:                    agent,
				TaskProposalMode:         string(proposal.Selected),
				ResolvedTaskProposalMode: string(proposal.Resolved),
				TaskProposalInstruction:  proposalPrompt,
			})
			name := fmt.Sprintf("planning/%s.json", agent.Name)
			if err != nil {
				errText := fmt.Sprintf("agent %s failed: %v", agent.Name, err)
				if len(raw) > 0 {
					errText += "\n\nraw response excerpt:\n" + truncateString(sanitizeHandoffText(string(raw)), 4000)
					if path, writeErr := writeHandoffFile(store, fmt.Sprintf("planning/raw_response_%s.txt", agent.Name), raw); writeErr == nil {
						record("planning_"+agent.Name+"_raw_response", path)
					}
				}
				path, writeErr := store.WriteString(fmt.Sprintf("planning/%s.error.txt", agent.Name), sanitizeHandoffText(errText)+"\n")
				if writeErr == nil {
					record("planning_"+agent.Name+"_error", path)
					results[i].Artifact = relArtifactPath(store, path)
				}
				results[i].Error = sanitizeHandoffText(err.Error())
				errs <- fmt.Errorf("%s planning failed: %w", agent.Name, err)
				return
			}
			draft = sanitizePlanningDraft(draft)
			if strings.TrimSpace(draft.Agent) == "" {
				draft.Agent = agent.Name
			}
			if err := validatePlanningDraft(agent.Name, draft); err != nil {
				errText := fmt.Sprintf("agent %s returned incomplete planning draft: %v", agent.Name, err)
				path, writeErr := store.WriteString(fmt.Sprintf("planning/%s.error.txt", agent.Name), sanitizeHandoffText(errText)+"\n")
				if writeErr == nil {
					record("planning_"+agent.Name+"_error", path)
					results[i].Artifact = relArtifactPath(store, path)
				}
				results[i].Error = sanitizeHandoffText(err.Error())
				errs <- fmt.Errorf("%s planning failed: %w", agent.Name, err)
				return
			}
			path, err := writeHandoffFile(store, name, raw)
			if err != nil {
				errs <- err
				results[i].Error = sanitizeHandoffText(err.Error())
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
		return errors.New("merged SPEC content is empty")
	}
	if strings.TrimSpace(merged.Task) == "" {
		return errors.New("merged TASK content is empty")
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
		{Name: "qa_validation", Focus: "identify risks, edge cases, failure scenarios, deterministic validation plans, and regression guards"},
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

func sortedRedactionKinds(counts map[string]int64) []string {
	kinds := make([]string, 0, len(counts))
	for kind, count := range counts {
		if strings.TrimSpace(kind) != "" && count > 0 {
			kinds = append(kinds, kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}
