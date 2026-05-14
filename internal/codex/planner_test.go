package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jungju/jj/internal/artifact"
	ai "github.com/jungju/jj/internal/openai"
)

func TestPlannerDraftUsesCodexRunnerAndParsesJSON(t *testing.T) {
	store := newPlannerTestStore(t)
	runner := &fakePlannerExecutor{
		summary: `{
			"agent": "product_spec",
			"summary": "summary",
			"spec_markdown": "# SPEC",
			"task_markdown": "# TASK",
			"risks": [],
			"assumptions": [],
			"acceptance_criteria": ["works"],
			"test_plan": ["go test ./..."]
		}`,
	}
	var records []string
	planner := Planner{
		CWD:        store.CWD,
		Bin:        "/tmp/codex",
		Model:      "codex-model",
		AllowNoGit: true,
		Store:      store,
		Runner:     runner,
		Record: func(_ string, path string) {
			records = append(records, path)
		},
	}

	draft, raw, err := planner.Draft(context.Background(), ai.DraftRequest{
		Agent: ai.Agent{Name: "product_spec", Focus: "product"},
		Plan:  "build jj",
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if draft.Agent != "product_spec" || !strings.Contains(string(raw), "spec_markdown") {
		t.Fatalf("unexpected draft/raw: %#v %s", draft, raw)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one codex request, got %d", len(runner.requests))
	}
	req := runner.requests[0]
	if req.Bin != "/tmp/codex" || req.Model != "codex-model" || req.CWD != store.CWD || !req.AllowNoGit {
		t.Fatalf("unexpected request: %#v", req)
	}
	if !strings.HasSuffix(req.EventsPath, "planning/product_spec.events.jsonl") ||
		!strings.HasSuffix(req.OutputLastMessage, "planning/product_spec.last-message.txt") {
		t.Fatalf("unexpected artifact paths: %#v", req)
	}
	if len(records) != 2 {
		t.Fatalf("expected event and last-message records, got %#v", records)
	}
}

func TestPlannerInvalidJSONReturnsStageError(t *testing.T) {
	store := newPlannerTestStore(t)
	planner := Planner{
		CWD:    store.CWD,
		Store:  store,
		Runner: &fakePlannerExecutor{summary: "not-json"},
	}

	_, raw, err := planner.Draft(context.Background(), ai.DraftRequest{
		Agent: ai.Agent{Name: "product_spec"},
	})
	if err == nil || !strings.Contains(err.Error(), "codex draft product_spec") {
		t.Fatalf("expected stage parse error, got %v", err)
	}
	if string(raw) != "not-json" {
		t.Fatalf("expected raw invalid summary, got %q", raw)
	}
}

func TestMergePromptRequestsCanonicalTaskQueue(t *testing.T) {
	prompt := mergePrompt(ai.MergeRequest{
		Plan: "Make generated docs canonical.",
		Drafts: []ai.PlanningDraft{{
			Agent:        "product_spec",
			Summary:      "summary",
			SpecMarkdown: "# SPEC",
			TaskMarkdown: "# TASK",
		}},
	})
	for _, want := range []string{
		`.jj/spec.json`,
		`.jj/tasks.json`,
		`\"version\":1`,
		`\"active_task_id\":null`,
		`\"mode\":\"feature\"`,
		`\"status\":\"queued\"`,
		`\"validation_command\":\"./scripts/validate.sh\"`,
		"append-only proposal input, not a full replacement for .jj/tasks.json",
		"Do not include existing tasks from context.",
		"jj will assign fresh task IDs, append every proposed task",
		"current .jj/spec.json state is present in the planning context, it is the source of truth",
		"docs/PLAN.md is product vision/background only",
		"Supported statuses are queued, active, in_progress, done, blocked, failed, skipped, and superseded.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("merge prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "TASK.md must include:") || strings.Contains(prompt, "# jj TASK Queue") || strings.Contains(prompt, "## Implementation Steps") {
		t.Fatalf("merge prompt should not request legacy task sections:\n%s", prompt)
	}
}

func TestReconcileSpecPromptRequestsResultBasedSchema(t *testing.T) {
	prompt := reconcileSpecPrompt(ai.ReconcileSpecRequest{
		PreviousSpec:      `{"version":1,"title":"Before"}`,
		PlannedSpec:       `{"version":1,"title":"Planned"}`,
		SelectedTask:      `{"id":"TASK-0001"}`,
		CodexSummary:      "Changed code.",
		GitDiffSummary:    "diff summary",
		ValidationSummary: "Validation status: passed",
	})

	for _, want := range []string{
		`"spec": "{\"version\":1`,
		"Preserve the existing .jj/spec.json schema; do not add top-level fields.",
		"The previous SPEC is the source of truth.",
		"Incorporate only behavior supported by the selected task, Codex summary, git diff summary, and passed validation evidence.",
		"Validation summary:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reconcile prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlannerPromptsSanitizeHandoffPayloads(t *testing.T) {
	secret := "codex-planner-secret-token-1234567890"
	t.Setenv("JJ_CODEX_PLANNER_TOKEN", secret)
	hostile := strings.Join([]string{
		"Keep safe planning context.",
		"command=./scripts/deploy --token " + secret,
		"PATH=/tmp/codex-planner-secret",
		"manifest={\"run_id\":\"attack\",\"token\":\"" + secret + "\"}",
		"validation_output=panic at /tmp/codex-validation",
		"diff --git a/config.txt b/config.txt",
		"+api_key=" + secret,
		"denied_path=../../" + secret,
	}, "\n")

	prompts := []string{
		draftPrompt(ai.DraftRequest{Plan: hostile, Agent: ai.Agent{Name: "product_spec", Focus: "focus"}}),
		mergePrompt(ai.MergeRequest{
			Plan: hostile,
			Drafts: []ai.PlanningDraft{{
				Agent:              "product_spec",
				Summary:            "command=./scripts/deploy --token " + secret,
				SpecMarkdown:       hostile,
				TaskMarkdown:       hostile,
				AcceptanceCriteria: []string{"validation_output=panic " + secret},
				TestPlan:           []string{"PATH=/tmp/raw-env"},
			}},
		}),
		reconcileSpecPrompt(ai.ReconcileSpecRequest{
			PreviousSpec:      `{"version":1,"summary":"command=./scripts/deploy --token ` + secret + `"}`,
			PlannedSpec:       `{"version":1,"summary":"PATH=/tmp/codex-planned"}`,
			SelectedTask:      `{"id":"TASK-0001","reason":"validation_output=panic ` + secret + `"}`,
			CodexSummary:      hostile,
			GitDiffSummary:    hostile,
			ValidationSummary: hostile,
		}),
	}
	for _, prompt := range prompts {
		for _, leaked := range []string{secret, "./scripts/deploy --token", "/tmp/codex", "/tmp/raw-env", "diff --git", "+api_key=", "../../"} {
			if strings.Contains(prompt, leaked) {
				t.Fatalf("prompt leaked %q:\n%s", leaked, prompt)
			}
		}
		if !strings.Contains(prompt, "Keep safe planning context.") || !strings.Contains(prompt, "[jj-omitted]") {
			t.Fatalf("prompt lost safe context or redaction evidence:\n%s", prompt)
		}
	}
}

type fakePlannerExecutor struct {
	summary  string
	requests []Request
}

func (f *fakePlannerExecutor) Run(_ context.Context, req Request) (Result, error) {
	f.requests = append(f.requests, req)
	if err := os.MkdirAll(filepath.Dir(req.EventsPath), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(req.OutputLastMessage), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(req.EventsPath, []byte("{\"type\":\"done\"}\n"), 0o644); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(req.OutputLastMessage, []byte(f.summary), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Summary: f.summary, ExitCode: 0, DurationMS: 1}, nil
}

func newPlannerTestStore(t *testing.T) artifact.Store {
	t.Helper()
	store, err := artifact.NewStore(t.TempDir(), "planner-test")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return store
}
