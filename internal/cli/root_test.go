package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/serve"
)

func TestRunCommandParsesFlags(t *testing.T) {
	var got run.Config
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		got = cfg
		return &run.Result{}, nil
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"run", "plan.md",
		"--dry-run",
		"--cwd", "/tmp/repo",
		"--run-id", "run-1",
		"--planner-agents", "2",
		"--openai-model", "model-a",
		"--codex-model", "model-b",
		"--codex-binary", "/tmp/codex",
		"--task-proposal-mode", "security",
		"--repo", "https://github.com/acme/app.git",
		"--repo-dir", "/tmp/repo-clone",
		"--base-branch", "main",
		"--work-branch", "jj/test",
		"--push",
		"--push-mode", "branch",
		"--github-token-env", "MY_GITHUB_TOKEN",
		"--allow-dirty",
		"--allow-no-git",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if got.PlanPath != "plan.md" || !got.DryRun || got.CWD != "/tmp/repo" || got.RunID != "run-1" {
		t.Fatalf("unexpected parsed config: %#v", got)
	}
	if got.PlanningAgents != 2 || got.OpenAIModel != "model-a" || got.CodexModel != "model-b" || got.CodexBin != "/tmp/codex" || !got.AllowNoGit {
		t.Fatalf("unexpected parsed flags: %#v", got)
	}
	if got.TaskProposalMode != run.TaskProposalModeSecurity || !got.TaskProposalModeExplicit {
		t.Fatalf("unexpected task proposal mode: %#v", got)
	}
	if got.RepoURL != "https://github.com/acme/app.git" || got.RepoDir != "/tmp/repo-clone" || got.BaseBranch != "main" || got.WorkBranch != "jj/test" {
		t.Fatalf("unexpected repository flags: %#v", got)
	}
	if !got.Push || got.PushMode != "branch" || got.GitHubTokenEnv != "MY_GITHUB_TOKEN" || !got.RepoAllowDirty {
		t.Fatalf("unexpected repository push/auth flags: %#v", got)
	}
	if !got.RepoURLExplicit || !got.RepoDirExplicit || !got.BaseBranchExplicit || !got.WorkBranchExplicit || !got.PushExplicit || !got.PushModeExplicit || !got.GitHubTokenEnvExplicit || !got.RepoAllowDirtyExplicit {
		t.Fatalf("expected repository explicit markers: %#v", got)
	}
	if !got.PlanningAgentsExplicit || !got.OpenAIModelExplicit || !got.CodexModelExplicit || !got.CodexBinExplicit {
		t.Fatalf("expected explicit flag markers: %#v", got)
	}
	if !got.DryRunExplicit || !got.AllowNoGitExplicit {
		t.Fatalf("expected explicit boolean flag markers: %#v", got)
	}
	if got.ConfigSearchDir == "" {
		t.Fatal("expected config search directory to be set")
	}
}

func TestRunCommandRejectsRemovedDocumentFlags(t *testing.T) {
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		t.Fatal("executor should not be called")
		return &run.Result{}, nil
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "plan.md", "--spec-path", "SPEC.md"})

	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected removed flag error, got %v", err)
	}
}

func TestRunCommandRequiresPlan(t *testing.T) {
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		t.Fatal("executor should not be called")
		return nil, nil
	})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"run"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Fatalf("expected argument error, got %v", err)
	}
}

func TestRunCommandRejectsInvalidTaskProposalMode(t *testing.T) {
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		t.Fatal("executor should not be called")
		return nil, nil
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "plan.md", "--task-proposal-mode", "fast"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), `invalid task proposal mode: "fast"`) || !strings.Contains(err.Error(), run.ValidTaskProposalModesString()) {
		t.Fatalf("expected invalid task proposal mode error, got %v", err)
	}
}

func TestRunCommandParsesTaskProposalModeAlias(t *testing.T) {
	var got run.Config
	cmd := newRootCommand(func(_ context.Context, cfg run.Config) (*run.Result, error) {
		got = cfg
		return &run.Result{}, nil
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "plan.md", "--proposal-mode", "docs"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if got.TaskProposalMode != run.TaskProposalModeDocs || !got.TaskProposalModeExplicit {
		t.Fatalf("unexpected task proposal mode alias config: %#v", got)
	}
}

func TestRunCommandAutoContinueContinuesAfterPassUntilMaxTurns(t *testing.T) {
	dir := t.TempDir()
	executor := &cliLoopFakeExecutor{
		statuses:    []string{run.StatusSuccess, run.StatusSuccess, run.StatusSuccess},
		validations: []string{"passed", "passed", "passed"},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newRootCommandWithServeAndIO(executor.Run, serve.Execute, &stdout, &stderr)
	cmd.SetArgs([]string{"run", "plan.md", "--cwd", dir, "--run-id", "loop-pass", "--auto-continue", "--max-turns", "3", "--allow-no-git"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute auto-continue: %v", err)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("expected three turns, got %d", len(executor.calls))
	}
	if executor.calls[0].RunID != "loop-pass" {
		t.Fatalf("unexpected turn run IDs: %#v", executor.calls)
	}
	if !executor.calls[0].LoopEnabled || executor.calls[0].LoopBaseRunID != "loop-pass" || executor.calls[0].LoopTurn != 1 || executor.calls[0].LoopMaxTurns != 3 {
		t.Fatalf("first turn missing loop metadata: %#v", executor.calls[0])
	}
	if executor.calls[1].RunID != "loop-pass-t02" || executor.calls[2].RunID != "loop-pass-t03" {
		t.Fatalf("unexpected turn run IDs: %#v", executor.calls)
	}
	if !strings.Contains(executor.calls[1].AdditionalPlanContext, "Previous Manifest") {
		t.Fatalf("second turn missing continuation context: %#v", executor.calls[1])
	}
	if !strings.Contains(stdout.String(), "jj loop: stopped: max turns reached") {
		t.Fatalf("loop progress output missing expected text:\n%s", stdout.String())
	}
}

func TestRunCommandAutoContinueValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "max turns without auto continue", args: []string{"run", "plan.md", "--max-turns", "3"}, want: "--max-turns requires --auto-continue"},
		{name: "dry run", args: []string{"run", "plan.md", "--auto-continue", "--dry-run"}, want: "auto continue requires full-run"},
		{name: "too low", args: []string{"run", "plan.md", "--auto-continue", "--max-turns", "0"}, want: "max turns must be between 1 and 50"},
		{name: "too high", args: []string{"run", "plan.md", "--auto-continue", "--max-turns", "51"}, want: "max turns must be between 1 and 50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRootCommand(func(_ context.Context, _ run.Config) (*run.Result, error) {
				t.Fatal("executor should not be called")
				return nil, nil
			})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tt.args)

			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestRunCommandAutoContinueStopsAtMaxTurns(t *testing.T) {
	dir := t.TempDir()
	executor := &cliLoopFakeExecutor{
		statuses:    []string{"needs_work", "needs_work", "needs_work"},
		validations: []string{"skipped", "skipped", "skipped"},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newRootCommandWithServeAndIO(executor.Run, serve.Execute, &stdout, &stderr)
	cmd.SetArgs([]string{"run", "plan.md", "--cwd", dir, "--run-id", "loop-max", "--auto-continue", "--max-turns", "2", "--allow-no-git"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute auto-continue max turns: %v", err)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("expected two turns, got %d", len(executor.calls))
	}
	if !strings.Contains(stdout.String(), "jj loop: stopped: max turns reached") {
		t.Fatalf("loop output missing max turns stop:\n%s", stdout.String())
	}
}

func TestRunCommandAutoContinueStopsOnExecutorError(t *testing.T) {
	dir := t.TempDir()
	executor := &cliLoopFakeExecutor{
		statuses:    []string{"needs_work"},
		validations: []string{"skipped"},
		errAt:       2,
	}
	cmd := newRootCommandWithServeAndIO(executor.Run, serve.Execute, &bytes.Buffer{}, &bytes.Buffer{})
	cmd.SetArgs([]string{"run", "plan.md", "--cwd", dir, "--run-id", "loop-error", "--auto-continue", "--max-turns", "3", "--allow-no-git"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected executor error, got %v", err)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("expected two executor calls, got %d", len(executor.calls))
	}
}

func TestRunCommandAutoContinueRejectsReportedRunDirOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "unsafe-secret-token-1234567890")
	cmd := newRootCommandWithServeAndIO(
		func(_ context.Context, cfg run.Config) (*run.Result, error) {
			return &run.Result{RunID: cfg.RunID, RunDir: outside}, nil
		},
		serve.Execute,
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	cmd.SetArgs([]string{"run", "plan.md", "--cwd", dir, "--run-id", "unsafe-loop", "--auto-continue", "--max-turns", "2", "--allow-no-git"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reported run directory is outside the expected run root") {
		t.Fatalf("expected reported run dir rejection, got %v", err)
	}
	if strings.Contains(err.Error(), outside) || strings.Contains(err.Error(), "unsafe-secret-token-1234567890") {
		t.Fatalf("reported run dir rejection leaked unsafe path: %v", err)
	}
}

func TestRunCommandAutoContinueReusesRepositoryWorkBranch(t *testing.T) {
	dir := t.TempDir()
	executor := &cliLoopFakeExecutor{
		statuses:    []string{"needs_work", run.StatusSuccess},
		validations: []string{"skipped", "passed"},
	}
	cmd := newRootCommandWithServeAndIO(executor.Run, serve.Execute, &bytes.Buffer{}, &bytes.Buffer{})
	cmd.SetArgs([]string{"run", "plan.md", "--cwd", dir, "--run-id", "gh-loop", "--repo", "https://github.com/acme/app.git", "--auto-continue", "--max-turns", "2", "--allow-no-git"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute repository loop: %v", err)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("expected two turns, got %d", len(executor.calls))
	}
	for _, call := range executor.calls {
		if call.WorkBranch != "jj/run-gh-loop" || !call.WorkBranchExplicit {
			t.Fatalf("repository loop did not reuse generated work branch: %#v", call)
		}
	}

	executor = &cliLoopFakeExecutor{
		statuses:    []string{"needs_work", run.StatusSuccess},
		validations: []string{"skipped", "passed"},
	}
	cmd = newRootCommandWithServeAndIO(executor.Run, serve.Execute, &bytes.Buffer{}, &bytes.Buffer{})
	cmd.SetArgs([]string{"run", "plan.md", "--cwd", dir, "--run-id", "gh-loop-explicit", "--repo", "https://github.com/acme/app.git", "--work-branch", "jj/custom", "--auto-continue", "--max-turns", "2", "--allow-no-git"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute explicit repository loop: %v", err)
	}
	for _, call := range executor.calls {
		if call.WorkBranch != "jj/custom" || !call.WorkBranchExplicit {
			t.Fatalf("repository loop did not reuse explicit work branch: %#v", call)
		}
	}
}

func TestMainReturnsValidationExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main([]string{"run"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected validation exit code 2, got %d stderr=%s", code, stderr.String())
	}
}

func TestRunCommandHelp(t *testing.T) {
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"run", "--help"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "--dry-run") || !strings.Contains(stdout.String(), "--cwd") || strings.Contains(stdout.String(), "--spec-path") || strings.Contains(stdout.String(), "--task-path") || strings.Contains(stdout.String(), "--eval-path") || !strings.Contains(stdout.String(), "--planner-agents") || !strings.Contains(stdout.String(), "--task-proposal-mode") || !strings.Contains(stdout.String(), "--repo") || !strings.Contains(stdout.String(), "--push") || !strings.Contains(stdout.String(), "--auto-continue") || !strings.Contains(stdout.String(), "--max-turns") {
		t.Fatalf("help output missing expected flags:\n%s", stdout.String())
	}
}

func TestStatusCommandPrintsSanitizedSummary(t *testing.T) {
	dir := newStatusWorkspace(t, "- [~] TASK-0053 [feature] Add sanitized status\n- [x] TASK-0001 [security] Done\n")
	writeStatusFile(t, dir, ".jj/runs/20260428-120000-good/manifest.json", `{
		"run_id":"20260428-120000-good",
		"status":"complete",
		"started_at":"2026-04-28T12:00:00Z",
		"planner_provider":"openai",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"passed","evidence_status":"recorded","command_count":2,"passed_count":2,"failed_count":0}
	}`)

	out := runStatusCommandOutput(t, dir)
	for _, want := range []string{
		"TASK: state=available total=2 done=1 in_progress=1 pending=0 blocked=0",
		"TASK Next: id=TASK-0053 status=in-progress category=feature",
		"Latest Run: state=available run=20260428-120000-good status=complete provider_or_result=openai evaluation=passed timestamp=2026-04-28T12:00:00Z",
		"Next Action: state=continue_task label=Continue Task task=TASK-0053 status=in-progress category=feature",
		"Active Run: state=none",
		"Validation Status: state=passed run=20260428-120000-good counts=commands 2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestStatusCommandNoRuns(t *testing.T) {
	dir := newStatusWorkspace(t, "- [x] TASK-0001 [feature] Done\n")

	out := runStatusCommandOutput(t, dir)
	for _, want := range []string{
		"TASK: state=available total=1 done=1 in_progress=0 pending=0 blocked=0",
		"Latest Run: state=none run=none status=none provider_or_result=none evaluation=none timestamp=none",
		"Active Run: state=none",
		"Validation Status: state=none",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestStatusCommandActiveRunPresent(t *testing.T) {
	dir := newStatusWorkspace(t, "- [ ] TASK-0053 [feature] Add status\n")
	writeStatusFile(t, dir, ".jj/runs/20260428-130000-active/manifest.json", `{
		"run_id":"20260428-130000-active",
		"status":"running",
		"started_at":"2026-04-28T13:00:00Z",
		"planner_provider":"openai",
		"artifacts":{"manifest":"manifest.json"}
	}`)

	out := runStatusCommandOutput(t, dir)
	if !strings.Contains(out, "Active Run: state=available run=20260428-130000-active status=running provider_or_result=openai evaluation=unknown timestamp=2026-04-28T13:00:00Z") {
		t.Fatalf("status output missing active run:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestStatusCommandValidationMetadataPresent(t *testing.T) {
	dir := newStatusWorkspace(t, "- [x] TASK-0001 [feature] Done\n")
	writeStatusFile(t, dir, ".jj/runs/20260428-140000-validation/manifest.json", `{
		"run_id":"20260428-140000-validation",
		"status":"failed",
		"started_at":"2026-04-28T14:00:00Z",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"failed","evidence_status":"recorded","command_count":3,"passed_count":1,"failed_count":2}
	}`)

	out := runStatusCommandOutput(t, dir)
	if !strings.Contains(out, "Validation Status: state=failed run=20260428-140000-validation counts=commands 3") ||
		!strings.Contains(out, "failed 2") {
		t.Fatalf("status output missing validation metadata:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestStatusCommandMalformedMetadata(t *testing.T) {
	dir := newStatusWorkspace(t, "- [x] TASK-0001 [feature] Done\n")
	writeStatusFile(t, dir, ".jj/runs/20260428-150000-malformed/manifest.json", `{"run_id":"20260428-150000-malformed","status":"complete",`)

	out := runStatusCommandOutput(t, dir)
	for _, want := range []string{
		"Latest Run: state=unavailable run=20260428-150000-malformed status=unavailable provider_or_result=unavailable",
		"Validation Status: state=unavailable run=20260428-150000-malformed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestStatusCommandDeniedRunMetadata(t *testing.T) {
	dir := newStatusWorkspace(t, "- [x] TASK-0001 [feature] Done\n")
	runDir := filepath.Join(dir, ".jj", "runs", "20260428-160000-denied")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside-sk-proj-denied1234567890.json")
	if err := os.WriteFile(outside, []byte(`{"status":"secret"}`), 0o644); err != nil {
		t.Fatalf("write outside manifest: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(runDir, "manifest.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	out := runStatusCommandOutput(t, dir)
	if !strings.Contains(out, "Latest Run: state=denied run=20260428-160000-denied") ||
		!strings.Contains(out, "Validation Status: state=denied run=20260428-160000-denied") {
		t.Fatalf("status output missing denied state:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir, outside, "sk-proj-denied1234567890")
}

func TestStatusCommandHostileLabelsAreSanitized(t *testing.T) {
	dir := newStatusWorkspace(t, "- [ ] TASK-0053 [feature] Add status\n")
	secret := "sk-proj-statushostile1234567890"
	writeStatusFile(t, dir, ".jj/runs/20260428-170000-hostile/manifest.json", fmt.Sprintf(`{
		"run_id":"20260428-170000-hostile",
		"status":"complete",
		"started_at":"2026-04-28T17:00:00Z",
		"planner_provider":"Authorization: Bearer %s",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"passed","commands":[{"label":"API_KEY=%s","status":"passed"}]}
	}`, secret, secret))

	out := runStatusCommandOutput(t, dir)
	if !strings.Contains(out, "Latest Run: state=available run=20260428-170000-hostile status=complete provider_or_result=result complete evaluation=passed") {
		t.Fatalf("status output did not use deterministic sanitized labels:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir, secret, "Authorization", "API_KEY=")
}

func TestStatusCommandTokenLikeRunIDIsNotRendered(t *testing.T) {
	dir := newStatusWorkspace(t, "- [x] TASK-0001 [feature] Done\n")
	tokenRunID := "sk-proj-tokenlike1234567890"
	writeStatusFile(t, dir, ".jj/runs/"+tokenRunID+"/manifest.json", fmt.Sprintf(`{"run_id":%q,"status":"complete","artifacts":{"manifest":"manifest.json"}}`, tokenRunID))

	out := runStatusCommandOutput(t, dir)
	if !strings.Contains(out, "Latest Run: state=none run=none") {
		t.Fatalf("token-like run id should not be rendered:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir, tokenRunID)
}

func TestStatusCommandDeterministicLabels(t *testing.T) {
	dir := newStatusWorkspace(t, "- [x] TASK-0001 [feature] Done\n")
	for _, runID := range []string{"20260428-180000-a", "20260428-180000-b"} {
		writeStatusFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
			"run_id":%q,
			"status":"complete",
			"started_at":"not-a-time",
			"artifacts":{"manifest":"manifest.json"},
			"validation":{"ran":true,"status":"passed","evidence_status":"recorded"}
		}`, runID))
	}

	out := runStatusCommandOutput(t, dir)
	if !strings.Contains(out, "Latest Run: state=available run=20260428-180000-b") ||
		strings.Contains(out, "Latest Run: state=available run=20260428-180000-a") {
		t.Fatalf("status output did not use deterministic run label ordering:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestRunsCommandPrintsSanitizedRecentRunsWithDeterministicOrdering(t *testing.T) {
	dir := t.TempDir()
	writeRecentRunManifest := func(id, status, startedAt, provider, validation string) {
		fields := []string{
			fmt.Sprintf(`"run_id":%q`, id),
			fmt.Sprintf(`"status":%q`, status),
			fmt.Sprintf(`"planner_provider":%q`, provider),
			`"artifacts":{"manifest":"manifest.json"}`,
			fmt.Sprintf(`"validation":{"ran":true,"status":%q,"evidence_status":"recorded","command_count":2,"passed_count":1,"failed_count":0}`, validation),
		}
		if startedAt != "" {
			fields = append(fields, fmt.Sprintf(`"started_at":%q`, startedAt))
		}
		writeStatusFile(t, dir, ".jj/runs/"+id+"/manifest.json", "{"+strings.Join(fields, ",")+"}")
	}
	writeRecentRunManifest("20260429-120000-tie-b", "needs_work", "2026-04-29T15:00:00Z", "codex", "needs_work")
	writeRecentRunManifest("20260429-110000-tie-a", "complete", "2026-04-29T15:00:00Z", "openai", "passed")
	writeRecentRunManifest("20260429-140000-id-fallback", "failed", "not-a-time", "local", "failed")
	writeRecentRunManifest("20260429-130000-no-time", "complete", "", "codex", "passed")
	writeRecentRunManifest("20260429-100000-fifth", "complete", "2026-04-29T10:00:00Z", "openai", "passed")
	writeRecentRunManifest("20260429-090000-excluded", "complete", "2026-04-29T09:00:00Z", "openai", "passed")

	out := runRunsCommandOutput(t, dir)
	for _, want := range []string{
		"Runs: state=available total=5",
		"Run 1: state=available run=20260429-120000-tie-b status=needs_work provider_or_result=codex evaluation=needs_work validation=needs_work timestamp=2026-04-29T15:00:00Z",
		"Run 2: state=available run=20260429-110000-tie-a status=complete provider_or_result=openai evaluation=passed validation=passed timestamp=2026-04-29T15:00:00Z",
		"Run 3: state=available run=20260429-140000-id-fallback status=failed provider_or_result=local evaluation=failed validation=failed timestamp=unknown",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runs output missing %q:\n%s", want, out)
		}
	}
	ordered := []string{
		"20260429-120000-tie-b",
		"20260429-110000-tie-a",
		"20260429-140000-id-fallback",
		"20260429-130000-no-time",
		"20260429-100000-fifth",
	}
	last := -1
	for _, id := range ordered {
		idx := strings.Index(out, id)
		if idx < 0 || idx <= last {
			t.Fatalf("runs output order is not deterministic around %q:\n%s", id, out)
		}
		last = idx
	}
	if strings.Contains(out, "20260429-090000-excluded") {
		t.Fatalf("runs output should use the dashboard recent-run limit:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestRunsCommandNoRuns(t *testing.T) {
	dir := t.TempDir()

	out := runRunsCommandOutput(t, dir)
	if strings.TrimSpace(out) != "Runs: state=none total=0" {
		t.Fatalf("unexpected no-run output:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestRunsCommandMalformedPartialAndDeniedRunsAreDeterministic(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-sk-proj-runsdenied1234567890.json")
	writeStatusFile(t, dir, ".jj/runs/20260429-120000-badjson/manifest.json", `{"run_id":"20260429-120000-badjson","status":"complete",`)
	writeStatusFile(t, dir, ".jj/runs/20260429-121000-partial/manifest.json", `{"run_id":"20260429-121000-partial","status":"success"}`)
	writeStatusFile(t, dir, ".jj/runs/20260429-122000-mismatch/manifest.json", `{"run_id":"20260429-000000-other","status":"success","artifacts":{"manifest":"manifest.json"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj", "runs", "20260429-123000-missing"), 0o755); err != nil {
		t.Fatalf("mkdir missing run: %v", err)
	}
	if err := os.WriteFile(outside, []byte(`{"run_id":"20260429-124000-denied","status":"complete","artifacts":{"manifest":"manifest.json"}}`), 0o644); err != nil {
		t.Fatalf("write outside manifest: %v", err)
	}
	deniedDir := filepath.Join(dir, ".jj", "runs", "20260429-124000-denied")
	if err := os.MkdirAll(deniedDir, 0o755); err != nil {
		t.Fatalf("mkdir denied run: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(deniedDir, "manifest.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	out := runRunsCommandOutput(t, dir)
	for _, want := range []string{
		"Runs: state=available total=5",
		"Run 1: state=denied run=20260429-124000-denied status=denied provider_or_result=denied evaluation=denied validation=denied timestamp=unknown",
		"Run 2: state=unavailable run=20260429-123000-missing status=unavailable provider_or_result=unavailable evaluation=unavailable validation=unavailable timestamp=unknown",
		"Run 3: state=unavailable run=20260429-122000-mismatch status=unavailable provider_or_result=unavailable evaluation=unavailable validation=unavailable timestamp=unknown",
		"Run 4: state=unavailable run=20260429-121000-partial status=unavailable provider_or_result=unavailable evaluation=unavailable validation=unavailable timestamp=unknown",
		"Run 5: state=unavailable run=20260429-120000-badjson status=unavailable provider_or_result=unavailable evaluation=unavailable validation=unavailable timestamp=unknown",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runs output missing %q:\n%s", want, out)
		}
	}
	assertStatusOutputSafe(t, out, dir, outside, "sk-proj-runsdenied1234567890", "manifest is malformed", "manifest is incomplete")
}

func TestRunsCommandHostileLabelsAndTokenLikeRunIDsAreSanitized(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-runshostile1234567890"
	writeStatusFile(t, dir, ".jj/runs/20260429-130000-hostile/manifest.json", fmt.Sprintf(`{
		"run_id":"20260429-130000-hostile",
		"status":"complete",
		"started_at":"2026-04-29T13:00:00Z",
		"planner_provider":"Authorization: Bearer %s",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"passed","evidence_status":"recorded","commands":[{"label":"API_KEY=%s","status":"passed"}]}
	}`, secret, secret))
	tokenRunID := "sk-proj-runstoken1234567890"
	writeStatusFile(t, dir, ".jj/runs/"+tokenRunID+"/manifest.json", fmt.Sprintf(`{"run_id":%q,"status":"complete","artifacts":{"manifest":"manifest.json"}}`, tokenRunID))

	out := runRunsCommandOutput(t, dir)
	if !strings.Contains(out, "Run: state=available run=20260429-130000-hostile status=complete provider_or_result=result complete evaluation=passed validation=passed timestamp=2026-04-29T13:00:00Z") {
		t.Fatalf("runs output did not use deterministic sanitized hostile labels:\n%s", out)
	}
	assertStatusOutputSafe(t, out, dir, secret, tokenRunID, "Authorization", "API_KEY=")
}

func TestRunsCommandStaleAndInternallyInconsistentMetadata(t *testing.T) {
	dir := t.TempDir()
	writeStatusFile(t, dir, ".jj/runs/20260429-140000-inconsistent/manifest.json", `{
		"run_id":"20260429-140000-inconsistent",
		"status":"complete",
		"started_at":"2026-04-29T14:00:00Z",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"passed","evidence_status":"recorded","command_count":1,"failed_count":1}
	}`)
	writeStatusFile(t, dir, ".jj/runs/20260429-130000-stale/manifest.json", `{
		"run_id":"20260429-130000-stale",
		"status":"stale",
		"started_at":"2026-04-29T13:00:00Z",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"stale","evidence_status":"stale"}
	}`)

	out := runRunsCommandOutput(t, dir)
	for _, want := range []string{
		"Run 1: state=unknown run=20260429-140000-inconsistent status=unknown provider_or_result=unknown evaluation=unknown validation=unknown timestamp=2026-04-29T14:00:00Z",
		"Run 2: state=unavailable run=20260429-130000-stale status=unavailable provider_or_result=unavailable evaluation=unavailable validation=unavailable timestamp=2026-04-29T13:00:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("runs output missing %q:\n%s", want, out)
		}
	}
	assertStatusOutputSafe(t, out, dir)
}

func TestCLIRunSummaryLineUsesOrderedFieldsAndFallbacks(t *testing.T) {
	var latest bytes.Buffer
	writeCLIRunSummaryLine(&latest, cliLatestRunSummaryLine(serve.StatusLatestRunSummary{
		State:            "available",
		Status:           "complete",
		ProviderOrResult: "codex",
		EvaluationState:  "passed",
		TimestampLabel:   "2026-04-29T15:00:00Z",
	}))
	if got, want := latest.String(), "Latest Run: state=available run=none status=complete provider_or_result=codex evaluation=passed timestamp=2026-04-29T15:00:00Z\n"; got != want {
		t.Fatalf("latest-run summary line changed:\nwant %q\ngot  %q", want, got)
	}

	var active bytes.Buffer
	writeCLIRunSummaryLine(&active, cliActiveRunSummaryLine(serve.StatusActiveRunItem{
		RunID: "20260429-130000-active",
	}, 1, 0))
	if got, want := active.String(), "Active Run: state=available run=20260429-130000-active status=unknown provider_or_result=unknown evaluation=unknown timestamp=unknown\n"; got != want {
		t.Fatalf("active-run summary line changed:\nwant %q\ngot  %q", want, got)
	}

	var numberedActive bytes.Buffer
	writeCLIRunSummaryLine(&numberedActive, cliActiveRunSummaryLine(serve.StatusActiveRunItem{
		RunID: "20260429-130000-active",
	}, 2, 1))
	if got, want := numberedActive.String(), "Active Run 2: state=available run=20260429-130000-active status=unknown provider_or_result=unknown evaluation=unknown timestamp=unknown\n"; got != want {
		t.Fatalf("numbered active-run summary line changed:\nwant %q\ngot  %q", want, got)
	}

	var recent bytes.Buffer
	writeCLIRunSummaryLine(&recent, cliRecentRunSummaryLine(serve.RecentRunItem{
		State:            "denied",
		RunID:            "20260429-124000-denied",
		Status:           "[redacted]",
		ProviderOrResult: "sensitive value removed",
		ValidationState:  "denied",
	}, 1, 0))
	if got, want := recent.String(), "Run: state=denied run=20260429-124000-denied status=unknown provider_or_result=unknown evaluation=unknown validation=denied timestamp=unknown\n"; got != want {
		t.Fatalf("recent-run summary line changed:\nwant %q\ngot  %q", want, got)
	}
}

type cliLoopFakeExecutor struct {
	calls       []run.Config
	statuses    []string
	validations []string
	errAt       int
}

func (f *cliLoopFakeExecutor) Run(_ context.Context, cfg run.Config) (*run.Result, error) {
	f.calls = append(f.calls, cfg)
	call := len(f.calls)
	if f.errAt == call {
		return nil, errors.New("boom")
	}
	status := run.StatusSuccess
	if call-1 < len(f.statuses) && f.statuses[call-1] != "" {
		status = f.statuses[call-1]
	}
	validation := "passed"
	if call-1 < len(f.validations) && f.validations[call-1] != "" {
		validation = f.validations[call-1]
	}
	runDir := filepath.Join(cfg.CWD, ".jj", "runs", cfg.RunID)
	if err := writeCLILoopFile(filepath.Join(cfg.CWD, ".jj", "spec.json"), "{}\n"); err != nil {
		return nil, err
	}
	if err := writeCLILoopFile(filepath.Join(cfg.CWD, ".jj", "tasks.json"), "{}\n"); err != nil {
		return nil, err
	}
	if err := writeCLILoopFile(filepath.Join(runDir, "git", "diff-summary.txt"), "fake diff\n"); err != nil {
		return nil, err
	}
	if err := writeCLILoopFile(filepath.Join(runDir, "codex", "summary.md"), "fake codex summary\n"); err != nil {
		return nil, err
	}
	manifest := fmt.Sprintf(`{"run_id":%q,"status":%q,"validation":{"status":%q},"commit":{"status":"skipped"}}`, cfg.RunID, status, validation)
	if err := writeCLILoopFile(filepath.Join(runDir, "manifest.json"), manifest); err != nil {
		return nil, err
	}
	return &run.Result{RunID: cfg.RunID, RunDir: runDir}, nil
}

func writeCLILoopFile(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o644)
}

func newStatusWorkspace(t *testing.T, taskMarkdown string) string {
	t.Helper()
	dir := t.TempDir()
	writeStatusFile(t, dir, "docs/TASK.md", "# Current TASK\n\n"+taskMarkdown)
	return dir
}

func runStatusCommandOutput(t *testing.T, dir string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := newRootCommandWithServeAndIO(
		func(_ context.Context, _ run.Config) (*run.Result, error) {
			t.Fatal("run executor should not be called")
			return nil, nil
		},
		serve.Execute,
		&stdout,
		&stderr,
	)
	cmd.SetArgs([]string{"status", "--cwd", dir})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("status command failed: %v stderr=%s", err, stderr.String())
	}
	return stdout.String()
}

func runRunsCommandOutput(t *testing.T, dir string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := newRootCommandWithServeAndIO(
		func(_ context.Context, _ run.Config) (*run.Result, error) {
			t.Fatal("run executor should not be called")
			return nil, nil
		},
		serve.Execute,
		&stdout,
		&stderr,
	)
	cmd.SetArgs([]string{"runs", "--cwd", dir})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("runs command failed: %v stderr=%s", err, stderr.String())
	}
	return stdout.String()
}

func writeStatusFile(t *testing.T, root, rel, data string) {
	t.Helper()
	if err := writeCLILoopFile(filepath.Join(root, filepath.FromSlash(rel)), data); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func assertStatusOutputSafe(t *testing.T, out string, forbidden ...string) {
	t.Helper()
	defaultForbidden := []string{
		"sk-proj-",
		"ghp_",
		"BEGIN PRIVATE KEY",
		"raw command text",
		"raw environment",
		"raw artifact body",
		"raw diff body",
		"[jj-omitted]",
		"[REDACTED]",
		"[redacted]",
		"[omitted]",
		"sensitive value removed",
		"unsafe value removed",
	}
	forbidden = append(defaultForbidden, forbidden...)
	for _, value := range forbidden {
		if value != "" && strings.Contains(out, value) {
			t.Fatalf("status output leaked %q:\n%s", value, out)
		}
	}
}

func TestServeCommandParsesFlags(t *testing.T) {
	var gotCWD, gotAddr, gotRunID string
	var gotAddrExplicit bool
	cmd := newRootCommandWithServe(
		func(_ context.Context, cfg run.Config) (*run.Result, error) {
			t.Fatal("run executor should not be called")
			return nil, nil
		},
		func(_ context.Context, cfg serve.Config) error {
			gotCWD = cfg.CWD
			gotAddr = cfg.Addr
			gotRunID = cfg.RunID
			gotAddrExplicit = cfg.AddrExplicit
			return nil
		},
	)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"serve", "--cwd", "/tmp/repo", "--addr", "127.0.0.1:0", "--run-id", "run-1"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if gotCWD != "/tmp/repo" || gotAddr != "127.0.0.1:0" || gotRunID != "run-1" {
		t.Fatalf("unexpected serve config: cwd=%q addr=%q runID=%q", gotCWD, gotAddr, gotRunID)
	}
	if !gotAddrExplicit {
		t.Fatalf("expected addr explicit marker")
	}
}

func TestServeCommandParsesHostPort(t *testing.T) {
	var gotHost string
	var gotPort int
	var gotHostExplicit, gotPortExplicit bool
	cmd := newRootCommandWithServe(
		func(_ context.Context, cfg run.Config) (*run.Result, error) {
			t.Fatal("run executor should not be called")
			return nil, nil
		},
		func(_ context.Context, cfg serve.Config) error {
			gotHost = cfg.Host
			gotPort = cfg.Port
			gotHostExplicit = cfg.HostExplicit
			gotPortExplicit = cfg.PortExplicit
			return nil
		},
	)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"serve", "--cwd", "/tmp/repo", "--host", "localhost", "--port", "0"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if gotHost != "localhost" || gotPort != 0 || !gotHostExplicit || !gotPortExplicit {
		t.Fatalf("unexpected host/port config: host=%q port=%d explicit=%t/%t", gotHost, gotPort, gotHostExplicit, gotPortExplicit)
	}
}

func TestServeCommandHelp(t *testing.T) {
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"serve", "--help"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "--addr") || !strings.Contains(stdout.String(), "--run-id") {
		t.Fatalf("help output missing expected flags:\n%s", stdout.String())
	}
}
