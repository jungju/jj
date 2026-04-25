package run

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jungju/jj/internal/codex"
	ai "github.com/jungju/jj/internal/openai"
)

func TestExecuteRejectsInvalidPlanningAgents(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
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
		PlanPath:       "plan.md",
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

func TestExecuteDryRunCreatesPlanningArtifactsOnly(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{}
	codexRunner := &fakeCodexRunner{}

	result, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
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
	if planner.evalCalls != 0 {
		t.Fatal("evaluation should not run in dry-run")
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "SPEC.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "TASK.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning", "product_spec.json"))
	assertNoFile(t, filepath.Join(dir, "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "TASK.md"))
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "dry-run", "manifest.json"))
	if manifest.Status != "success" || !manifest.DryRun {
		t.Fatalf("unexpected dry-run manifest: %#v", manifest)
	}
}

func TestExecuteEndToEndWithFakes(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{}
	codexRunner := &fakeCodexRunner{mutate: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
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
	if planner.evalCalls != 1 {
		t.Fatalf("expected one eval call, got %d", planner.evalCalls)
	}
	assertFileExists(t, filepath.Join(dir, "SPEC.md"))
	assertFileExists(t, filepath.Join(dir, "TASK.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "EVAL.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "codex-events.jsonl"))
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "full", "manifest.json"))
	if manifest.SchemaVersion != "1" || manifest.Status != "success" || manifest.Evaluation.Result != "PASS" || manifest.Evaluation.Score != 90 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Artifacts["spec"] != "SPEC.md" || manifest.Artifacts["task"] != "TASK.md" {
		t.Fatalf("unexpected artifact paths: %#v", manifest.Artifacts)
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
		PlanPath:        "plan.md",
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
	if manifest.Config.ConfigFile != filepath.Join(dir, ".jjrc") || manifest.Config.PlanningAgents != 2 || manifest.Config.OpenAIModel != "file-openai" {
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
		PlanPath:           "plan.md",
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
	manifestPath := filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "manifest.json")
	manifest := readManifest(t, manifestPath)
	if manifest.Status != "success" || manifest.PlannerProvider != plannerProviderCodex || len(manifest.Errors) != 0 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Config.CodexModel != "codex-test-model" {
		t.Fatalf("expected codex model in manifest, got %#v", manifest.Config)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "planning", "product_spec.events.jsonl"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "planning", "merge.last-message.txt"))
	assertManifestDoesNotContain(t, manifestPath, "super-secret-value")
}

func TestExecuteFullRunWithoutOpenAIKeyUsesCodexPlannerAndEvaluation(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")
	plannerRunner := &scriptedCodexPlannerRunner{}
	implementationRunner := &fakeCodexRunner{mutate: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:           "plan.md",
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
		t.Fatalf("expected draft, merge, eval calls, got %d", got)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "manifest.json"))
	if manifest.Status != "success" || manifest.PlannerProvider != plannerProviderCodex || manifest.Evaluation.Result != "PASS" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "EVAL.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "planning", "eval.events.jsonl"))
}

func TestExecuteCodexPlannerInvalidJSONFailsManifest(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")

	_, err := Execute(context.Background(), Config{
		PlanPath:           "plan.md",
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
	if manifest.Status != "failed" || manifest.PlannerProvider != plannerProviderCodex {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestExecuteDryRunWithInjectedPlannerSkipsImplementationCodex(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	t.Setenv("OPENAI_API_KEY", "")
	implementationRunner := &fakeCodexRunner{}

	_, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
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
}

func TestExecuteRequiresGitUnlessAllowed(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
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
	planner := &fakePlanner{failAgents: map[string]error{"qa_evaluation": errors.New("qa failed")}}

	_, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
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
		if agent.Name == "qa_evaluation" && agent.Status == "failed" {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("expected failed qa_evaluation agent in manifest: %#v", manifest.Planning.Agents)
	}
}

func TestExecuteFailsWhenAllPlannersFail(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{failAll: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
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
	if manifest.Status != "failed" {
		t.Fatalf("expected failed manifest, got %q", manifest.Status)
	}
}

func TestExecuteReportsCodexFailure(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       "plan.md",
		CWD:            dir,
		RunID:          "codex-fail",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    &fakeCodexRunner{err: errors.New("boom")},
	})
	if err != nil {
		t.Fatalf("codex failure should continue to evaluation, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "codex-fail", "manifest.json"))
	if manifest.Status != "partial" {
		t.Fatalf("expected partial status, got %q", manifest.Status)
	}
	if manifest.Codex.Error == "" || !strings.Contains(manifest.Codex.Error, "boom") {
		t.Fatalf("expected codex error in manifest, got %#v", manifest.Codex)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-fail", "EVAL.md"))
}

type fakePlanner struct {
	mu         sync.Mutex
	draftIDs   []string
	models     []string
	evalCalls  int
	failAgents map[string]error
	failAll    bool
}

func (f *fakePlanner) Draft(_ context.Context, req ai.DraftRequest) (ai.PlanningDraft, []byte, error) {
	f.mu.Lock()
	f.draftIDs = append(f.draftIDs, req.Agent.Name)
	f.models = append(f.models, req.Model)
	f.mu.Unlock()
	if f.failAll {
		return ai.PlanningDraft{}, []byte("not-json"), errors.New("planner failed")
	}
	if err := f.failAgents[req.Agent.Name]; err != nil {
		return ai.PlanningDraft{}, []byte("not-json"), err
	}
	draft := ai.PlanningDraft{
		Agent:              req.Agent.Name,
		Summary:            "summary",
		SpecMarkdown:       "# Spec from " + req.Agent.Name,
		TaskMarkdown:       "# Task from " + req.Agent.Name,
		SpecDraft:          "# Spec from " + req.Agent.Name,
		TaskDraft:          "# Task from " + req.Agent.Name,
		Risks:              []string{"risk"},
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
	f.mu.Unlock()
	merged := ai.MergeResult{
		Spec:  "# SPEC\n\nImplement the requested behavior.\n",
		Task:  "# TASK\n\n1. Implement it.\n2. Run tests.\n",
		Notes: []string{"merged"},
	}
	return merged, mustJSON(merged), nil
}

func (f *fakePlanner) Evaluate(_ context.Context, req ai.EvaluationRequest) (ai.EvaluationResult, []byte, error) {
	f.mu.Lock()
	f.evalCalls++
	f.models = append(f.models, req.Model)
	f.mu.Unlock()
	eval := ai.EvaluationResult{
		Result:               "PASS",
		Score:                90,
		Summary:              "Looks good.",
		WhatChanged:          []string{"fake.go changed"},
		RequirementsCoverage: []string{"Spec and task were used."},
		TestCoverage:         []string{"fake tests passed"},
		Risks:                []string{},
		Regressions:          []string{},
		RecommendedFollowups: []string{},
	}
	return eval, mustJSON(eval), nil
}

type fakeCodexRunner struct {
	called      bool
	mutate      bool
	err         error
	lastRequest codex.Request
}

func (f *fakeCodexRunner) Run(_ context.Context, req codex.Request) (codex.Result, error) {
	f.called = true
	f.lastRequest = req
	if err := os.WriteFile(req.EventsPath, []byte("{\"type\":\"done\"}\n"), 0o644); err != nil {
		return codex.Result{}, err
	}
	if err := os.WriteFile(req.OutputLastMessage, []byte("Changed files: fake.go\nTests: fake pass\n"), 0o644); err != nil {
		return codex.Result{}, err
	}
	if f.mutate {
		if err := os.WriteFile(filepath.Join(req.CWD, "fake.go"), []byte("package fake\n"), 0o644); err != nil {
			return codex.Result{}, err
		}
	}
	if f.err != nil {
		return codex.Result{Summary: "failed summary", ExitCode: 1, DurationMS: 12}, f.err
	}
	return codex.Result{Summary: "Changed files: fake.go\nTests: fake pass\n", ExitCode: 0, DurationMS: 12}, nil
}

type scriptedCodexPlannerRunner struct {
	mu          sync.Mutex
	calls       []codex.Request
	invalidJSON bool
	err         error
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
	case "eval":
		return string(mustJSON(ai.EvaluationResult{
			Result:               "PASS",
			Score:                91,
			Summary:              "Codex fallback evaluation passed.",
			WhatChanged:          []string{"fake.go changed"},
			RequirementsCoverage: []string{"requirements covered"},
			TestCoverage:         []string{"tests summarized"},
			Risks:                []string{},
			Regressions:          []string{},
			RecommendedFollowups: []string{},
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
