package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jungju/jj/internal/codex"
	ai "github.com/jungju/jj/internal/openai"
	"github.com/jungju/jj/internal/security"
)

func TestExecuteRejectsInvalidPlanningAgents(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "invalid-agents",
		PlanningAgents: 0,
		AllowNoGit:     true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
	})
	if err == nil || !strings.Contains(err.Error(), "planning-agents") {
		t.Fatalf("expected planning-agents error, got %v", err)
	}
}

func TestExecuteRejectsInvalidRunID(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "../x",
		PlanningAgents: 1,
		AllowNoGit:     true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
	})
	if err == nil || !strings.Contains(err.Error(), "run id") {
		t.Fatalf("expected run-id validation error, got %v", err)
	}
}

func TestExecuteRejectsExternalPlanBeforeArtifactPersistence(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := "sk-proj-externalplansecret1234567890"
	outsidePlan := filepath.Join(outside, "plan.md")
	if err := os.WriteFile(outsidePlan, []byte("outside\n"+secret+"\n"), 0o644); err != nil {
		t.Fatalf("write outside plan: %v", err)
	}

	for _, tc := range []struct {
		name   string
		dryRun bool
	}{
		{name: "dry-run", dryRun: true},
		{name: "full-run", dryRun: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runID := "external-plan-" + strings.ReplaceAll(tc.name, "-", "")
			_, err := Execute(context.Background(), Config{
				PlanPath:       outsidePlan,
				CWD:            dir,
				RunID:          runID,
				PlanningAgents: 1,
				OpenAIModel:    "test-model",
				DryRun:         tc.dryRun,
				AllowNoGit:     true,
				Stdout:         io.Discard,
				Planner:        &fakePlanner{},
				CodexRunner:    &fakeCodexRunner{},
			})
			if err == nil || !strings.Contains(err.Error(), "outside workspace") {
				t.Fatalf("expected external plan rejection, got %v", err)
			}
			for _, leaked := range []string{outsidePlan, filepath.ToSlash(outsidePlan), secret} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("external plan error leaked %q: %v", leaked, err)
				}
			}
			assertNoFile(t, filepath.Join(dir, ".jj", "runs", runID))
		})
	}
}

func TestExecuteRejectsWorkspaceStateSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writePlan(t, dir, "plan.md")
	if err := os.Symlink(outside, filepath.Join(dir, ".jj")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "doc-symlink-escape",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected workspace state symlink rejection, got %v", err)
	}
	assertNoFile(t, filepath.Join(outside, "spec.json"))
	assertNoFile(t, filepath.Join(outside, "tasks.json"))
}

func TestExecuteRejectsWorkspaceStateSymlinkInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	if err := os.MkdirAll(filepath.Join(dir, "state-target"), 0o755); err != nil {
		t.Fatalf("mkdir state target: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "state-target"), filepath.Join(dir, ".jj")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "doc-symlink-internal",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "symlinked path") {
		t.Fatalf("expected workspace state symlink rejection, got %v", err)
	}
	assertNoFile(t, filepath.Join(dir, "state-target", "spec.json"))
	assertNoFile(t, filepath.Join(dir, "state-target", "tasks.json"))
}

func TestExecuteDryRunCreatesPlanningArtifactsOnly(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{}
	codexRunner := &fakeCodexRunner{}

	result, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "dry-run",
		PlanningAgents: 3,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if result.RunID != "dry-run" {
		t.Fatalf("unexpected run id: %s", result.RunID)
	}
	if codexRunner.called {
		t.Fatal("codex should not run in dry-run")
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "spec.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "tasks.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "eval.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "snapshots", "spec.before.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "snapshots", "spec.planned.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "snapshots", "spec.after.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "snapshots", "tasks.before.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "snapshots", "tasks.after.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "runs", "dry-run", "snapshots", "eval.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "input", "plan.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning", "product_spec.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning", "merged.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning", "planning.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "git", "baseline.txt"))
	assertNoFile(t, filepath.Join(dir, "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "TASK.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "TASK.md"))
	taskData := readFile(t, filepath.Join(dir, ".jj", "runs", "dry-run", "snapshots", "tasks.after.json"))
	if !strings.Contains(taskData, `"status": "queued"`) || !strings.Contains(taskData, `"mode": "feature"`) {
		t.Fatalf("dry-run task should use JSON queue state:\n%s", taskData)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "dry-run", "manifest.json"))
	if manifest.Status != StatusPlanned || !manifest.DryRun {
		t.Fatalf("unexpected dry-run manifest: %#v", manifest)
	}
	if manifest.RiskCount == 0 {
		t.Fatalf("expected risk count in manifest: %#v", manifest)
	}
	if !manifest.NoGitMode || !manifest.Config.AllowNoGit {
		t.Fatalf("expected no-git mode to be recorded: %#v", manifest)
	}
	if manifest.EndedAt == "" || manifest.InputPath == "" || !manifest.RedactionApplied {
		t.Fatalf("expected spec-compatible manifest metadata: %#v", manifest)
	}
	if manifest.Planner.Provider != plannerProviderInjected || manifest.Planner.Model == "" || manifest.Planner.Artifacts["planning_merged"] != "planning/merged.json" {
		t.Fatalf("expected nested planner metadata: %#v", manifest.Planner)
	}
	if manifest.Git.BaselineTextPath != "git/baseline.txt" || manifest.Git.DirtyAfter != manifest.Git.DirtyBefore {
		t.Fatalf("expected git compatibility metadata: %#v", manifest.Git)
	}
	if manifest.Git.BaselinePath != "git/baseline.json" || manifest.Artifacts["git_baseline"] != "git/baseline.json" {
		t.Fatalf("expected git baseline artifact in manifest: %#v", manifest)
	}
	if manifest.Git.StatusBeforePath != "git/status.before.txt" || manifest.Artifacts["git_status_before"] != "git/status.before.txt" {
		t.Fatalf("expected git status.before artifact in manifest: %#v", manifest)
	}
	if manifest.Artifacts["planning"] != "planning.json" || manifest.Artifacts["planning_normalized"] != "planning/planning.json" {
		t.Fatalf("expected spec-compatible planning artifact paths, got %#v", manifest.Artifacts)
	}
	planning := readPlanning(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning.json"))
	if planning.Spec == "" || planning.Task == "" || len(planning.AcceptanceCriteria) == 0 || len(planning.TestGuidance) == 0 {
		t.Fatalf("normalized planning json missing top-level fields: %#v", planning)
	}
}

func TestExecuteDryRunPreservesExistingWorkspaceState(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	specPath := filepath.Join(dir, ".jj", "spec.json")
	taskPath := filepath.Join(dir, ".jj", "tasks.json")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	specBefore := `{"version":1,"title":"existing","summary":"keep me"}` + "\n"
	tasksBefore := `{"version":1,"active_task_id":null,"tasks":[]}` + "\n"
	if err := os.WriteFile(specPath, []byte(specBefore), 0o644); err != nil {
		t.Fatalf("write spec state: %v", err)
	}
	if err := os.WriteFile(taskPath, []byte(tasksBefore), 0o644); err != nil {
		t.Fatalf("write task state: %v", err)
	}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "dry-run-preserve",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
	})
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if got := readFile(t, specPath); got != specBefore {
		t.Fatalf("dry-run modified workspace spec:\n%s", got)
	}
	if got := readFile(t, taskPath); got != tasksBefore {
		t.Fatalf("dry-run modified workspace tasks:\n%s", got)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "dry-run-preserve", "manifest.json"))
	if manifest.Workspace.SpecWritten || manifest.Workspace.TaskWritten {
		t.Fatalf("dry-run should not record workspace writes, got %#v", manifest.Workspace)
	}
}

func TestExecuteTaskProposalModePersistsPromptsAndEvents(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:         filepath.Join(dir, "plan.md"),
		CWD:              dir,
		RunID:            "proposal-feature",
		PlanningAgents:   1,
		OpenAIModel:      "test-model",
		TaskProposalMode: TaskProposalModeFeature,
		Stdout:           io.Discard,
		Planner:          planner,
		CodexRunner:      &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("execute task proposal mode run: %v", err)
	}

	runDir := filepath.Join(dir, ".jj", "runs", "proposal-feature")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.TaskProposalMode != TaskProposalModeFeature || manifest.ResolvedTaskProposalMode != TaskProposalModeFeature || manifest.SelectedTaskID != "TASK-0001" {
		t.Fatalf("unexpected proposal manifest metadata: %#v", manifest)
	}
	if manifest.Config.TaskProposalMode != string(TaskProposalModeFeature) {
		t.Fatalf("manifest config missing proposal mode: %#v", manifest.Config)
	}
	planning := readPlanning(t, filepath.Join(runDir, "planning.json"))
	if planning.TaskProposalMode != string(TaskProposalModeFeature) || planning.ResolvedTaskProposalMode != string(TaskProposalModeFeature) || planning.SelectedTaskID != "TASK-0001" {
		t.Fatalf("planning metadata missing proposal mode: %#v", planning)
	}
	events := readFile(t, filepath.Join(runDir, "events.jsonl"))
	for _, want := range []string{"task_proposal_mode.selected", `"mode":"feature"`, "task_proposal_mode.resolved", `"resolved_mode":"feature"`, "task.proposed", `"task_id":"TASK-0001"`} {
		if !strings.Contains(events, want) {
			t.Fatalf("events missing %q:\n%s", want, events)
		}
	}
	if len(planner.draftRequests) != 1 {
		t.Fatalf("expected one draft request, got %d", len(planner.draftRequests))
	}
	for _, req := range []struct {
		name        string
		mode        string
		resolved    string
		instruction string
	}{
		{"draft", planner.draftRequests[0].TaskProposalMode, planner.draftRequests[0].ResolvedTaskProposalMode, planner.draftRequests[0].TaskProposalInstruction},
		{"merge", planner.lastMergeRequest.TaskProposalMode, planner.lastMergeRequest.ResolvedTaskProposalMode, planner.lastMergeRequest.TaskProposalInstruction},
	} {
		if req.mode != string(TaskProposalModeFeature) || req.resolved != string(TaskProposalModeFeature) || !strings.Contains(req.instruction, "Task Proposal Mode: feature") {
			t.Fatalf("%s request missing proposal mode context: %#v", req.name, req)
		}
	}
	taskData := readFile(t, filepath.Join(runDir, "snapshots", "tasks.after.json"))
	if !strings.Contains(taskData, `"id": "TASK-0001"`) || !strings.Contains(taskData, `"mode": "feature"`) {
		t.Fatalf("tasks.json missing proposal mode metadata:\n%s", taskData)
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "eval.json"))
}

func TestExecuteTaskProposalModeCriticalBlockerOverridesConcreteMode(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:              filepath.Join(dir, "plan.md"),
		CWD:                   dir,
		RunID:                 "proposal-blocked",
		PlanningAgents:        1,
		OpenAIModel:           "test-model",
		AllowNoGit:            true,
		DryRun:                true,
		TaskProposalMode:      TaskProposalModeFeature,
		AdditionalPlanContext: "validation failed and blocks feature work",
		Stdout:                io.Discard,
		Planner:               &fakePlanner{},
	})
	if err != nil {
		t.Fatalf("execute blocked proposal dry-run: %v", err)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "proposal-blocked")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.TaskProposalMode != TaskProposalModeFeature || manifest.ResolvedTaskProposalMode != TaskProposalModeBugfix {
		t.Fatalf("expected feature to resolve bugfix, got %#v", manifest)
	}
	if !strings.Contains(manifest.TaskProposalReason, "overridden") || manifest.SelectedTaskID != "TASK-0001" {
		t.Fatalf("expected override reason and bugfix task id, got %#v", manifest)
	}
	taskData := readFile(t, filepath.Join(runDir, "snapshots", "tasks.after.json"))
	if !strings.Contains(taskData, `"id": "TASK-0001"`) || !strings.Contains(taskData, `"mode": "bugfix"`) || !strings.Contains(taskData, `"selected_task_proposal_mode": "feature"`) {
		t.Fatalf("tasks.json missing bugfix override metadata:\n%s", taskData)
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "eval.json"))
}

func TestExecuteNextIntentOverridePersistsContextAndPreservesOnPassedValidation(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	secret := "sk-proj-prioritysecret1234567890"
	intent := "Web UI About page feature only.\nOPENAI_API_KEY=" + secret + "\n"
	writeNextIntent(t, dir, intent)
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:         filepath.Join(dir, "plan.md"),
		CWD:              dir,
		RunID:            "next-intent-success",
		PlanningAgents:   1,
		OpenAIModel:      "test-model",
		TaskProposalMode: TaskProposalModeSecurity,
		Stdout:           io.Discard,
		Planner:          planner,
		CodexRunner:      &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("execute next intent run: %v", err)
	}

	runDir := filepath.Join(dir, ".jj", "runs", "next-intent-success")
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != intent {
		t.Fatalf("next intent should be preserved after passed validation, got %q", got)
	}
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.TaskProposalMode != TaskProposalModeSecurity || manifest.ResolvedTaskProposalMode != TaskProposalModeSecurity {
		t.Fatalf("next intent should preserve mode metadata while overriding task choice guidance, got %#v", manifest)
	}
	if manifest.Artifacts["input_next_intent"] != "input/next-intent.md" {
		t.Fatalf("manifest missing next intent artifact: %#v", manifest.Artifacts)
	}
	intentArtifact := readFile(t, filepath.Join(runDir, "input", "next-intent.md"))
	if !strings.Contains(intentArtifact, "Web UI About page feature only.") || strings.Contains(intentArtifact, secret) || !strings.Contains(intentArtifact, "[jj-omitted]") {
		t.Fatalf("next intent artifact should contain redacted content:\n%s", intentArtifact)
	}
	input := readFile(t, filepath.Join(runDir, "input.md"))
	if !strings.Contains(input, "# Next Intent Override") || !strings.Contains(input, "Web UI About page feature only.") || strings.Contains(input, secret) {
		t.Fatalf("planning input missing redacted next intent override:\n%s", input)
	}
	if len(planner.draftRequests) != 1 {
		t.Fatalf("expected one draft request, got %d", len(planner.draftRequests))
	}
	for _, req := range []struct {
		name        string
		plan        string
		instruction string
	}{
		{"draft", planner.draftRequests[0].Plan, planner.draftRequests[0].TaskProposalInstruction},
		{"merge", planner.lastMergeRequest.Plan, planner.lastMergeRequest.TaskProposalInstruction},
	} {
		if !strings.Contains(req.plan, "# Next Intent Override") || !strings.Contains(req.plan, "Web UI About page feature only.") || strings.Contains(req.plan, secret) {
			t.Fatalf("%s request missing redacted next intent context:\n%s", req.name, req.plan)
		}
		if !strings.Contains(req.instruction, "Task Proposal Mode: security") || !strings.Contains(req.instruction, ".jj/next-intent.md is active") || !strings.Contains(req.instruction, "Ignore task-proposal-mode") || !strings.Contains(req.instruction, "Use mode only after satisfying the intent") {
			t.Fatalf("%s request missing priority instruction:\n%s", req.name, req.instruction)
		}
	}
	events := readFile(t, filepath.Join(runDir, "events.jsonl"))
	if strings.Contains(events, "next_intent.cleared") {
		t.Fatalf("events should not record next intent clearing:\n%s", events)
	}
}

func TestExecuteNextIntentDryRunDoesNotClear(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	intent := "Keep next intent for full run.\n"
	writeNextIntent(t, dir, intent)

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "next-intent-dry-run",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
	})
	if err != nil {
		t.Fatalf("execute next intent dry-run: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != intent {
		t.Fatalf("dry-run should preserve next intent, got %q", got)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "next-intent-dry-run", "manifest.json"))
	if manifest.Artifacts["input_next_intent"] != "input/next-intent.md" {
		t.Fatalf("dry-run manifest missing next intent artifact: %#v", manifest.Artifacts)
	}
}

func TestExecuteNextIntentKeptOnValidationFailure(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	writeValidationScript(t, dir, "exit 7")
	intent := "Keep next intent after failed validation.\n"
	writeNextIntent(t, dir, intent)

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "next-intent-validation-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("validation failure should not abort next intent run: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != intent {
		t.Fatalf("validation failure should preserve next intent, got %q", got)
	}
}

func TestExecuteNextIntentKeptOnPlanningFailure(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	intent := "Keep next intent after planning failure.\n"
	writeNextIntent(t, dir, intent)

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "next-intent-planning-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{failAll: true},
	})
	if err == nil {
		t.Fatal("expected planning failure")
	}
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != intent {
		t.Fatalf("planning failure should preserve next intent, got %q", got)
	}
}

func TestExecuteNextIntentKeptOnCodexFailure(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	intent := "Keep next intent after Codex failure.\n"
	writeNextIntent(t, dir, intent)

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "next-intent-codex-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{err: errors.New("boom")},
	})
	if err == nil {
		t.Fatal("expected codex failure")
	}
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != intent {
		t.Fatalf("codex failure should preserve next intent, got %q", got)
	}
}

func TestExecuteNextIntentKeptOnSpecReconcileFailure(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	intent := "Keep next intent after reconcile failure.\n"
	writeNextIntent(t, dir, intent)

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "next-intent-reconcile-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{reconcileErr: errors.New("reconcile failed")},
		CodexRunner:    &fakeCodexRunner{mutate: true},
	})
	if err == nil {
		t.Fatal("expected reconcile failure")
	}
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != intent {
		t.Fatalf("reconcile failure should preserve next intent, got %q", got)
	}
}

func TestExecuteDryRunRecordsAlternateDocumentPathsWithoutWorkspaceWrites(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	codexRunner := &fakeCodexRunner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "dry-run-alt-docs",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute dry run with alternate docs: %v", err)
	}
	if codexRunner.called {
		t.Fatal("implementation codex runner should not run in dry-run")
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "dry-run-alt-docs", "manifest.json"))
	if manifest.Config.SpecPath != ".jj/spec.json" || manifest.Config.TaskPath != ".jj/tasks.json" {
		t.Fatalf("expected canonical JSON paths in manifest config, got %#v", manifest.Config)
	}
	if manifest.Workspace.SpecPath != ".jj/spec.json" || manifest.Workspace.TaskPath != ".jj/tasks.json" {
		t.Fatalf("expected canonical JSON workspace paths in manifest, got %#v", manifest.Workspace)
	}
	if manifest.Workspace.SpecWritten || manifest.Workspace.TaskWritten {
		t.Fatalf("dry-run should not write workspace JSON state, got %#v", manifest.Workspace)
	}
	assertNoFile(t, filepath.Join(dir, "docs", "ALT_SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "ALT_TASK.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "TASK.md"))
}

func TestExecuteDryRunAcceptsInlinePlanText(t *testing.T) {
	dir := t.TempDir()
	planner := &fakePlanner{}

	result, err := Execute(context.Background(), Config{
		PlanText:       "Build from a web prompt.\n",
		PlanInputName:  DefaultWebPromptInput,
		CWD:            dir,
		RunID:          "inline-plan",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        planner,
	})
	if err != nil {
		t.Fatalf("execute inline plan dry run: %v", err)
	}
	if result.RunID != "inline-plan" {
		t.Fatalf("unexpected run id: %s", result.RunID)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "inline-plan")
	input := readFile(t, filepath.Join(runDir, "input-original.md"))
	if input != "Build from a web prompt.\n" {
		t.Fatalf("unexpected inline input artifact:\n%s", input)
	}
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.InputSource != PlanInputSourceWebPrompt || manifest.PlanPath != "" || manifest.InputPath != DefaultWebPromptInput {
		t.Fatalf("unexpected inline input manifest metadata: %#v", manifest)
	}
	assertNoFile(t, filepath.Join(dir, "plan.md"))
}

func TestExecuteRejectsEmptyInlinePlanWithoutPath(t *testing.T) {
	dir := t.TempDir()

	_, err := Execute(context.Background(), Config{
		PlanText:       " \n\t",
		CWD:            dir,
		RunID:          "empty-inline-plan",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
	})
	if err == nil || !strings.Contains(err.Error(), "plan file is required") {
		t.Fatalf("expected empty inline plan validation error, got %v", err)
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "runs", "empty-inline-plan", "manifest.json"))
}

func TestExecuteWritesContinuationInputArtifacts(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:              filepath.Join(dir, "plan.md"),
		CWD:                   dir,
		RunID:                 "continuation",
		PlanningAgents:        1,
		OpenAIModel:           "test-model",
		AllowNoGit:            true,
		DryRun:                true,
		AdditionalPlanContext: "Previous Manifest: keep going",
		Stdout:                io.Discard,
		Planner:               &fakePlanner{},
	})
	if err != nil {
		t.Fatalf("execute continuation dry run: %v", err)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "continuation")
	input := readFile(t, filepath.Join(runDir, "input.md"))
	original := readFile(t, filepath.Join(runDir, "input-original.md"))
	contextInput := readFile(t, filepath.Join(runDir, "input-context.md"))
	if !strings.Contains(input, "Recent Run Evidence") || !strings.Contains(input, "Previous Manifest") {
		t.Fatalf("input.md missing continuation context:\n%s", input)
	}
	if strings.Contains(original, "Previous Manifest") {
		t.Fatalf("input-original.md should remain unchanged:\n%s", original)
	}
	if !strings.Contains(contextInput, "Previous Manifest") {
		t.Fatalf("input-context.md missing context:\n%s", contextInput)
	}
}

func TestExecuteRedactsProviderPromptInputs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JJ_PLAN_SECRET", "literal-plan-secret")
	planSecret := "plain-plan-secret"
	plannerSecret := "sk-proj-plannersecret1234567890"
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("Build safely.\nOPENAI_API_KEY="+planSecret+"\nliteral-plan-secret\n"), 0o644); err != nil {
		t.Fatalf("write secret plan: %v", err)
	}
	planner := &fakePlanner{secret: plannerSecret}

	_, err := Execute(context.Background(), Config{
		PlanPath:              filepath.Join(dir, "plan.md"),
		CWD:                   dir,
		RunID:                 "provider-redaction",
		PlanningAgents:        1,
		OpenAIModel:           "test-model",
		AllowNoGit:            true,
		AdditionalPlanContext: "password=continuation-secret",
		Stdout:                io.Discard,
		Planner:               planner,
		CodexRunner:           &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("execute provider redaction run: %v", err)
	}

	leaks := []string{planSecret, "literal-plan-secret", "continuation-secret", plannerSecret}
	for _, req := range planner.draftRequests {
		for _, leak := range leaks[:3] {
			if strings.Contains(req.Plan, leak) {
				t.Fatalf("draft request leaked %q:\n%s", leak, req.Plan)
			}
		}
		if !strings.Contains(req.Plan, "[jj-omitted]") {
			t.Fatalf("draft request should retain redaction evidence:\n%s", req.Plan)
		}
	}
	for _, surface := range []struct {
		name string
		text string
	}{
		{"merge plan", planner.lastMergeRequest.Plan},
	} {
		for _, leak := range leaks {
			if strings.Contains(surface.text, leak) {
				t.Fatalf("%s leaked %q:\n%s", surface.name, leak, surface.text)
			}
		}
		if !strings.Contains(surface.text, "[jj-omitted]") {
			t.Fatalf("%s should include redaction marker:\n%s", surface.name, surface.text)
		}
	}
}

func TestExecutePersistsLoopMetadata(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:          filepath.Join(dir, "plan.md"),
		CWD:               dir,
		RunID:             "loop-run-t02",
		PlanningAgents:    1,
		OpenAIModel:       "test-model",
		Stdout:            io.Discard,
		Planner:           &fakePlanner{},
		CodexRunner:       &fakeCodexRunner{mutate: true},
		LoopEnabled:       true,
		LoopBaseRunID:     "loop-run",
		LoopTurn:          2,
		LoopMaxTurns:      5,
		LoopPreviousRunID: "loop-run",
	})
	if err != nil {
		t.Fatalf("execute loop metadata run: %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "loop-run-t02", "manifest.json"))
	if manifest.Loop == nil || !manifest.Loop.Enabled || manifest.Loop.BaseRunID != "loop-run" || manifest.Loop.Turn != 2 || manifest.Loop.MaxTurns != 5 || manifest.Loop.PreviousRunID != "loop-run" {
		t.Fatalf("unexpected loop metadata: %#v", manifest.Loop)
	}
}

func TestExecuteEndToEndWithFakes(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{}
	codexRunner := &fakeCodexRunner{mutate: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "full",
		PlanningAgents: 3,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !codexRunner.called {
		t.Fatal("codex should have run")
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "spec.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "tasks.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "snapshots", "spec.planned.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "planning", "spec-reconcile.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "eval.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "runs", "full", "snapshots", "eval.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "codex", "events.jsonl"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "codex", "exit.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "git", "diff.stat.txt"))
	if !strings.Contains(codexRunner.lastRequest.Prompt, "SPEC summary") || !strings.Contains(codexRunner.lastRequest.Prompt, "Selected task") {
		t.Fatalf("codex prompt should contain compact JSON context:\n%s", codexRunner.lastRequest.Prompt)
	}
	if strings.Contains(codexRunner.lastRequest.Prompt, "docs/TASK.md") {
		t.Fatalf("codex prompt should not depend on markdown task docs:\n%s", codexRunner.lastRequest.Prompt)
	}
	taskData := readFile(t, filepath.Join(dir, ".jj", "tasks.json"))
	if !strings.Contains(taskData, `"status": "done"`) || !strings.Contains(taskData, `"mode": "feature"`) {
		t.Fatalf("workspace task should use canonical JSON state:\n%s", taskData)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "full", "manifest.json"))
	if manifest.SchemaVersion != "1" || manifest.Status != StatusCompleted || manifest.Validation.Status != validationStatusPassed {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Artifacts["snapshot_spec_after"] != "snapshots/spec.after.json" || manifest.Artifacts["snapshot_tasks_after"] != "snapshots/tasks.after.json" || manifest.Artifacts["snapshot_eval"] != "" {
		t.Fatalf("unexpected artifact paths: %#v", manifest.Artifacts)
	}
	if manifest.Artifacts["snapshot_spec_planned"] != "snapshots/spec.planned.json" || manifest.Artifacts["planning_spec_reconcile"] != "planning/spec-reconcile.json" {
		t.Fatalf("expected planned and reconciliation artifacts: %#v", manifest.Artifacts)
	}
	if manifest.Config.OpenAIKeySet || manifest.Config.OpenAIKeyEnv == "" {
		t.Fatalf("unexpected manifest metadata: %#v", manifest)
	}
	if !manifest.RedactionApplied || !manifest.WorkspaceGuardrailsApplied || !manifest.Security.RedactionApplied || !manifest.Security.WorkspaceGuardrailsApplied || !strings.Contains(manifest.Security.PathPolicy, "symlink escapes") || !strings.Contains(manifest.Security.CommandPolicy, "argv-style") {
		t.Fatalf("manifest missing security policy metadata: %#v", manifest.Security)
	}
	if manifest.Git.StatusAfterPath != "git/status.after.txt" || manifest.Artifacts["git_status_after"] != "git/status.after.txt" {
		t.Fatalf("expected git status.after artifact in manifest: %#v", manifest)
	}
	if manifest.Git.DiffStatPath != "git/diff.stat.txt" || manifest.Artifacts["git_diff_stat"] != "git/diff.stat.txt" {
		t.Fatalf("expected git diff stat artifact in manifest: %#v", manifest.Git)
	}
	if manifest.Codex.EventsPath != "codex/events.jsonl" || manifest.Codex.SummaryPath != "codex/summary.md" || manifest.Codex.ExitPath != "codex/exit.json" || manifest.Codex.Status != "success" || manifest.Codex.Summary == "" {
		t.Fatalf("expected codex evidence in manifest: %#v", manifest.Codex)
	}
	exitData := readFile(t, filepath.Join(dir, ".jj", "runs", "full", "codex", "exit.json"))
	if !strings.Contains(exitData, `"provider": "codex"`) || !strings.Contains(exitData, `"run_id": "full"`) || !strings.Contains(exitData, `"argv"`) || strings.Contains(exitData, "OPENAI_API_KEY") || strings.Contains(exitData, dir) {
		t.Fatalf("codex command metadata should be sanitized and argv-style:\n%s", exitData)
	}
	if !strings.Contains(exitData, `"[workspace]"`) || !strings.Contains(exitData, `"[run]/codex/summary.md"`) {
		t.Fatalf("codex command metadata should use safe path labels:\n%s", exitData)
	}
	if len(manifest.Risks) == 0 || manifest.Risks[0] != "risk" {
		t.Fatalf("expected planning risk summary in manifest, got %#v", manifest.Risks)
	}
	if manifest.Commit.Ran || manifest.Commit.Status != "skipped" || manifest.Commit.SHA != "" {
		t.Fatalf("default non-dry-run should not commit, got %#v", manifest.Commit)
	}
}

func TestExecuteDoesNotRewriteWorkspaceSpecBeforeImplementation(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	specPath := filepath.Join(dir, ".jj", "spec.json")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	specBefore := `{"version":1,"title":"Existing SPEC","summary":"old source of truth","requirements":["keep old until validation passes"],"created_at":"2026-04-27T00:00:00Z","updated_at":"2026-04-27T00:00:00Z"}` + "\n"
	if err := os.WriteFile(specPath, []byte(specBefore), 0o644); err != nil {
		t.Fatalf("write spec before: %v", err)
	}
	planner := &fakePlanner{}
	codexRunner := &fakeCodexRunner{
		mutate: true,
		beforeRun: func(_ codex.Request) error {
			got, err := os.ReadFile(specPath)
			if err != nil {
				return err
			}
			if string(got) != specBefore {
				return errors.New("workspace spec changed before implementation")
			}
			return nil
		},
	}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "spec-write-timing",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute spec timing run: %v", err)
	}
	if !codexRunner.called {
		t.Fatal("codex should have run")
	}
	if len(planner.reconcileRequests) != 1 {
		t.Fatalf("expected one spec reconciliation request, got %d", len(planner.reconcileRequests))
	}
	specAfter := readFile(t, specPath)
	if !strings.Contains(specAfter, "Implement the requested behavior.") || strings.Contains(specAfter, "old source of truth") {
		t.Fatalf("workspace spec should be reconciled only after validation passes:\n%s", specAfter)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "spec-write-timing", "manifest.json"))
	if !manifest.Workspace.SpecWritten || manifest.Validation.Status != validationStatusPassed {
		t.Fatalf("expected passed validation and final spec write: %#v", manifest)
	}
}

func TestExecuteFailedValidationLeavesWorkspaceSpecUnchanged(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	writeValidationScript(t, dir, "exit 9")
	specPath := filepath.Join(dir, ".jj", "spec.json")
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	specBefore := `{"version":1,"title":"Previous SPEC","summary":"keep on failure","requirements":["stable"],"created_at":"2026-04-27T00:00:00Z","updated_at":"2026-04-27T00:00:00Z"}` + "\n"
	if err := os.WriteFile(specPath, []byte(specBefore), 0o644); err != nil {
		t.Fatalf("write spec before: %v", err)
	}
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "spec-validation-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("failed validation should finish partial without aborting: %v", err)
	}
	if got := readFile(t, specPath); got != specBefore {
		t.Fatalf("workspace spec should remain unchanged on validation failure:\n%s", got)
	}
	if len(planner.reconcileRequests) != 0 {
		t.Fatalf("reconciliation should not run when validation fails, got %d requests", len(planner.reconcileRequests))
	}
	runDir := filepath.Join(dir, ".jj", "runs", "spec-validation-fail")
	specAfter := readFile(t, filepath.Join(runDir, "snapshots", "spec.after.json"))
	if !strings.Contains(specAfter, "Previous SPEC") || strings.Contains(specAfter, "Implement the requested behavior.") {
		t.Fatalf("failed run spec.after should be the unchanged pre-run spec:\n%s", specAfter)
	}
	tasks := readTaskState(t, filepath.Join(dir, ".jj", "tasks.json"))
	if len(tasks.Tasks) == 0 || tasks.Tasks[len(tasks.Tasks)-1].Status != "failed" {
		t.Fatalf("selected task should record failed validation, got %#v", tasks.Tasks)
	}
}

func TestExecuteRunsExistingInProgressTaskBeforePlanningNewWork(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	nextIntent := "Plan a brand new feature later.\n"
	writeNextIntent(t, dir, nextIntent)
	if err := os.MkdirAll(filepath.Join(dir, ".jj"), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	existingTasks := `{
		"version": 1,
		"active_task_id": "TASK-0001",
		"tasks": [{
			"id": "TASK-0001",
			"title": "Existing active feature",
			"mode": "feature",
			"priority": "high",
			"status": "in_progress",
			"reason": "existing",
			"acceptance_criteria": ["existing works"],
			"validation_command": "./scripts/validate.sh"
		}]
	}`
	if err := os.WriteFile(filepath.Join(dir, ".jj", "tasks.json"), []byte(existingTasks), 0o644); err != nil {
		t.Fatalf("write tasks state: %v", err)
	}
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "existing-task-full",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("execute existing task run: %v", err)
	}
	if len(planner.draftRequests) != 0 || strings.TrimSpace(planner.lastMergeRequest.Plan) != "" {
		t.Fatalf("planner draft/merge should be skipped for existing work, drafts=%d merge=%#v", len(planner.draftRequests), planner.lastMergeRequest)
	}

	state := readTaskState(t, filepath.Join(dir, ".jj", "tasks.json"))
	if state.ActiveTaskID != nil {
		t.Fatalf("completed run should clear active task, got %#v", state.ActiveTaskID)
	}
	if len(state.Tasks) != 1 {
		t.Fatalf("expected no appended task when existing work is runnable, got %#v", state.Tasks)
	}
	if state.Tasks[0].ID != "TASK-0001" || state.Tasks[0].Status != "done" || state.Tasks[0].CompletedByRun == nil || *state.Tasks[0].CompletedByRun != "existing-task-full" {
		t.Fatalf("existing active task should be selected and completed, got %#v", state.Tasks[0])
	}
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != nextIntent {
		t.Fatalf("existing work should preserve next intent, got %q", got)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "existing-task-full")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.SelectedTaskID != "TASK-0001" || manifest.Validation.Status != validationStatusPassed {
		t.Fatalf("manifest should point at existing selected task, got selected=%s validation=%#v", manifest.SelectedTaskID, manifest.Validation)
	}
	if _, ok := manifest.Artifacts["input_next_intent"]; ok {
		t.Fatalf("next intent should not be artifacted when existing work runs: %#v", manifest.Artifacts)
	}
	planning := readPlanning(t, filepath.Join(runDir, "planning.json"))
	if planning.Provider != "existing_task" || planning.SelectedTaskID != "TASK-0001" {
		t.Fatalf("planning artifact should record existing task source, got %#v", planning)
	}
	events := readFile(t, filepath.Join(runDir, "events.jsonl"))
	if !strings.Contains(events, "task.selected") || !strings.Contains(events, `"source":"existing"`) || strings.Contains(events, "task.proposed") {
		t.Fatalf("events should record existing task selection only:\n%s", events)
	}
}

func TestExecutePromotesExistingQueuedTaskBeforePlanningNewWork(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	if err := os.MkdirAll(filepath.Join(dir, ".jj"), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	existingTasks := `{
		"version": 1,
		"active_task_id": null,
		"tasks": [{
			"id": "TASK-0001",
			"title": "Existing queued feature",
			"mode": "feature",
			"priority": "high",
			"status": "queued",
			"reason": "existing",
			"acceptance_criteria": ["existing works"],
			"validation_command": "./scripts/validate.sh"
		}]
	}`
	if err := os.WriteFile(filepath.Join(dir, ".jj", "tasks.json"), []byte(existingTasks), 0o644); err != nil {
		t.Fatalf("write tasks state: %v", err)
	}
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "existing-queued-full",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("execute queued existing task run: %v", err)
	}
	if len(planner.draftRequests) != 0 || strings.TrimSpace(planner.lastMergeRequest.Plan) != "" {
		t.Fatalf("planner draft/merge should be skipped for queued existing work, drafts=%d merge=%#v", len(planner.draftRequests), planner.lastMergeRequest)
	}
	state := readTaskState(t, filepath.Join(dir, ".jj", "tasks.json"))
	if len(state.Tasks) != 1 || state.Tasks[0].ID != "TASK-0001" || state.Tasks[0].Status != "done" {
		t.Fatalf("queued existing task should be completed without appending, got %#v", state.Tasks)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "existing-queued-full", "manifest.json"))
	if manifest.SelectedTaskID != "TASK-0001" {
		t.Fatalf("manifest should select queued existing task, got %#v", manifest)
	}
}

func TestExecuteDryRunReportsExistingTaskWithoutConsumingNextIntent(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	nextIntent := "Use later after the queue is empty.\n"
	writeNextIntent(t, dir, nextIntent)
	if err := os.MkdirAll(filepath.Join(dir, ".jj"), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	existingTasks := `{
		"version": 1,
		"active_task_id": null,
		"tasks": [{
			"id": "TASK-0001",
			"title": "Existing queued dry-run task",
			"mode": "feature",
			"priority": "high",
			"status": "queued",
			"reason": "existing",
			"acceptance_criteria": ["existing works"],
			"validation_command": "./scripts/validate.sh"
		}]
	}`
	taskPath := filepath.Join(dir, ".jj", "tasks.json")
	if err := os.WriteFile(taskPath, []byte(existingTasks), 0o644); err != nil {
		t.Fatalf("write tasks state: %v", err)
	}
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "existing-dry-run",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        planner,
	})
	if err != nil {
		t.Fatalf("execute existing dry-run: %v", err)
	}
	if len(planner.draftRequests) != 0 || strings.TrimSpace(planner.lastMergeRequest.Plan) != "" {
		t.Fatalf("planner draft/merge should be skipped for existing dry-run, drafts=%d merge=%#v", len(planner.draftRequests), planner.lastMergeRequest)
	}
	if got := readFile(t, taskPath); got != existingTasks {
		t.Fatalf("dry-run should not mutate workspace tasks, got:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, DefaultNextIntentPath)); got != nextIntent {
		t.Fatalf("dry-run existing work should preserve next intent, got %q", got)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "existing-dry-run")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.SelectedTaskID != "TASK-0001" {
		t.Fatalf("dry-run manifest should select existing task, got %#v", manifest)
	}
	if _, ok := manifest.Artifacts["input_next_intent"]; ok {
		t.Fatalf("dry-run existing task should not artifact next intent: %#v", manifest.Artifacts)
	}
}

func TestExecuteRecordsSuccessfulValidationEvidence(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	secret := "sk-proj-validationsecret1234567890"
	writeValidationScript(t, dir, `
printf 'validation ok\n'
printf 'OPENAI_API_KEY=`+secret+`\n'
printf 'Authorization: Bearer `+secret+`\n' >&2
`)
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "validation-pass",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("execute validation pass: %v", err)
	}

	runDir := filepath.Join(dir, ".jj", "runs", "validation-pass")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if !manifest.Validation.Ran || manifest.Validation.Skipped || manifest.Validation.Status != validationStatusPassed || manifest.Validation.EvidenceStatus != validationEvidenceRecorded {
		t.Fatalf("unexpected validation manifest: %#v", manifest.Validation)
	}
	if manifest.Validation.CommandCount != 1 || manifest.Validation.PassedCount != 1 || len(manifest.Validation.Commands) != 1 {
		t.Fatalf("unexpected validation counts: %#v", manifest.Validation)
	}
	command := manifest.Validation.Commands[0]
	if command.Label != "validate" || command.Command != "" || command.ExitCode != 0 || command.Status != validationStatusPassed || command.StdoutPath == "" || command.StderrPath == "" || command.Summary == "" {
		t.Fatalf("unexpected validation command result: %#v", command)
	}
	if command.Provider != "local" || command.RunID != "validation-pass" || command.CWD != "[workspace]" || len(command.Argv) != 1 || command.Argv[0] != "./scripts/validate.sh" {
		t.Fatalf("validation command metadata should be sanitized argv-style, got %#v", command)
	}
	for _, key := range []string{"validation_results", "validation_summary", "validation_001_stdout", "validation_001_stderr"} {
		if manifest.Artifacts[key] == "" {
			t.Fatalf("manifest missing %s artifact: %#v", key, manifest.Artifacts)
		}
	}
	stdout := readFile(t, filepath.Join(runDir, command.StdoutPath))
	stderr := readFile(t, filepath.Join(runDir, command.StderrPath))
	if !strings.Contains(stdout, "validation ok") || !strings.Contains(stdout, "[jj-omitted]") || !strings.Contains(stderr, "Authorization: [jj-omitted]") {
		t.Fatalf("validation output was not captured/redacted\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("validation output leaked secret\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	resultsData := readFile(t, filepath.Join(runDir, manifest.Validation.ResultsPath))
	if !strings.Contains(resultsData, `"status": "passed"`) || strings.Contains(resultsData, secret) {
		t.Fatalf("validation results missing redacted passed status:\n%s", resultsData)
	}
	assertNoFile(t, filepath.Join(runDir, "snapshots", "eval.json"))
}

func TestExecuteRecordsFailingValidationEvidence(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	writeValidationScript(t, dir, `
printf 'validation stdout\n'
printf 'validation stderr\n' >&2
exit 7
`)

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "validation-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("validation failure should be recorded without aborting the run: %v", err)
	}

	runDir := filepath.Join(dir, ".jj", "runs", "validation-fail")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.Status != StatusPartial || manifest.Validation.Status != validationStatusFailed || manifest.Validation.FailedCount != 1 {
		t.Fatalf("unexpected failed validation manifest: status=%s validation=%#v", manifest.Status, manifest.Validation)
	}
	command := manifest.Validation.Commands[0]
	if command.ExitCode != 7 || command.Status != validationStatusFailed || command.Error == "" {
		t.Fatalf("unexpected failed validation command: %#v", command)
	}
	if stdout := readFile(t, filepath.Join(runDir, command.StdoutPath)); !strings.Contains(stdout, "validation stdout") {
		t.Fatalf("stdout not captured:\n%s", stdout)
	}
	if stderr := readFile(t, filepath.Join(runDir, command.StderrPath)); !strings.Contains(stderr, "validation stderr") {
		t.Fatalf("stderr not captured:\n%s", stderr)
	}
	taskData := readFile(t, filepath.Join(dir, ".jj", "tasks.json"))
	if !strings.Contains(taskData, `"status": "failed"`) || !strings.Contains(taskData, `"verdict": "failed"`) {
		t.Fatalf("tasks.json should record failed validation:\n%s", taskData)
	}
	assertNoFile(t, filepath.Join(runDir, "snapshots", "eval.json"))
}

func TestExecuteRecordsMissingValidationState(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "validation-missing",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("execute missing validation: %v", err)
	}

	runDir := filepath.Join(dir, ".jj", "runs", "validation-missing")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.Validation.Ran || !manifest.Validation.Skipped || manifest.Validation.Status != validationStatusMissing || manifest.Validation.EvidenceStatus != validationEvidenceMissing {
		t.Fatalf("unexpected missing validation manifest: %#v", manifest.Validation)
	}
	if manifest.Validation.ResultsPath != "validation/results.json" || manifest.Validation.SummaryPath != "validation/summary.md" {
		t.Fatalf("missing validation should still point to structured artifacts: %#v", manifest.Validation)
	}
	assertFileExists(t, filepath.Join(runDir, "validation", "results.json"))
	assertFileExists(t, filepath.Join(runDir, "validation", "summary.md"))
	resultsData := readFile(t, filepath.Join(runDir, manifest.Validation.ResultsPath))
	if !strings.Contains(resultsData, `"evidence_status": "missing"`) {
		t.Fatalf("validation results should distinguish missing raw validation evidence:\n%s", resultsData)
	}
	assertNoFile(t, filepath.Join(runDir, "snapshots", "eval.json"))
}

func TestExecuteCapturesUntrackedTextEvidence(t *testing.T) {
	dir := initGit(t)
	prepareCommittedWorkspace(t, dir)
	secret := "sk-proj-untracked1234567890"
	planner := &fakePlanner{}
	codexRunner := &fakeCodexRunner{
		files: map[string][]byte{
			"new-script.sh":     []byte("#!/bin/sh\napi_key=" + secret + "\n"),
			"tests/new_test.go": []byte("package tests\n"),
		},
	}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "untracked-text",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	runDir := filepath.Join(dir, ".jj", "runs", "untracked-text")
	files := readFile(t, filepath.Join(runDir, "git", "untracked-files.txt"))
	patch := readFile(t, filepath.Join(runDir, "git", "untracked.patch"))
	summary := readFile(t, filepath.Join(runDir, "git", "untracked-summary.txt"))
	for _, want := range []string{"new-script.sh", "tests/new_test.go"} {
		if !strings.Contains(files, want) || !strings.Contains(patch, want) || !strings.Contains(summary, want) {
			t.Fatalf("untracked evidence missing %q\nfiles:\n%s\npatch:\n%s\nsummary:\n%s", want, files, patch, summary)
		}
	}
	for _, leaked := range []string{secret, ".jj/runs"} {
		if strings.Contains(files, leaked) || strings.Contains(patch, leaked) || strings.Contains(summary, leaked) {
			t.Fatalf("untracked evidence leaked %q\nfiles:\n%s\npatch:\n%s\nsummary:\n%s", leaked, files, patch, summary)
		}
	}
	if !strings.Contains(patch, "[jj-omitted]") {
		t.Fatalf("untracked patch should redact secret content:\n%s", patch)
	}

	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.Git.UntrackedFilesPath != "git/untracked-files.txt" ||
		manifest.Git.UntrackedPatchPath != "git/untracked.patch" ||
		manifest.Git.UntrackedSummaryPath != "git/untracked-summary.txt" {
		t.Fatalf("manifest missing untracked artifact paths: %#v", manifest.Git)
	}
	if manifest.Artifacts["git_untracked_files"] != "git/untracked-files.txt" ||
		manifest.Artifacts["git_untracked_patch"] != "git/untracked.patch" ||
		manifest.Artifacts["git_untracked_summary"] != "git/untracked-summary.txt" {
		t.Fatalf("manifest missing untracked artifacts: %#v", manifest.Artifacts)
	}
	if !manifest.Git.UntrackedAvailable || manifest.Git.UntrackedCount != 2 || manifest.Git.UntrackedCapturedCount != 2 || manifest.Git.UntrackedSkippedCount != 0 {
		t.Fatalf("unexpected untracked counts: %#v", manifest.Git)
	}
	assertNoFile(t, filepath.Join(runDir, "snapshots", "eval.json"))
	if cached := strings.TrimSpace(runGitOutput(t, dir, "diff", "--cached", "--name-only")); cached != "" {
		t.Fatalf("jj run should not stage untracked evidence, got cached diff:\n%s", cached)
	}
}

func TestExecuteSkipsUnsafeUntrackedEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior requires elevated privileges on some Windows setups")
	}
	dir := initGit(t)
	prepareCommittedWorkspace(t, dir)
	codexRunner := &fakeCodexRunner{
		files: map[string][]byte{
			"ignored.txt": []byte("ignored\n"),
			"binary.dat":  []byte{0x00, 0x01, 0x02},
			"huge.txt":    []byte(strings.Repeat("x", int(untrackedEvidenceMaxBytes)+1)),
		},
		symlinks: map[string]string{
			"linked.txt": "target.txt",
		},
	}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "untracked-unsafe",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	runDir := filepath.Join(dir, ".jj", "runs", "untracked-unsafe")
	files := readFile(t, filepath.Join(runDir, "git", "untracked-files.txt"))
	patch := readFile(t, filepath.Join(runDir, "git", "untracked.patch"))
	summary := readFile(t, filepath.Join(runDir, "git", "untracked-summary.txt"))
	for _, want := range []string{"binary.dat", "huge.txt", "linked.txt"} {
		if !strings.Contains(files, want) || !strings.Contains(summary, want) {
			t.Fatalf("unsafe untracked path missing %q\nfiles:\n%s\nsummary:\n%s", want, files, summary)
		}
		if strings.Contains(patch, want) {
			t.Fatalf("unsafe file %q should not be inlined:\n%s", want, patch)
		}
	}
	if strings.Contains(files, "ignored.txt") || strings.Contains(summary, "ignored.txt") || strings.Contains(patch, "ignored.txt") {
		t.Fatalf("gitignored file should be excluded from untracked evidence\nfiles:\n%s\nsummary:\n%s\npatch:\n%s", files, summary, patch)
	}
	for _, want := range []string{"binary file", "oversized file", "symlink"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing skip reason %q:\n%s", want, summary)
		}
	}
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.Git.UntrackedCount != 3 || manifest.Git.UntrackedCapturedCount != 0 || manifest.Git.UntrackedSkippedCount != 3 {
		t.Fatalf("unexpected unsafe untracked counts: %#v", manifest.Git)
	}
	diag := manifest.Security.Diagnostics
	if diag.DeniedPathCount != 3 || diag.DeniedPathCategoryCounts["untracked_binary_file"] != 1 || diag.DeniedPathCategoryCounts["untracked_oversized_file"] != 1 || diag.DeniedPathCategoryCounts["untracked_symlink"] != 1 {
		t.Fatalf("expected denied path diagnostics by category, got %#v", diag)
	}
	for _, skipped := range []string{"binary.dat", "huge.txt", "linked.txt"} {
		if strings.Contains(strings.Join(diag.DeniedPathCategories, ","), skipped) {
			t.Fatalf("denied path diagnostics leaked skipped path %q: %#v", skipped, diag)
		}
	}
	if cached := strings.TrimSpace(runGitOutput(t, dir, "diff", "--cached", "--name-only")); cached != "" {
		t.Fatalf("jj run should not stage unsafe untracked evidence, got cached diff:\n%s", cached)
	}
}

func TestExecuteNonDryRunCommitsCleanGitWorkspace(t *testing.T) {
	dir := initGit(t)
	runGit(t, dir, "config", "user.email", "jj@example.com")
	runGit(t, dir, "config", "user.name", "jj test")
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jj/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGit(t, dir, "add", "--all")
	runGit(t, dir, "commit", "-m", "initial")
	headBefore := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD"))
	planner := &fakePlanner{mergeTask: plannedTaskJSON("Refresh dashboard controls")}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "local-commit-turn",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("execute non-dry-run: %v", err)
	}
	headAfter := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD"))
	if headAfter == headBefore {
		t.Fatalf("HEAD should advance after successful clean git run, before=%s after=%s", headBefore, headAfter)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "local-commit-turn", "manifest.json"))
	wantSubject := "jj: TASK-0001 Refresh dashboard controls"
	if !manifest.Commit.Ran || manifest.Commit.Status != "success" || manifest.Commit.SHA != headAfter || manifest.Commit.Message != wantSubject {
		t.Fatalf("expected successful commit metadata, got %#v head=%s", manifest.Commit, headAfter)
	}
	if strings.Contains(manifest.Commit.Message, "Add the next useful user-facing capability") {
		t.Fatalf("commit metadata should use selected task title, got %q", manifest.Commit.Message)
	}
	subject := strings.TrimSpace(runGitOutput(t, dir, "log", "-1", "--pretty=%s"))
	if subject != wantSubject {
		t.Fatalf("unexpected commit subject: %q", subject)
	}
	committedFiles := runGitOutput(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	for _, want := range []string{"fake.go", ".jj/spec.json", ".jj/tasks.json"} {
		if !strings.Contains(committedFiles, want) {
			t.Fatalf("commit should include %s, files:\n%s", want, committedFiles)
		}
	}
	if strings.Contains(committedFiles, ".jj/runs/") {
		t.Fatalf("commit should not include run artifacts:\n%s", committedFiles)
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "eval.json"))
	if cached := strings.TrimSpace(runGitOutput(t, dir, "diff", "--cached", "--name-only")); cached != "" {
		t.Fatalf("jj run should leave no staged files after commit, got cached diff:\n%s", cached)
	}
}

func TestExecuteAutoPRBranchesPushesAndCreatesIntentPR(t *testing.T) {
	dir := initGit(t)
	runGit(t, dir, "checkout", "-b", "main")
	runGit(t, dir, "config", "user.email", "jj@example.com")
	runGit(t, dir, "config", "user.name", "jj test")
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jj/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGit(t, dir, "add", "--all")
	runGit(t, dir, "commit", "-m", "initial")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGitClone(t, "--bare", dir, origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	runGit(t, dir, "remote", "set-url", "--push", "origin", origin)
	t.Setenv("JJ_GITHUB_TOKEN", "test-token")
	intent := "Web UI About page feature\n\nAcceptance: dashboard links to About.\n"
	writeNextIntent(t, dir, intent)
	client := &fakeGitHubPRClient{}
	planner := &fakePlanner{mergeTask: plannedTaskJSON("Build About page")}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "auto-pr-success",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AutoPR:         true,
		AutoPRExplicit: true,
		Stdout:         io.Discard,
		Planner:        planner,
		CodexRunner:    &fakeCodexRunner{mutate: true},
		GitHubClient:   client,
	})
	if err != nil {
		t.Fatalf("execute auto-pr run: %v", err)
	}

	hash := shortHash(intent)
	workBranch := "jj/intent-web-ui-about-" + hash
	if branch := strings.TrimSpace(runGitOutput(t, dir, "branch", "--show-current")); branch != workBranch {
		t.Fatalf("unexpected branch: %q", branch)
	}
	if !gitRefExists(t, origin, "refs/heads/"+workBranch) {
		t.Fatalf("expected pushed branch %s", workBranch)
	}
	if client.createCalls != 1 || client.findCalls != 1 {
		t.Fatalf("expected one PR lookup and create, find=%d create=%d", client.findCalls, client.createCalls)
	}
	if client.createReq.Title != "Web UI About page feature" {
		t.Fatalf("unexpected PR title: %#v", client.createReq)
	}
	for _, forbidden := range []string{hash, workBranch, "jj/intent-"} {
		if strings.Contains(client.createReq.Title, forbidden) || strings.Contains(client.createReq.Body, forbidden) {
			t.Fatalf("PR text should not contain branch hash/name %q:\n%s\n%s", forbidden, client.createReq.Title, client.createReq.Body)
		}
	}
	if !strings.Contains(client.createReq.Body, "Web UI About page feature") || !strings.Contains(client.createReq.Body, "TASK-0001 Build About page") {
		t.Fatalf("PR body missing intent/task metadata:\n%s", client.createReq.Body)
	}

	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "auto-pr-success", "manifest.json"))
	if !manifest.Repository.Enabled || manifest.Repository.WorkBranch != workBranch || !manifest.Repository.Pushed || manifest.Repository.PRStatus != "created" || manifest.Repository.PRNumber != 12 {
		t.Fatalf("unexpected repository metadata: %#v", manifest.Repository)
	}
	tasks := readTaskState(t, filepath.Join(dir, ".jj", "tasks.json"))
	selected := taskByID(tasks, "TASK-0001", TaskProposalResolution{})
	if selected.WorkBranch != workBranch || selected.NextIntentHash != hash {
		t.Fatalf("task missing workstream metadata: %#v", selected)
	}
}

func TestExecuteAutoPRValidationFailureDoesNotPushOrCreatePR(t *testing.T) {
	dir := initGit(t)
	runGit(t, dir, "checkout", "-b", "main")
	runGit(t, dir, "config", "user.email", "jj@example.com")
	runGit(t, dir, "config", "user.name", "jj test")
	writePlan(t, dir, "plan.md")
	writeValidationScript(t, dir, "exit 1")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jj/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGit(t, dir, "add", "--all")
	runGit(t, dir, "commit", "-m", "initial")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGitClone(t, "--bare", dir, origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	runGit(t, dir, "remote", "set-url", "--push", "origin", origin)
	t.Setenv("JJ_GITHUB_TOKEN", "test-token")
	intent := "Quality pass\n"
	writeNextIntent(t, dir, intent)
	client := &fakeGitHubPRClient{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "auto-pr-validation-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AutoPR:         true,
		AutoPRExplicit: true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{mutate: true},
		GitHubClient:   client,
	})
	if err != nil {
		t.Fatalf("validation failure should not abort auto-pr run: %v", err)
	}

	workBranch := "jj/intent-quality-pass-" + shortHash(intent)
	if gitRefExists(t, origin, "refs/heads/"+workBranch) {
		t.Fatalf("validation failure should not push branch %s", workBranch)
	}
	if client.findCalls != 0 || client.createCalls != 0 {
		t.Fatalf("validation failure should not call PR API, find=%d create=%d", client.findCalls, client.createCalls)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "auto-pr-validation-fail", "manifest.json"))
	if manifest.Repository.Pushed || manifest.Repository.PRStatus == "created" {
		t.Fatalf("validation failure should not push or create PR: %#v", manifest.Repository)
	}
}

func TestExecuteDirtyWorkspacePreservedAndRecorded(t *testing.T) {
	dir := initGit(t)
	runGit(t, dir, "config", "user.email", "jj@example.com")
	runGit(t, dir, "config", "user.name", "jj test")
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jj/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGit(t, dir, "add", "--all")
	runGit(t, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	headBefore := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD"))

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "dirty-preserved-turn",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("execute dirty workspace: %v", err)
	}
	headAfter := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("HEAD changed: before=%s after=%s", headBefore, headAfter)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "dirty-preserved-turn", "manifest.json"))
	if !manifest.Git.DirtyBefore || !manifest.Git.DirtyAfter {
		t.Fatalf("expected dirty flags to be recorded, got %#v", manifest.Git)
	}
	if manifest.Commit.Ran || manifest.Commit.Status != "skipped" || !strings.Contains(manifest.Commit.Error, "workspace was dirty before run") {
		t.Fatalf("expected skipped commit metadata, got %#v", manifest.Commit)
	}
	before := readFile(t, filepath.Join(dir, ".jj", "runs", "dirty-preserved-turn", "git", "status.before.txt"))
	if !strings.Contains(before, "dirty.txt") {
		t.Fatalf("expected dirty file in baseline status, got %q", before)
	}
	status := runGitOutput(t, dir, "status", "--short")
	if !strings.Contains(status, "dirty.txt") {
		t.Fatalf("expected dirty file to remain in status, got %q", status)
	}
	if cached := strings.TrimSpace(runGitOutput(t, dir, "diff", "--cached", "--name-only")); cached != "" {
		t.Fatalf("jj run should not stage dirty files, got cached diff:\n%s", cached)
	}
}

func TestExecuteAllowNoGitRecordsGitUnavailableAndNoCommit(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "nogit-commit-skip",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("execute no-git: %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "nogit-commit-skip", "manifest.json"))
	if manifest.Git.Available || !manifest.Git.NoGit {
		t.Fatalf("expected git unavailable metadata, got %#v", manifest.Git)
	}
	if manifest.Commit.Ran || manifest.Commit.Status != "skipped" || manifest.Commit.Error != "" {
		t.Fatalf("expected no commit metadata, got %#v", manifest.Commit)
	}
}

func TestExecuteDoesNotInvokeGitMutationCommands(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "no-git-mutation",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
		GitRunner: fakeGitRunner{
			outputs: map[string]string{
				"rev-parse --show-toplevel": dir,
				"rev-parse HEAD":            "abc123",
				"branch --show-current":     "main",
				"status --short":            "M .jj/spec.json",
				"diff --stat":               " .jj/spec.json | 1 +",
				"diff --name-status":        "M\t.jj/spec.json",
				"diff --binary":             "diff --git a/.jj/spec.json b/.jj/spec.json",
			},
		},
	})
	if err != nil {
		t.Fatalf("execute with fake git runner: %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "no-git-mutation", "manifest.json"))
	if manifest.Commit.Ran || manifest.Commit.Status != "skipped" {
		t.Fatalf("expected skipped commit metadata, got %#v", manifest.Commit)
	}
}

func TestExecutePathModeStillWritesCanonicalJSONState(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "root-docs",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("execute root docs: %v", err)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "spec.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "tasks.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "eval.json"))
	assertNoFile(t, filepath.Join(dir, "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "SPEC.md"))
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "root-docs", "manifest.json"))
	if manifest.Workspace.SpecPath != ".jj/spec.json" || manifest.Workspace.TaskPath != ".jj/tasks.json" {
		t.Fatalf("expected canonical JSON workspace paths in manifest: %#v", manifest)
	}
}

func TestExecuteIgnoresCustomDocumentNamesForCanonicalJSONState(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	codexRunner := &fakeCodexRunner{mutate: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "custom-docs",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute custom docs: %v", err)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "spec.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "tasks.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "eval.json"))
	assertNoFile(t, filepath.Join(dir, "docs", "PRODUCT.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "WORK.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "REVIEW.md"))
	if !strings.Contains(codexRunner.lastRequest.Prompt, "Selected task") {
		t.Fatalf("codex prompt should include compact selected task context:\n%s", codexRunner.lastRequest.Prompt)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "custom-docs", "manifest.json"))
	if manifest.Artifacts["snapshot_spec_after"] != "snapshots/spec.after.json" || manifest.Artifacts["snapshot_tasks_after"] != "snapshots/tasks.after.json" || manifest.Artifacts["snapshot_eval"] != "" {
		t.Fatalf("unexpected custom artifact paths: %#v", manifest.Artifacts)
	}
}

func TestExecuteUsesJJRCConfiguration(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	writeJJRC(t, dir, `{
		"openai_api_key_env": "JJ_TEST_OPENAI_KEY",
		"openai_model": "file-openai",
		"codex_model": "file-codex",
		"codex_bin": "/tmp/file-codex",
		"planning_agents": 2
	}`)
	t.Setenv("JJ_TEST_OPENAI_KEY", "sk-test-value")
	t.Setenv("JJ_OPENAI_MODEL", "")
	t.Setenv("JJ_CODEX_MODEL", "")
	t.Setenv("JJ_CODEX_BIN", "")
	planner := &fakePlanner{}
	codexRunner := &fakeCodexRunner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:        filepath.Join(dir, "plan.md"),
		CWD:             dir,
		ConfigSearchDir: dir,
		RunID:           "jjrc-config",
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
		AllowNoGit:      true,
		Stdout:          io.Discard,
		Planner:         planner,
		CodexRunner:     codexRunner,
	})
	if err != nil {
		t.Fatalf("execute with .jjrc: %v", err)
	}
	if len(planner.draftIDs) != 2 {
		t.Fatalf("expected 2 planning agents from .jjrc, got %d", len(planner.draftIDs))
	}
	for _, model := range planner.models {
		if model != "file-openai" {
			t.Fatalf("expected file-openai model, got models %#v", planner.models)
		}
	}
	if !codexRunner.called || codexRunner.lastRequest.Model != "file-codex" || codexRunner.lastRequest.Bin != "/tmp/file-codex" {
		t.Fatalf("expected codex request from .jjrc, got %#v", codexRunner.lastRequest)
	}
	manifestPath := filepath.Join(dir, ".jj", "runs", "jjrc-config", "manifest.json")
	manifest := readManifest(t, manifestPath)
	if manifest.Config.ConfigFile != "" && manifest.Config.ConfigFile != filepath.Join(dir, ".jjrc") {
		t.Fatalf("unexpected manifest config file: %#v", manifest.Config)
	}
	if manifest.Config.PlanningAgents != 2 || manifest.Config.OpenAIModel != "file-openai" {
		t.Fatalf("unexpected manifest config: %#v", manifest.Config)
	}
	assertManifestDoesNotContain(t, manifestPath, "sk-test-value")
}

func TestExecuteDryRunWithoutOpenAIKeyUsesCodexPlanner(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("JJ_TEST_SECRET", "super-secret-value")
	plannerRunner := &scriptedCodexPlannerRunner{}
	implementationRunner := &fakeCodexRunner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:           filepath.Join(dir, "plan.md"),
		CWD:                dir,
		RunID:              "codex-planner-dry-run",
		PlanningAgents:     2,
		OpenAIModel:        "test-model",
		CodexModel:         "codex-test-model",
		AllowNoGit:         true,
		DryRun:             true,
		Stdout:             io.Discard,
		PlannerCodexRunner: plannerRunner,
		CodexRunner:        implementationRunner,
	})
	if err != nil {
		t.Fatalf("execute dry-run with codex planner: %v", err)
	}
	if implementationRunner.called {
		t.Fatal("implementation codex runner should not run in dry-run")
	}
	if got := plannerRunner.callCount(); got != 3 {
		t.Fatalf("expected 2 draft calls plus merge call, got %d", got)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run")
	manifestPath := filepath.Join(runDir, "manifest.json")
	manifest := readManifest(t, manifestPath)
	if manifest.Status != StatusPlanned || manifest.PlannerProvider != plannerProviderCodex || len(manifest.Errors) != 0 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Config.CodexModel != "codex-test-model" {
		t.Fatalf("expected codex model in manifest, got %#v", manifest.Config)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "planning", "product_spec.events.jsonl"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "planning", "merge.last-message.txt"))
	taskData := readFile(t, filepath.Join(runDir, "snapshots", "tasks.after.json"))
	if !strings.Contains(taskData, `"status": "queued"`) {
		t.Fatalf("codex planner dry-run task should use canonical JSON queue state:\n%s", taskData)
	}
	assertManifestDoesNotContain(t, manifestPath, "super-secret-value")
}

func TestExecuteDryRunWithoutOpenAIKeyUsesFakeCodexExecutable(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("JJ_OPENAI_API_KEY_ENV", "OPENAI_API_KEY")
	fakeCodex := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message)
      shift
      out="$1"
      ;;
  esac
  shift
done
stage=$(basename "$out" .last-message.txt)
case "$stage" in
  merge)
    printf '%s\n' '{"spec":"# SPEC\n\nCodex executable merged spec.","task":"# TASK\n\n1. Codex executable merged task.","notes":["merged by fake executable"]}' > "$out"
    ;;
  *)
    printf '%s\n' '{"agent":"product_spec","summary":"Codex executable draft.","spec_markdown":"# SPEC draft","task_markdown":"# TASK draft","risks":["risk"],"assumptions":["assumption"],"acceptance_criteria":["acceptance"],"test_plan":["go test ./..."]}' > "$out"
    ;;
esac
printf '{"type":"done","stage":"%s"}\n' "$stage"
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	_, err := Execute(context.Background(), Config{
		PlanPath:               filepath.Join(dir, "plan.md"),
		CWD:                    dir,
		RunID:                  "codex-executable-dry-run",
		PlanningAgents:         1,
		PlanningAgentsExplicit: true,
		OpenAIModel:            "test-model",
		CodexModel:             "codex-test-model",
		CodexBin:               fakeCodex,
		CodexBinExplicit:       true,
		AllowNoGit:             true,
		AllowNoGitExplicit:     true,
		DryRun:                 true,
		DryRunExplicit:         true,
		Stdout:                 io.Discard,
	})
	if err != nil {
		t.Fatalf("execute dry-run with fake codex executable: %v", err)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "codex-executable-dry-run")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.Status != StatusPlanned || manifest.PlannerProvider != plannerProviderCodex || manifest.Planner.Provider != plannerProviderCodex {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Planner.Model != "codex-test-model" {
		t.Fatalf("expected fake codex planner model in manifest, got planner=%#v config=%#v", manifest.Planner, manifest.Config)
	}
	if manifest.Config.CodexBin != "" && manifest.Config.CodexBin != fakeCodex {
		t.Fatalf("expected fake codex config in manifest, got planner=%#v config=%#v", manifest.Planner, manifest.Config)
	}
	assertFileExists(t, filepath.Join(runDir, "planning", "product_spec.events.jsonl"))
	assertFileExists(t, filepath.Join(runDir, "planning", "merge.last-message.txt"))
	taskData := readFile(t, filepath.Join(runDir, "snapshots", "tasks.after.json"))
	if !strings.Contains(taskData, `"status": "queued"`) {
		t.Fatalf("fake codex executable task should use canonical JSON state:\n%s", taskData)
	}
}

func TestExecuteFullRunWithoutOpenAIKeyUsesCodexPlanner(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")
	plannerRunner := &scriptedCodexPlannerRunner{}
	implementationRunner := &fakeCodexRunner{mutate: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:           filepath.Join(dir, "plan.md"),
		CWD:                dir,
		RunID:              "codex-planner-full",
		PlanningAgents:     1,
		OpenAIModel:        "test-model",
		CodexModel:         "codex-test-model",
		Stdout:             io.Discard,
		PlannerCodexRunner: plannerRunner,
		CodexRunner:        implementationRunner,
	})
	if err != nil {
		t.Fatalf("execute full run with codex planner: %v", err)
	}
	if !implementationRunner.called {
		t.Fatal("implementation codex runner should run in full-run")
	}
	if got := plannerRunner.callCount(); got != 3 {
		t.Fatalf("expected draft, merge, and spec reconciliation calls, got %d", got)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "manifest.json"))
	if manifest.Status != StatusCompleted || manifest.PlannerProvider != plannerProviderCodex || manifest.Validation.Status != validationStatusPassed {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "snapshots", "eval.json"))
	assertNoFile(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "planning", "eval.events.jsonl"))
	taskData := readFile(t, filepath.Join(dir, ".jj", "tasks.json"))
	if !strings.Contains(taskData, `"status": "done"`) {
		t.Fatalf("codex planner full-run task should use canonical JSON state:\n%s", taskData)
	}
}

func TestExecuteCodexPlannerInvalidJSONFailsManifest(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")

	_, err := Execute(context.Background(), Config{
		PlanPath:           filepath.Join(dir, "plan.md"),
		CWD:                dir,
		RunID:              "codex-planner-invalid-json",
		PlanningAgents:     1,
		OpenAIModel:        "test-model",
		AllowNoGit:         true,
		DryRun:             true,
		Stdout:             io.Discard,
		PlannerCodexRunner: &scriptedCodexPlannerRunner{invalidJSON: true},
	})
	if err == nil || !strings.Contains(err.Error(), "codex draft product_spec") {
		t.Fatalf("expected codex draft parse error, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "codex-planner-invalid-json", "manifest.json"))
	if manifest.Status != StatusPlanningFailed || manifest.PlannerProvider != plannerProviderCodex {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestExecuteDryRunWithInjectedPlannerSkipsImplementationCodex(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")
	implementationRunner := &fakeCodexRunner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "dry-run-injected",
		PlanningAgents: 1,
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    implementationRunner,
	})
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	if implementationRunner.called {
		t.Fatal("implementation codex runner should not run in dry-run")
	}
	assertNoFile(t, filepath.Join(dir, "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "TASK.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "TASK.md"))
}

func TestExecuteRedactsGitBaselineArtifact(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	t.Setenv("JJ_TEST_SECRET", "super-secret-value")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "redacted-git-baseline",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		AllowNoGit:     false,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		GitRunner: fakeGitRunner{
			outputs: map[string]string{
				"rev-parse --show-toplevel": dir,
				"rev-parse HEAD":            "abc123",
				"branch --show-current":     "feature/super-secret-value",
				"status --short":            "",
				"diff --stat":               "",
				"diff --name-status":        "",
				"diff --binary":             "",
			},
		},
	})
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	baseline := readFile(t, filepath.Join(dir, ".jj", "runs", "redacted-git-baseline", "git", "baseline.json"))
	if strings.Contains(baseline, "super-secret-value") || !strings.Contains(baseline, "[jj-omitted]") {
		t.Fatalf("git baseline should be redacted:\n%s", baseline)
	}
}

func TestExecuteRedactsPersistedArtifactsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	secret := "run-secret-token-1234567890"
	openAIKey := "sk-proj-redact1234567890"
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("JJ_RUN_TEST_TOKEN", secret)
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("Build a thing.\nAuthorization: Bearer "+secret+"\napi_key="+secret+"\n"+openAIKey+"\n"), 0o644); err != nil {
		t.Fatalf("write plan with secret: %v", err)
	}
	writeValidationScript(t, dir, "printf 'ok\\n'")
	writeJJRC(t, dir, `{"codex_model":"`+secret+`","codex_bin":"`+secret+`"}`)

	_, err := Execute(context.Background(), Config{
		PlanPath:               filepath.Join(dir, "plan.md"),
		CWD:                    dir,
		ConfigSearchDir:        dir,
		RunID:                  "redaction-e2e",
		PlanningAgents:         1,
		PlanningAgentsExplicit: true,
		OpenAIModel:            "test-model",
		AllowNoGit:             true,
		AllowNoGitExplicit:     true,
		DryRun:                 false,
		DryRunExplicit:         true,
		Stdout:                 io.Discard,
		Planner:                &fakePlanner{secret: secret},
		CodexRunner:            &fakeCodexRunner{secret: secret},
	})
	if err != nil {
		t.Fatalf("execute redaction run: %v", err)
	}
	runDir := filepath.Join(dir, ".jj", "runs", "redaction-e2e")
	assertTreeDoesNotContain(t, runDir, secret)
	assertTreeDoesNotContain(t, runDir, openAIKey)
	for _, rel := range []string{".jj/spec.json", ".jj/tasks.json"} {
		data := readFile(t, filepath.Join(dir, rel))
		if strings.Contains(data, secret) || strings.Contains(data, openAIKey) {
			t.Fatalf("%s contains raw secret:\n%s", rel, data)
		}
	}
	manifestData := readFile(t, filepath.Join(runDir, "manifest.json"))
	if !strings.Contains(manifestData, "[jj-omitted]") {
		t.Fatalf("manifest missing redaction marker:\n%s", manifestData)
	}
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if manifest.Config.CodexModel != "" || manifest.Config.CodexBin != "" {
		t.Fatalf("manifest safe config should omit secret-like codex values, got %#v", manifest.Config)
	}
	if manifest.Config.OpenAIKeySet || manifest.Config.OpenAIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("manifest safe config should retain key presence metadata, got %#v", manifest.Config)
	}
	if manifest.RedactionCount == 0 || manifest.Security.RedactionCount == 0 {
		t.Fatalf("manifest should report redaction counts, got top=%d security=%d\n%s", manifest.RedactionCount, manifest.Security.RedactionCount, manifestData)
	}
	if len(manifest.RedactionKinds) == 0 || len(manifest.Security.RedactionKinds) == 0 {
		t.Fatalf("manifest should report redaction kinds, got top=%#v security=%#v\n%s", manifest.RedactionKinds, manifest.Security.RedactionKinds, manifestData)
	}
	if strings.Contains(strings.Join(manifest.RedactionKinds, ","), secret) {
		t.Fatalf("redaction kinds leaked original secret: %#v", manifest.RedactionKinds)
	}
	input := readFile(t, filepath.Join(runDir, "input.md"))
	if !strings.Contains(input, "[jj-omitted]") {
		t.Fatalf("input.md should retain redacted evidence:\n%s", input)
	}
}

func TestBuildContinuationContextRejectsReportedRunDirEscape(t *testing.T) {
	cwd := t.TempDir()
	outside := t.TempDir()
	runDir := filepath.Join(cwd, ".jj", "runs", "safe-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), []byte(`{"status":"complete"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "manifest.json"), []byte(`{"error":"outside"}`), 0o644); err != nil {
		t.Fatalf("write outside manifest: %v", err)
	}

	if _, err := BuildContinuationContextFromRunDir(cwd, outside, "safe-run"); err == nil || !strings.Contains(err.Error(), "outside the expected run root") {
		t.Fatalf("expected outside reported run dir rejection, got %v", err)
	}
}

func TestExecuteRequiresGitUnlessAllowed(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "no-git",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
	})
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("expected git error, got %v", err)
	}
}

func TestExecuteAllowsPartialPlannerFailure(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{failAgents: map[string]error{"qa_validation": errors.New("qa failed")}}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "partial-planning",
		PlanningAgents: 3,
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        planner,
	})
	if err != nil {
		t.Fatalf("expected partial planner success, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "partial-planning", "manifest.json"))
	var failed bool
	for _, agent := range manifest.Planning.Agents {
		if agent.Name == "qa_validation" && agent.Status == "failed" {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("expected failed qa_validation agent in manifest: %#v", manifest.Planning.Agents)
	}
}

func TestExecuteFailsWhenAllPlannersFail(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{failAll: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "all-planners-fail",
		PlanningAgents: 2,
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        planner,
	})
	if err == nil || !strings.Contains(err.Error(), "all planning agents failed") {
		t.Fatalf("expected all planners failed error, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "all-planners-fail", "manifest.json"))
	if manifest.Status != StatusPlanningFailed {
		t.Fatalf("expected failed manifest, got %q", manifest.Status)
	}
	if !manifest.Codex.Skipped {
		t.Fatalf("expected codex to be marked skipped: codex=%#v", manifest.Codex)
	}
}

func TestExecuteRejectsIncompletePlannerDrafts(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{incompleteAgents: map[string]bool{"product_spec": true}}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "incomplete-planner",
		PlanningAgents: 1,
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        planner,
	})
	if err == nil || !strings.Contains(err.Error(), "all planning agents failed") {
		t.Fatalf("expected incomplete planner failure, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "incomplete-planner", "manifest.json"))
	if manifest.Status != StatusPlanningFailed || manifest.FailurePhase != StatusPlanning {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestExecuteRejectsEmptyOrWhitespaceMergedPlannerDocuments(t *testing.T) {
	cases := []struct {
		name        string
		runID       string
		makePlanner func() *fakePlanner
		wantErr     string
	}{
		{
			name:        "empty spec",
			runID:       "empty-spec",
			makePlanner: func() *fakePlanner { return &fakePlanner{emptySpec: true} },
			wantErr:     "merged SPEC content is empty",
		},
		{
			name:        "whitespace spec",
			runID:       "whitespace-spec",
			makePlanner: func() *fakePlanner { return &fakePlanner{whitespaceSpec: true} },
			wantErr:     "merged SPEC content is empty",
		},
		{
			name:        "empty task",
			runID:       "empty-task",
			makePlanner: func() *fakePlanner { return &fakePlanner{emptyTask: true} },
			wantErr:     "merged TASK content is empty",
		},
		{
			name:        "whitespace task",
			runID:       "whitespace-task",
			makePlanner: func() *fakePlanner { return &fakePlanner{whitespaceTask: true} },
			wantErr:     "merged TASK content is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writePlan(t, dir, "plan.md")
			if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
				t.Fatalf("mkdir docs: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "docs", "SPEC.md"), []byte("# existing spec\n"), 0o644); err != nil {
				t.Fatalf("write existing spec: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "docs", "TASK.md"), []byte("# existing task\n"), 0o644); err != nil {
				t.Fatalf("write existing task: %v", err)
			}
			codexRunner := &fakeCodexRunner{}

			_, err := Execute(context.Background(), Config{
				PlanPath:       filepath.Join(dir, "plan.md"),
				CWD:            dir,
				RunID:          tc.runID,
				PlanningAgents: 1,
				AllowNoGit:     true,
				Stdout:         io.Discard,
				Planner:        tc.makePlanner(),
				CodexRunner:    codexRunner,
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected merge validation failure %q, got %v", tc.wantErr, err)
			}
			if codexRunner.called {
				t.Fatal("implementation Codex runner should not run after planning validation failure")
			}
			if got := readFile(t, filepath.Join(dir, "docs", "SPEC.md")); got != "# existing spec\n" {
				t.Fatalf("workspace SPEC should not be mutated, got:\n%s", got)
			}
			if got := readFile(t, filepath.Join(dir, "docs", "TASK.md")); got != "# existing task\n" {
				t.Fatalf("workspace TASK should not be mutated, got:\n%s", got)
			}
			runDir := filepath.Join(dir, ".jj", "runs", tc.runID)
			assertNoFile(t, filepath.Join(runDir, "docs", "SPEC.md"))
			assertNoFile(t, filepath.Join(runDir, "docs", "TASK.md"))
			assertFileExists(t, filepath.Join(runDir, "planning", "merge.json"))
			assertFileExists(t, filepath.Join(runDir, "planning", "merged.json"))
			assertFileExists(t, filepath.Join(runDir, "planning", "raw_response_merge.txt"))
			assertFileExists(t, filepath.Join(runDir, "planning.json"))
			assertFileExists(t, filepath.Join(runDir, "planning", "planning.json"))
			manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
			if manifest.Status != StatusPlanningFailed || manifest.FailurePhase != StatusPlanning || manifest.FailedStage != StatusPlanning {
				t.Fatalf("unexpected manifest failure state: %#v", manifest)
			}
			if !strings.Contains(manifest.ErrorSummary, tc.wantErr) || !strings.Contains(manifest.FailureMessage, tc.wantErr) {
				t.Fatalf("manifest should record validation error %q, got summary=%q message=%q", tc.wantErr, manifest.ErrorSummary, manifest.FailureMessage)
			}
			if !manifest.Codex.Skipped || manifest.Codex.Ran || manifest.Codex.Status != "skipped" {
				t.Fatalf("codex should be skipped in manifest, got %#v", manifest.Codex)
			}
			if manifest.Workspace.SpecWritten || manifest.Workspace.TaskWritten {
				t.Fatalf("workspace writes should be false, got %#v", manifest.Workspace)
			}
			if manifest.Artifacts["spec"] != "" || manifest.Artifacts["task"] != "" {
				t.Fatalf("failed planning manifest should not list generated docs, got %#v", manifest.Artifacts)
			}
			if manifest.Artifacts["planning_merge"] != "planning/merge.json" || manifest.Artifacts["planning_merged"] != "planning/merged.json" || manifest.Artifacts["planning_merge_raw_response"] != "planning/raw_response_merge.txt" {
				t.Fatalf("manifest should preserve merge artifacts, got %#v", manifest.Artifacts)
			}
			if manifest.Artifacts["planning"] != "planning.json" || manifest.Artifacts["planning_normalized"] != "planning/planning.json" {
				t.Fatalf("manifest should preserve normalized planning artifacts, got %#v", manifest.Artifacts)
			}
		})
	}
}

func TestExecuteReportsCodexFailure(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "codex-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{err: errors.New("boom")},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("codex failure should return sanitized error after validation, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "codex-fail", "manifest.json"))
	if manifest.Status != StatusImplementationFailed {
		t.Fatalf("expected implementation failed status, got %q", manifest.Status)
	}
	if manifest.Codex.Error == "" || !strings.Contains(manifest.Codex.Error, "boom") {
		t.Fatalf("expected codex error in manifest, got %#v", manifest.Codex)
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "runs", "codex-fail", "snapshots", "eval.json"))
}

func TestExecuteValidationFailureCapturesFinalGitDiff(t *testing.T) {
	dir := initGit(t)
	runGit(t, dir, "config", "user.email", "jj@example.com")
	runGit(t, dir, "config", "user.name", "jj test")
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jj/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package tracked\n"), 0o644); err != nil {
		t.Fatalf("write tracked: %v", err)
	}
	writeValidationScript(t, dir, `exit 9`)
	runGit(t, dir, "add", "--all")
	runGit(t, dir, "commit", "-m", "initial")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "validation-fail-final-diff",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("validation failure should complete with partial status, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "validation-fail-final-diff", "manifest.json"))
	if manifest.Status != StatusPartial || manifest.Validation.Status != validationStatusFailed || manifest.Git.DiffPath != "git/diff.patch" || manifest.Git.StatusAfterPath != "git/status.after.txt" {
		t.Fatalf("expected partial manifest with final git evidence, got %#v", manifest)
	}
	assertNoFile(t, filepath.Join(dir, ".jj", "runs", "validation-fail-final-diff", "snapshots", "eval.json"))
}

func TestExecuteGitDiffArtifactsPersistRedactedEvidenceInDryRunAndFullRun(t *testing.T) {
	secret := "diff-redaction-secret-value"
	openAIKey := "sk-proj-diffredaction1234567890"
	privateKeyBody := "diff-redaction-private-key-body"
	tokenLike := "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890QwErTy"
	t.Setenv("JJ_DIFF_REDACTION_TOKEN", secret)

	type diffEvidence struct {
		count  int
		kinds  map[string]int
		labels []string
	}
	var baseline *diffEvidence
	for _, tc := range []struct {
		name   string
		dryRun bool
	}{
		{name: "dry-run", dryRun: true},
		{name: "full-run", dryRun: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writePlan(t, dir, "plan.md")
			if !tc.dryRun {
				writeValidationScript(t, dir, "printf 'ok\\n'")
			}
			unsafeAbs := filepath.Join(t.TempDir(), "unsafe", "secret.txt")
			outputs := hostileGitDiffOutputs(dir, unsafeAbs, secret, openAIKey, privateKeyBody, tokenLike)

			result, err := Execute(context.Background(), Config{
				PlanPath:       filepath.Join(dir, "plan.md"),
				CWD:            dir,
				RunID:          "diff-redaction-" + strings.ReplaceAll(tc.name, "-", ""),
				PlanningAgents: 1,
				OpenAIModel:    "test-model",
				DryRun:         tc.dryRun,
				DryRunExplicit: true,
				Stdout:         io.Discard,
				Stderr:         io.Discard,
				Planner:        &fakePlanner{},
				CodexRunner:    &fakeCodexRunner{},
				GitRunner:      fakeGitRunner{outputs: outputs},
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}

			for _, rel := range []string{
				"git/diff.patch",
				"git/diff-summary.txt",
				"git/diff.stat.txt",
				"git/status.txt",
				"git/status.after.txt",
			} {
				body := readFile(t, filepath.Join(result.RunDir, filepath.FromSlash(rel)))
				for _, leaked := range []string{
					secret,
					openAIKey,
					privateKeyBody,
					tokenLike,
					"-----BEGIN PRIVATE KEY-----",
					"-----END PRIVATE KEY-----",
					unsafeAbs,
					filepath.ToSlash(unsafeAbs),
					"[omitted]",
					"../denied/" + secret,
				} {
					if strings.Contains(body, leaked) {
						t.Fatalf("%s leaked %q:\n%s", rel, leaked, body)
					}
				}
			}
			diffBody := readFile(t, filepath.Join(result.RunDir, "git", "diff.patch"))
			if !strings.Contains(diffBody, security.RedactionMarker) || !strings.Contains(diffBody, "[path]") {
				t.Fatalf("diff artifact should retain sanitized redaction evidence:\n%s", diffBody)
			}

			manifest := readManifest(t, filepath.Join(result.RunDir, "manifest.json"))
			if manifest.Git.DiffPath != "git/diff.patch" ||
				manifest.Git.DiffSummaryPath != "git/diff-summary.txt" ||
				manifest.Git.DiffStatPath != "git/diff.stat.txt" ||
				!manifest.Git.DiffRedactionApplied ||
				manifest.Git.DiffRedactionCount == 0 {
				t.Fatalf("manifest missing git diff redaction evidence: %#v", manifest.Git)
			}
			diag := manifest.Security.Diagnostics
			if !diag.GitDiffArtifactsAvailable ||
				!diag.GitDiffRedactionApplied ||
				diag.GitDiffRedactionCount != manifest.Git.DiffRedactionCount ||
				!reflect.DeepEqual(diag.GitDiffRedactionCategoryCounts, manifest.Git.DiffRedactionCategoryCounts) ||
				!reflect.DeepEqual(diag.GitDiffArtifactLabels, manifest.Git.DiffArtifactLabels) {
				t.Fatalf("security diagnostics did not mirror sanitized diff evidence:\ngit=%#v\ndiag=%#v", manifest.Git, diag)
			}
			for _, want := range []string{"absolute_path", "private_key", "openai_key", "token_like"} {
				if diag.GitDiffRedactionCategoryCounts[want] == 0 {
					t.Fatalf("diff redaction categories missing %q: %#v", want, diag.GitDiffRedactionCategoryCounts)
				}
			}
			got := diffEvidence{
				count:  manifest.Git.DiffRedactionCount,
				kinds:  manifest.Git.DiffRedactionCategoryCounts,
				labels: manifest.Git.DiffArtifactLabels,
			}
			if baseline == nil {
				baseline = &got
			} else if baseline.count != got.count || !reflect.DeepEqual(baseline.kinds, got.kinds) || !reflect.DeepEqual(baseline.labels, got.labels) {
				t.Fatalf("dry-run/full-run diff evidence diverged:\nbase=%#v\ngot=%#v", baseline, got)
			}
		})
	}
}

type fakePlanner struct {
	mu                sync.Mutex
	draftIDs          []string
	models            []string
	secret            string
	failAgents        map[string]error
	incompleteAgents  map[string]bool
	failAll           bool
	emptyMerge        bool
	emptySpec         bool
	whitespaceSpec    bool
	emptyTask         bool
	whitespaceTask    bool
	mergeTask         string
	draftRequests     []ai.DraftRequest
	lastMergeRequest  ai.MergeRequest
	reconcileRequests []ai.ReconcileSpecRequest
	reconcileSpec     string
	reconcileErr      error
}

func (f *fakePlanner) Draft(_ context.Context, req ai.DraftRequest) (ai.PlanningDraft, []byte, error) {
	f.mu.Lock()
	f.draftIDs = append(f.draftIDs, req.Agent.Name)
	f.models = append(f.models, req.Model)
	f.draftRequests = append(f.draftRequests, req)
	f.mu.Unlock()
	if f.failAll {
		return ai.PlanningDraft{}, []byte("not-json"), errors.New("planner failed")
	}
	if err := f.failAgents[req.Agent.Name]; err != nil {
		return ai.PlanningDraft{}, []byte("not-json"), err
	}
	if f.incompleteAgents[req.Agent.Name] {
		draft := ai.PlanningDraft{Agent: req.Agent.Name}
		return draft, mustJSON(draft), nil
	}
	secretSuffix := ""
	if f.secret != "" {
		secretSuffix = " " + f.secret
	}
	draft := ai.PlanningDraft{
		Agent:              req.Agent.Name,
		Summary:            "summary" + secretSuffix,
		SpecMarkdown:       "# Spec from " + req.Agent.Name + secretSuffix,
		TaskMarkdown:       "# Task from " + req.Agent.Name + secretSuffix,
		SpecDraft:          "# Spec from " + req.Agent.Name + secretSuffix,
		TaskDraft:          "# Task from " + req.Agent.Name + secretSuffix,
		Risks:              []string{"risk" + secretSuffix},
		Assumptions:        []string{"assumption"},
		AcceptanceCriteria: []string{"acceptance"},
		TestPlan:           []string{"go test ./..."},
		TestingGuidance:    []string{"go test ./..."},
	}
	return draft, mustJSON(tidyDraft(draft)), nil
}

func (f *fakePlanner) Merge(_ context.Context, req ai.MergeRequest) (ai.MergeResult, []byte, error) {
	f.mu.Lock()
	f.models = append(f.models, req.Model)
	f.lastMergeRequest = req
	f.mu.Unlock()
	if f.emptyMerge {
		merged := ai.MergeResult{}
		return merged, mustJSON(merged), nil
	}
	secretSuffix := ""
	if f.secret != "" {
		secretSuffix = " " + f.secret
	}
	merged := ai.MergeResult{
		Spec:  "# SPEC\n\nImplement the requested behavior." + secretSuffix + "\n",
		Task:  "# TASK\n\n1. Implement it" + secretSuffix + ".\n2. Run tests.\n",
		Notes: []string{"merged" + secretSuffix},
	}
	if f.emptySpec {
		merged.Spec = ""
	}
	if f.whitespaceSpec {
		merged.Spec = " \n\t"
	}
	if f.emptyTask {
		merged.Task = ""
	}
	if f.whitespaceTask {
		merged.Task = " \n\t"
	}
	if strings.TrimSpace(f.mergeTask) != "" {
		merged.Task = f.mergeTask
	}
	return merged, mustJSON(merged), nil
}

func (f *fakePlanner) ReconcileSpec(_ context.Context, req ai.ReconcileSpecRequest) (ai.ReconcileSpecResult, []byte, error) {
	f.mu.Lock()
	f.models = append(f.models, req.Model)
	f.reconcileRequests = append(f.reconcileRequests, req)
	f.mu.Unlock()
	if f.reconcileErr != nil {
		return ai.ReconcileSpecResult{}, []byte("not-json"), f.reconcileErr
	}
	spec := strings.TrimSpace(req.PlannedSpec)
	if strings.TrimSpace(f.reconcileSpec) != "" {
		spec = f.reconcileSpec
	}
	if spec == "" || spec == "{}" {
		spec = req.PreviousSpec
	}
	result := ai.ReconcileSpecResult{
		Spec:  spec,
		Notes: []string{"reconciled by fake planner"},
	}
	return result, mustJSON(result), nil
}

type fakeCodexRunner struct {
	called      bool
	mutate      bool
	err         error
	secret      string
	files       map[string][]byte
	symlinks    map[string]string
	lastRequest codex.Request
	beforeRun   func(codex.Request) error
}

func (f *fakeCodexRunner) Run(_ context.Context, req codex.Request) (codex.Result, error) {
	f.called = true
	f.lastRequest = req
	if f.beforeRun != nil {
		if err := f.beforeRun(req); err != nil {
			return codex.Result{}, err
		}
	}
	event := "{\"type\":\"done\"}\n"
	summary := "Changed files: fake.go\nTests: fake pass\n"
	if f.secret != "" {
		event = "{\"type\":\"done\",\"token\":\"" + f.secret + "\"}\n"
		summary += "Authorization: Bearer " + f.secret + "\n"
	}
	if err := os.WriteFile(req.EventsPath, []byte(event), 0o644); err != nil {
		return codex.Result{}, err
	}
	if err := os.WriteFile(req.OutputLastMessage, []byte(summary), 0o644); err != nil {
		return codex.Result{}, err
	}
	if f.mutate {
		if err := os.WriteFile(filepath.Join(req.CWD, "fake.go"), []byte("package fake\n"), 0o644); err != nil {
			return codex.Result{}, err
		}
	}
	for rel, data := range f.files {
		path := filepath.Join(req.CWD, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return codex.Result{}, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return codex.Result{}, err
		}
	}
	for rel, target := range f.symlinks {
		path := filepath.Join(req.CWD, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return codex.Result{}, err
		}
		if err := os.Symlink(target, path); err != nil {
			return codex.Result{}, err
		}
	}
	if f.err != nil {
		return codex.Result{Summary: "failed summary", ExitCode: 1, DurationMS: 12}, f.err
	}
	return codex.Result{Summary: summary, ExitCode: 0, DurationMS: 12}, nil
}

type scriptedCodexPlannerRunner struct {
	mu          sync.Mutex
	calls       []codex.Request
	invalidJSON bool
	err         error
}

type fakeGitRunner struct {
	outputs map[string]string
}

func (f fakeGitRunner) Output(_ context.Context, _ string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	value, ok := f.outputs[key]
	if !ok {
		if key == "ls-files --others --exclude-standard -z" {
			return "", nil
		}
		return "", errors.New("unexpected git command: " + key)
	}
	if key == "ls-files --others --exclude-standard -z" {
		return value, nil
	}
	return value + "\n", nil
}

func hostileGitDiffOutputs(root, unsafeAbs, secret, openAIKey, privateKeyBody, tokenLike string) map[string]string {
	privateKey := "-----BEGIN PRIVATE KEY-----\n" + privateKeyBody + "\n-----END PRIVATE KEY-----"
	full := fmt.Sprintf(`diff --git a/config.txt b/config.txt
--- a/config.txt
+++ b/config.txt
@@ -1 +1,10 @@
+api_key=%s
+Authorization: Bearer %s
+%s
+%s
+path=%s
+command=./scripts/deploy --token %s
+opaque=%s
+placeholder=[omitted]
+secret_path=../denied/%s
`, secret, openAIKey, openAIKey, privateKey, unsafeAbs, secret, tokenLike, secret)
	return map[string]string{
		"rev-parse --show-toplevel": root,
		"rev-parse HEAD":            "abc123",
		"branch --show-current":     "main",
		"status --short":            "M config.txt",
		"diff --stat":               " " + unsafeAbs + " | 10 ++++++++++",
		"diff --name-status":        "M\t" + unsafeAbs,
		"diff --binary":             full,
	}
}

func (s *scriptedCodexPlannerRunner) Run(_ context.Context, req codex.Request) (codex.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(req.EventsPath), 0o755); err != nil {
		return codex.Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(req.OutputLastMessage), 0o755); err != nil {
		return codex.Result{}, err
	}
	if err := os.WriteFile(req.EventsPath, []byte("{\"type\":\"done\"}\n"), 0o644); err != nil {
		return codex.Result{}, err
	}

	summary := s.summaryFor(req)
	if s.invalidJSON {
		summary = "not-json"
	}
	if err := os.WriteFile(req.OutputLastMessage, []byte(summary), 0o644); err != nil {
		return codex.Result{}, err
	}
	if s.err != nil {
		return codex.Result{Summary: summary, ExitCode: 1, DurationMS: 3}, s.err
	}
	return codex.Result{Summary: summary, ExitCode: 0, DurationMS: 3}, nil
}

func (s *scriptedCodexPlannerRunner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *scriptedCodexPlannerRunner) summaryFor(req codex.Request) string {
	stage := strings.TrimSuffix(filepath.Base(req.OutputLastMessage), ".last-message.txt")
	switch stage {
	case "merge":
		return string(mustJSON(ai.MergeResult{
			Spec:  "# SPEC\n\nCodex merged spec.\n",
			Task:  "# TASK\n\n1. Codex merged task.\n",
			Notes: []string{"merged by codex planner"},
		}))
	case "spec-reconcile":
		return string(mustJSON(ai.ReconcileSpecResult{
			Spec:  `{"version":1,"title":"Codex reconciled spec","summary":"Codex reconciled summary.","goals":["goal"],"non_goals":[],"requirements":["requirement"],"acceptance_criteria":["acceptance"],"open_questions":[],"created_at":"","updated_at":""}`,
			Notes: []string{"reconciled by codex planner"},
		}))
	default:
		return string(mustJSON(ai.PlanningDraft{
			Agent:              stage,
			Summary:            "Codex fallback draft.",
			SpecMarkdown:       "# Spec from " + stage,
			TaskMarkdown:       "# Task from " + stage,
			Risks:              []string{"risk"},
			Assumptions:        []string{"assumption"},
			AcceptanceCriteria: []string{"acceptance"},
			TestPlan:           []string{"go test ./..."},
		}))
	}
}

func writePlan(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("Build a thing.\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
}

func writeNextIntent(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, DefaultNextIntentPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir next intent dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write next intent: %v", err)
	}
}

func plannedTaskJSON(title string) string {
	return fmt.Sprintf(`{
		"version": 1,
		"tasks": [{
			"title": %q,
			"mode": "feature",
			"priority": "high",
			"status": "queued",
			"reason": "test proposal",
			"acceptance_criteria": ["works"],
			"validation_command": "./scripts/validate.sh"
		}]
	}`, title)
}

func writeValidationScript(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "scripts", "validate.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	content := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write validate.sh: %v", err)
	}
}

func prepareCommittedWorkspace(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.email", "jj@example.com")
	runGit(t, dir, "config", "user.name", "jj test")
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jj/\nignored.txt\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGit(t, dir, "add", "--all")
	runGit(t, dir, "commit", "-m", "initial")
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s: %v", path, err)
	}
}

func assertNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no file %s, got err %v", path, err)
	}
}

func readManifest(t *testing.T, path string) Manifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readPlanning(t *testing.T, path string) normalizedPlanningResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read planning: %v", err)
	}
	var planning normalizedPlanningResult
	if err := json.Unmarshal(data, &planning); err != nil {
		t.Fatalf("decode planning: %v", err)
	}
	return planning
}

func readTaskState(t *testing.T, path string) TaskState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task state: %v", err)
	}
	var state TaskState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode task state: %v", err)
	}
	return state
}

func assertManifestDoesNotContain(t *testing.T, path, needle string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(data), needle) {
		t.Fatalf("manifest should not contain %q", needle)
	}
}

func assertTreeDoesNotContain(t *testing.T, root, needle string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), needle) {
			t.Fatalf("%s should not contain %q:\n%s", path, needle, data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
}

func mustJSON(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func tidyDraft(d ai.PlanningDraft) ai.PlanningDraft {
	return d
}
