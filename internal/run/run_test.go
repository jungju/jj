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

func TestExecuteRejectsInvalidDocumentName(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "invalid-doc",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		SpecDoc:        "../SPEC.md",
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
	})
	if err == nil || !strings.Contains(err.Error(), "spec-path") {
		t.Fatalf("expected spec-path validation error, got %v", err)
	}
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
	if planner.evalCalls != 0 {
		t.Fatal("evaluation should not run in dry-run")
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "docs", "SPEC.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "docs", "TASK.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "docs", "EVAL.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning", "product_spec.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning", "merged.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "dry-run", "git", "baseline.txt"))
	assertNoFile(t, filepath.Join(dir, "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "TASK.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "SPEC.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "TASK.md"))
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
	if !manifest.Evaluation.Ran || manifest.Evaluation.Skipped || manifest.Evaluation.Result != "SKIPPED" || manifest.Evaluation.EvalPath != "docs/EVAL.md" {
		t.Fatalf("dry-run evaluation should produce a skipped report, got %#v", manifest.Evaluation)
	}
	planning := readPlanning(t, filepath.Join(dir, ".jj", "runs", "dry-run", "planning", "planning.json"))
	if planning.Spec == "" || planning.Task == "" || len(planning.AcceptanceCriteria) == 0 || len(planning.TestGuidance) == 0 {
		t.Fatalf("normalized planning json missing top-level fields: %#v", planning)
	}
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
	if !strings.Contains(input, "jj Continuation Context") || !strings.Contains(input, "Previous Manifest") {
		t.Fatalf("input.md missing continuation context:\n%s", input)
	}
	if strings.Contains(original, "Previous Manifest") {
		t.Fatalf("input-original.md should remain unchanged:\n%s", original)
	}
	if !strings.Contains(contextInput, "Previous Manifest") {
		t.Fatalf("input-context.md missing context:\n%s", contextInput)
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
	if planner.evalCalls != 1 {
		t.Fatalf("expected one eval call, got %d", planner.evalCalls)
	}
	assertFileExists(t, filepath.Join(dir, "docs", "SPEC.md"))
	assertFileExists(t, filepath.Join(dir, "docs", "TASK.md"))
	assertFileExists(t, filepath.Join(dir, "docs", "EVAL.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "docs", "EVAL.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "codex", "events.jsonl"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "codex", "exit.json"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "full", "git", "diff.stat.txt"))
	evalData, err := os.ReadFile(filepath.Join(dir, ".jj", "runs", "full", "docs", "EVAL.md"))
	if err != nil {
		t.Fatalf("read eval: %v", err)
	}
	if !strings.Contains(string(evalData), "SPEC Requirement Results") || !strings.Contains(string(evalData), "TASK Item Results") {
		t.Fatalf("evaluation should classify spec and task items:\n%s", evalData)
	}
	if !strings.Contains(codexRunner.lastRequest.Prompt, "docs/SPEC.md") || !strings.Contains(codexRunner.lastRequest.Prompt, "docs/TASK.md") {
		t.Fatalf("codex prompt should reference docs paths:\n%s", codexRunner.lastRequest.Prompt)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "full", "manifest.json"))
	if manifest.SchemaVersion != "1" || manifest.Status != StatusCompleted || manifest.Evaluation.Result != "PASS" || manifest.Evaluation.Score != 90 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Artifacts["spec"] != "docs/SPEC.md" || manifest.Artifacts["task"] != "docs/TASK.md" || manifest.Artifacts["eval"] != "docs/EVAL.md" {
		t.Fatalf("unexpected artifact paths: %#v", manifest.Artifacts)
	}
	if manifest.Artifacts["spec_worktree"] != "docs/SPEC.md" || manifest.Artifacts["task_worktree"] != "docs/TASK.md" {
		t.Fatalf("unexpected artifact paths: %#v", manifest.Artifacts)
	}
	if manifest.Artifacts["eval_worktree"] != "docs/EVAL.md" || manifest.Config.OpenAIKeySet || manifest.Config.OpenAIKeyEnv == "" {
		t.Fatalf("unexpected manifest metadata: %#v", manifest)
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
	if manifest.Evaluation.Status != "pass" || manifest.Evaluation.Summary == "" {
		t.Fatalf("expected evaluation summary/status in manifest: %#v", manifest.Evaluation)
	}
	if len(manifest.Risks) == 0 || manifest.Risks[0] != "risk" {
		t.Fatalf("expected planning risk summary in manifest, got %#v", manifest.Risks)
	}
	if manifest.Commit.Ran || manifest.Commit.Status != "skipped" || manifest.Commit.SHA != "" {
		t.Fatalf("default non-dry-run should not commit, got %#v", manifest.Commit)
	}
}

func TestExecuteNonDryRunDoesNotCommitChanges(t *testing.T) {
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

	_, err := Execute(context.Background(), Config{
		PlanPath:        filepath.Join(dir, "plan.md"),
		CWD:             dir,
		RunID:           "no-commit-turn",
		PlanningAgents:  1,
		OpenAIModel:     "test-model",
		Stdout:          io.Discard,
		Planner:         &fakePlanner{},
		CodexRunner:     &fakeCodexRunner{mutate: true},
		CommitOnSuccess: true,
		CommitMessage:   "jj: turn no-commit-turn",
	})
	if err != nil {
		t.Fatalf("execute non-dry-run: %v", err)
	}
	headAfter := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("HEAD changed: before=%s after=%s", headBefore, headAfter)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "no-commit-turn", "manifest.json"))
	if manifest.Commit.Ran || manifest.Commit.Status != "skipped" || manifest.Commit.SHA != "" || manifest.Commit.Message != "" {
		t.Fatalf("expected skipped commit metadata, got %#v", manifest.Commit)
	}
	status := runGitOutput(t, dir, "status", "--short")
	for _, want := range []string{"docs/", "fake.go"} {
		if !strings.Contains(status, want) {
			t.Fatalf("expected %s to remain reviewable in git status, got %q", want, status)
		}
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
	if manifest.Commit.Ran || manifest.Commit.Status != "skipped" {
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
				"status --short":            "M docs/SPEC.md",
				"diff --stat":               " docs/SPEC.md | 1 +",
				"diff --name-status":        "M\tdocs/SPEC.md",
				"diff --binary":             "diff --git a/docs/SPEC.md b/docs/SPEC.md",
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

func TestExecutePathModeWritesWorkspaceRelativeRootDocs(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:        filepath.Join(dir, "plan.md"),
		CWD:             dir,
		RunID:           "root-docs",
		PlanningAgents:  1,
		OpenAIModel:     "test-model",
		SpecDoc:         "SPEC.md",
		TaskDoc:         "TASK.md",
		EvalDoc:         "EVAL.md",
		SpecDocPathMode: true,
		TaskDocPathMode: true,
		EvalDocPathMode: true,
		Stdout:          io.Discard,
		Planner:         &fakePlanner{},
		CodexRunner:     &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("execute root docs: %v", err)
	}
	assertFileExists(t, filepath.Join(dir, "SPEC.md"))
	assertFileExists(t, filepath.Join(dir, "TASK.md"))
	assertFileExists(t, filepath.Join(dir, "EVAL.md"))
	assertNoFile(t, filepath.Join(dir, "docs", "SPEC.md"))
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "root-docs", "manifest.json"))
	if manifest.Workspace.SpecPath != "SPEC.md" || manifest.Artifacts["spec_worktree"] != "SPEC.md" {
		t.Fatalf("expected root workspace paths in manifest: %#v", manifest)
	}
}

func TestExecuteUsesCustomDocumentNames(t *testing.T) {
	dir := initGit(t)
	writePlan(t, dir, "plan.md")
	codexRunner := &fakeCodexRunner{mutate: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "custom-docs",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		SpecDoc:        "PRODUCT.md",
		TaskDoc:        "WORK.md",
		EvalDoc:        "REVIEW.md",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{},
		CodexRunner:    codexRunner,
	})
	if err != nil {
		t.Fatalf("execute custom docs: %v", err)
	}
	assertFileExists(t, filepath.Join(dir, "docs", "PRODUCT.md"))
	assertFileExists(t, filepath.Join(dir, "docs", "WORK.md"))
	assertFileExists(t, filepath.Join(dir, "docs", "REVIEW.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "custom-docs", "docs", "EVAL.md"))
	if !strings.Contains(codexRunner.lastRequest.Prompt, "docs/PRODUCT.md") || !strings.Contains(codexRunner.lastRequest.Prompt, "docs/WORK.md") {
		t.Fatalf("codex prompt should reference custom docs paths:\n%s", codexRunner.lastRequest.Prompt)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "custom-docs", "manifest.json"))
	if manifest.Artifacts["spec"] != "docs/SPEC.md" || manifest.Artifacts["task"] != "docs/TASK.md" || manifest.Artifacts["eval"] != "docs/EVAL.md" {
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
	manifestPath := filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "manifest.json")
	manifest := readManifest(t, manifestPath)
	if manifest.Status != StatusPlanned || manifest.PlannerProvider != plannerProviderCodex || len(manifest.Errors) != 0 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.Config.CodexModel != "codex-test-model" {
		t.Fatalf("expected codex model in manifest, got %#v", manifest.Config)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "planning", "product_spec.events.jsonl"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-dry-run", "planning", "merge.last-message.txt"))
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
	if manifest.Planner.Model != "codex-test-model" || manifest.Config.CodexBin != fakeCodex {
		t.Fatalf("expected fake codex config in manifest, got planner=%#v config=%#v", manifest.Planner, manifest.Config)
	}
	assertFileExists(t, filepath.Join(runDir, "planning", "product_spec.events.jsonl"))
	assertFileExists(t, filepath.Join(runDir, "planning", "merge.last-message.txt"))
}

func TestExecuteFullRunWithoutOpenAIKeyUsesCodexPlannerAndEvaluation(t *testing.T) {
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
		t.Fatalf("expected draft, merge, eval calls, got %d", got)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "manifest.json"))
	if manifest.Status != StatusCompleted || manifest.PlannerProvider != plannerProviderCodex || manifest.Evaluation.Result != "PASS" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "docs", "EVAL.md"))
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-planner-full", "planning", "eval.events.jsonl"))
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
			},
		},
	})
	if err != nil {
		t.Fatalf("execute dry run: %v", err)
	}
	baseline := readFile(t, filepath.Join(dir, ".jj", "runs", "redacted-git-baseline", "git", "baseline.json"))
	if strings.Contains(baseline, "super-secret-value") || !strings.Contains(baseline, "[redacted]") {
		t.Fatalf("git baseline should be redacted:\n%s", baseline)
	}
}

func TestExecuteRedactsPersistedArtifactsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	secret := "run-secret-token-1234567890"
	openAIKey := "sk-proj-redact1234567890"
	t.Setenv("JJ_RUN_TEST_TOKEN", secret)
	writePlan(t, dir, "plan.md")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("Build a thing.\nAuthorization: Bearer "+secret+"\napi_key="+secret+"\n"+openAIKey+"\n"), 0o644); err != nil {
		t.Fatalf("write plan with secret: %v", err)
	}
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
	for _, rel := range []string{"docs/SPEC.md", "docs/TASK.md", "docs/EVAL.md"} {
		data := readFile(t, filepath.Join(dir, rel))
		if strings.Contains(data, secret) || strings.Contains(data, openAIKey) {
			t.Fatalf("%s contains raw secret:\n%s", rel, data)
		}
	}
	manifestData := readFile(t, filepath.Join(runDir, "manifest.json"))
	if !strings.Contains(manifestData, "[redacted]") {
		t.Fatalf("manifest missing redaction marker:\n%s", manifestData)
	}
	input := readFile(t, filepath.Join(runDir, "input.md"))
	if !strings.Contains(input, "[redacted]") || !strings.Contains(input, "[redacted-openai-key]") {
		t.Fatalf("input.md should retain redacted evidence:\n%s", input)
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
	planner := &fakePlanner{failAgents: map[string]error{"qa_eval": errors.New("qa failed")}}

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
		if agent.Name == "qa_eval" && agent.Status == "failed" {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("expected failed qa_eval agent in manifest: %#v", manifest.Planning.Agents)
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
	if !manifest.Codex.Skipped || !manifest.Evaluation.Skipped || manifest.Evaluation.Result != "SKIPPED" {
		t.Fatalf("expected unreached steps to be marked skipped: codex=%#v eval=%#v", manifest.Codex, manifest.Evaluation)
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

func TestExecuteRejectsEmptyMergedPlannerOutput(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, "plan.md")
	planner := &fakePlanner{emptyMerge: true}

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "empty-merge",
		PlanningAgents: 1,
		AllowNoGit:     true,
		DryRun:         true,
		Stdout:         io.Discard,
		Planner:        planner,
	})
	if err == nil || !strings.Contains(err.Error(), "merged spec is required") {
		t.Fatalf("expected merge validation failure, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "empty-merge", "manifest.json"))
	if manifest.Status != StatusPlanningFailed || manifest.FailurePhase != StatusPlanning {
		t.Fatalf("unexpected manifest: %#v", manifest)
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
		t.Fatalf("codex failure should return sanitized error after evaluation, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "codex-fail", "manifest.json"))
	if manifest.Status != StatusImplementationFailed {
		t.Fatalf("expected implementation failed status, got %q", manifest.Status)
	}
	if manifest.Codex.Error == "" || !strings.Contains(manifest.Codex.Error, "boom") {
		t.Fatalf("expected codex error in manifest, got %#v", manifest.Codex)
	}
	assertFileExists(t, filepath.Join(dir, ".jj", "runs", "codex-fail", "docs", "EVAL.md"))
}

func TestExecuteEvaluationFailureCapturesFinalGitDiff(t *testing.T) {
	dir := initGit(t)
	runGit(t, dir, "config", "user.email", "jj@example.com")
	runGit(t, dir, "config", "user.name", "jj test")
	writePlan(t, dir, "plan.md")
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jj/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "SPEC.md"), []byte("# old spec\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "TASK.md"), []byte("# old task\n"), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "EVAL.md"), []byte("# old eval\n"), 0o644); err != nil {
		t.Fatalf("write eval: %v", err)
	}
	runGit(t, dir, "add", "--all")
	runGit(t, dir, "commit", "-m", "initial")

	_, err := Execute(context.Background(), Config{
		PlanPath:       filepath.Join(dir, "plan.md"),
		CWD:            dir,
		RunID:          "eval-fail-final-diff",
		PlanningAgents: 1,
		OpenAIModel:    "test-model",
		Stdout:         io.Discard,
		Planner:        &fakePlanner{evalErr: errors.New("eval boom")},
		CodexRunner:    &fakeCodexRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "eval boom") {
		t.Fatalf("expected evaluation error, got %v", err)
	}
	manifest := readManifest(t, filepath.Join(dir, ".jj", "runs", "eval-fail-final-diff", "manifest.json"))
	if manifest.Status != StatusEvaluationFailed || manifest.Git.DiffPath != "git/diff.patch" || manifest.Git.StatusAfterPath != "git/status.after.txt" {
		t.Fatalf("expected failed manifest with final git evidence, got %#v", manifest)
	}
	diff := readFile(t, filepath.Join(dir, ".jj", "runs", "eval-fail-final-diff", "git", "diff.patch"))
	if !strings.Contains(diff, "docs/EVAL.md") {
		t.Fatalf("expected final diff to include evaluation file, got:\n%s", diff)
	}
}

type fakePlanner struct {
	mu               sync.Mutex
	draftIDs         []string
	models           []string
	evalCalls        int
	secret           string
	failAgents       map[string]error
	incompleteAgents map[string]bool
	failAll          bool
	emptyMerge       bool
	evalErr          error
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
	return merged, mustJSON(merged), nil
}

func (f *fakePlanner) Evaluate(_ context.Context, req ai.EvaluationRequest) (ai.EvaluationResult, []byte, error) {
	f.mu.Lock()
	f.evalCalls++
	f.models = append(f.models, req.Model)
	f.mu.Unlock()
	if f.evalErr != nil {
		return ai.EvaluationResult{}, []byte("not-json"), f.evalErr
	}
	secretSuffix := ""
	if f.secret != "" {
		secretSuffix = " " + f.secret
	}
	risks := []string{}
	followups := []string{}
	if secretSuffix != "" {
		risks = []string{secretSuffix}
		followups = []string{"review" + secretSuffix}
	}
	eval := ai.EvaluationResult{
		Result:               "PASS",
		Score:                90,
		Summary:              "Looks good." + secretSuffix,
		WhatChanged:          []string{"fake.go changed" + secretSuffix},
		RequirementsCoverage: []string{"Spec and task were used." + secretSuffix},
		TestCoverage:         []string{"fake tests passed" + secretSuffix},
		Risks:                risks,
		Regressions:          []string{},
		RecommendedFollowups: followups,
	}
	return eval, mustJSON(eval), nil
}

type fakeCodexRunner struct {
	called      bool
	mutate      bool
	err         error
	secret      string
	lastRequest codex.Request
}

func (f *fakeCodexRunner) Run(_ context.Context, req codex.Request) (codex.Result, error) {
	f.called = true
	f.lastRequest = req
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
		return "", errors.New("unexpected git command: " + key)
	}
	return value + "\n", nil
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
