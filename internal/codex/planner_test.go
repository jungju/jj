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
