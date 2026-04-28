package run

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jungju/jj/internal/codex"
)

func TestResolveGitHubTokenPreferenceAndExplicitEnv(t *testing.T) {
	t.Setenv("JJ_GITHUB_TOKEN", "jj-token")
	t.Setenv("GITHUB_TOKEN", "github-token")
	t.Setenv("GH_TOKEN", "gh-token")

	token, envName, present, err := ResolveGitHubToken("", true)
	if err != nil {
		t.Fatalf("resolve token: %v", err)
	}
	if token != "jj-token" || envName != "JJ_GITHUB_TOKEN" || !present {
		t.Fatalf("unexpected default token resolution: token=%q env=%q present=%t", token, envName, present)
	}

	t.Setenv("MY_GITHUB_TOKEN", "custom-token")
	token, envName, present, err = ResolveGitHubToken("MY_GITHUB_TOKEN", true)
	if err != nil {
		t.Fatalf("resolve explicit token: %v", err)
	}
	if token != "custom-token" || envName != "MY_GITHUB_TOKEN" || !present {
		t.Fatalf("unexpected explicit token resolution: token=%q env=%q present=%t", token, envName, present)
	}

	_, _, _, err = ResolveGitHubToken("MISSING_GITHUB_TOKEN", true)
	if err == nil || !strings.Contains(err.Error(), "GitHub token not found. Set MISSING_GITHUB_TOKEN.") {
		t.Fatalf("expected explicit missing token error, got %v", err)
	}
}

func TestSanitizeRepositoryURLRemovesCredentials(t *testing.T) {
	for _, input := range []string{
		"https://token@github.com/org/repo.git",
		"https://user:token@github.com/org/repo.git",
	} {
		got, err := SanitizeRepositoryURL(input)
		if err != nil {
			t.Fatalf("sanitize %q: %v", input, err)
		}
		if got != "https://github.com/org/repo.git" {
			t.Fatalf("unexpected sanitized URL for %q: %q", input, got)
		}
	}
}

func TestCommitSubjectUsesSelectedTaskTitle(t *testing.T) {
	proposal := TaskProposalResolution{
		Selected:       TaskProposalModeAuto,
		Resolved:       TaskProposalModeFeature,
		SelectedTaskID: "TASK-0001",
	}

	got := commitSubject(proposal, TaskRecord{ID: "TASK-0009", Title: "Refresh dashboard controls"})
	if got != "jj: TASK-0009 Refresh dashboard controls" {
		t.Fatalf("unexpected selected task subject: %q", got)
	}

	got = commitSubject(proposal, TaskRecord{ID: "TASK-0009", Title: "Refresh\tdashboard\ncontrols"})
	if got != "jj: TASK-0009 Refresh dashboard controls" {
		t.Fatalf("subject should collapse whitespace: %q", got)
	}

	got = commitSubject(proposal, TaskRecord{ID: "TASK-0009"})
	if got != "jj: TASK-0009 Add the next useful user-facing capability" {
		t.Fatalf("blank title should fall back to mode title: %q", got)
	}

	secret := "sk-proj-commitsubjectsecret1234567890"
	got = commitSubject(proposal, TaskRecord{ID: "TASK-0009", Title: "Use " + secret})
	if strings.Contains(got, secret) || !strings.Contains(got, "[jj-omitted]") {
		t.Fatalf("subject should redact secrets: %q", got)
	}
}

func TestExecuteRepositoryWorkflowClonesBranchesAndCommitsWithoutPush(t *testing.T) {
	origin := newBareRepository(t)
	dir := t.TempDir()
	planDir := t.TempDir()
	chdirForRepositoryTest(t, planDir)
	writePlan(t, planDir, "plan.md")
	repoDir := filepath.Join(dir, "clone")
	secret := "ghp_repositorysecret1234567890"
	setGitCommitterEnv(t)
	t.Setenv("JJ_GITHUB_TOKEN", secret)
	codexRunner := &fakeCodexRunner{mutate: true}
	planner := &fakePlanner{mergeTask: plannedTaskJSON("Refresh repository dashboard")}

	_, err := Execute(context.Background(), Config{
		PlanPath:        filepath.Join(planDir, "plan.md"),
		RepoURL:         origin,
		RepoURLExplicit: true,
		RepoDir:         repoDir,
		RepoDirExplicit: true,
		RunID:           "repo-run",
		PlanningAgents:  1,
		OpenAIModel:     "test-model",
		Stdout:          io.Discard,
		Planner:         planner,
		CodexRunner:     codexRunner,
	})
	if err != nil {
		t.Fatalf("execute repository workflow: %v", err)
	}
	if !codexRunner.called || codexRunner.lastRequest.CWD != repoDir {
		t.Fatalf("codex runner should execute in repo root, got %#v", codexRunner.lastRequest)
	}
	if branch := strings.TrimSpace(runGitOutput(t, repoDir, "branch", "--show-current")); branch != "jj/run-repo-run" {
		t.Fatalf("unexpected current branch: %q", branch)
	}
	if subject := strings.TrimSpace(runGitOutput(t, repoDir, "log", "-1", "--pretty=%s")); subject != "jj: TASK-0001 Refresh repository dashboard" {
		t.Fatalf("unexpected commit subject: %q", subject)
	}
	if gitRefExists(t, origin, "refs/heads/jj/run-repo-run") {
		t.Fatalf("work branch should not be pushed by default")
	}

	runDir := filepath.Join(repoDir, ".jj", "runs", "repo-run")
	manifest := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if !manifest.Repository.Enabled || manifest.Repository.Provider != "github" {
		t.Fatalf("repository metadata missing: %#v", manifest.Repository)
	}
	if manifest.Repository.RepoURL != "[path]" || manifest.Repository.SanitizedRepoURL != "[path]" || manifest.Repository.RepoDir != "[workspace]" || manifest.Repository.BaseBranch != "main" || manifest.Repository.WorkBranch != "jj/run-repo-run" {
		t.Fatalf("unexpected repository metadata: %#v", manifest.Repository)
	}
	if manifest.Repository.PushEnabled || manifest.Repository.PushStatus != "not_pushed" || manifest.Repository.Pushed || manifest.Repository.PushedRef != "" {
		t.Fatalf("unexpected push metadata: %#v", manifest.Repository)
	}
	if manifest.Commit.Status != "success" || manifest.Commit.SHA == "" {
		t.Fatalf("expected successful repository commit, got %#v", manifest.Commit)
	}
	events := readFile(t, filepath.Join(runDir, "events.jsonl"))
	for _, want := range []string{
		"github.token.resolved",
		"github.repo.clone.started",
		"github.repo.clone.completed",
		"github.repo.fetch.started",
		"github.repo.fetch.completed",
		"github.repo.checkout.started",
		"github.repo.checkout.completed",
		"github.branch.created",
		"github.branch.checked_out",
	} {
		if !strings.Contains(events, want) {
			t.Fatalf("events missing %q:\n%s", want, events)
		}
	}
	if strings.Contains(events, secret) || strings.Contains(codexRunner.lastRequest.Prompt, secret) {
		t.Fatalf("token leaked into events or codex prompt")
	}
	for _, forbidden := range []string{secret, origin, filepath.ToSlash(origin), repoDir, filepath.ToSlash(repoDir), planDir, filepath.ToSlash(planDir)} {
		assertTreeDoesNotContain(t, runDir, forbidden)
	}
	config := runGitOutput(t, repoDir, "config", "--get", "remote.origin.url")
	if strings.Contains(config, secret) || strings.Contains(config, "@github.com") {
		t.Fatalf("origin URL should be sanitized, got %q", config)
	}
}

func TestExecuteRepositoryWorkflowPushesBranch(t *testing.T) {
	origin := newBareRepository(t)
	planDir := t.TempDir()
	chdirForRepositoryTest(t, planDir)
	writePlan(t, planDir, "plan.md")
	repoDir := filepath.Join(t.TempDir(), "clone")
	setGitCommitterEnv(t)

	_, err := Execute(context.Background(), Config{
		PlanPath:        filepath.Join(planDir, "plan.md"),
		RepoURL:         origin,
		RepoURLExplicit: true,
		RepoDir:         repoDir,
		RepoDirExplicit: true,
		RunID:           "repo-push",
		PlanningAgents:  1,
		OpenAIModel:     "test-model",
		Push:            true,
		PushExplicit:    true,
		PushMode:        PushModeBranch,
		Stdout:          io.Discard,
		Planner:         &fakePlanner{},
		CodexRunner:     &fakeCodexRunner{mutate: true},
	})
	if err != nil {
		t.Fatalf("execute repository push workflow: %v", err)
	}
	if !gitRefExists(t, origin, "refs/heads/jj/run-repo-push") {
		t.Fatalf("expected pushed work branch in origin")
	}
	manifest := readManifest(t, filepath.Join(repoDir, ".jj", "runs", "repo-push", "manifest.json"))
	if !manifest.Repository.PushEnabled || !manifest.Repository.Pushed || manifest.Repository.PushStatus != "pushed" || manifest.Repository.PushedRef != "origin/jj/run-repo-push" {
		t.Fatalf("unexpected push metadata: %#v", manifest.Repository)
	}
	events := readFile(t, filepath.Join(repoDir, ".jj", "runs", "repo-push", "events.jsonl"))
	if !strings.Contains(events, "github.push.started") || !strings.Contains(events, "github.push.completed") {
		t.Fatalf("push events missing:\n%s", events)
	}
}

func TestExecuteRepositoryWorkflowPushFailureIsRecorded(t *testing.T) {
	origin := newBareRepository(t)
	planDir := t.TempDir()
	chdirForRepositoryTest(t, planDir)
	writePlan(t, planDir, "plan.md")
	repoDir := filepath.Join(t.TempDir(), "clone")
	setGitCommitterEnv(t)

	_, err := Execute(context.Background(), Config{
		PlanPath:        filepath.Join(planDir, "plan.md"),
		RepoURL:         origin,
		RepoURLExplicit: true,
		RepoDir:         repoDir,
		RepoDirExplicit: true,
		RunID:           "repo-push-fail",
		PlanningAgents:  1,
		OpenAIModel:     "test-model",
		Push:            true,
		PushExplicit:    true,
		PushMode:        PushModeBranch,
		Stdout:          io.Discard,
		Planner:         &fakePlanner{},
		CodexRunner:     &remoteBreakingCodexRunner{mutate: true},
	})
	if err == nil || !strings.Contains(err.Error(), "push failed for branch jj/run-repo-push-fail") {
		t.Fatalf("expected push failure error, got %v", err)
	}
	if gitRefExists(t, origin, "refs/heads/jj/run-repo-push-fail") {
		t.Fatalf("failed push should not create origin work branch")
	}
	manifest := readManifest(t, filepath.Join(repoDir, ".jj", "runs", "repo-push-fail", "manifest.json"))
	if manifest.Commit.Status != "success" || manifest.Commit.SHA == "" {
		t.Fatalf("local commit should remain after push failure, got %#v", manifest.Commit)
	}
	if manifest.Repository.PushStatus != "failed" || manifest.Repository.Pushed || manifest.Repository.Error == "" {
		t.Fatalf("push failure metadata missing: %#v", manifest.Repository)
	}
	events := readFile(t, filepath.Join(repoDir, ".jj", "runs", "repo-push-fail", "events.jsonl"))
	if !strings.Contains(events, "github.push.failed") {
		t.Fatalf("push failure event missing:\n%s", events)
	}
}

func TestExecuteRepositoryWorkflowMismatchedOriginFails(t *testing.T) {
	originA := newBareRepository(t)
	originB := newBareRepository(t)
	repoDir := filepath.Join(t.TempDir(), "clone")
	runGitClone(t, originA, repoDir)
	planDir := t.TempDir()
	chdirForRepositoryTest(t, planDir)
	writePlan(t, planDir, "plan.md")

	_, err := Execute(context.Background(), Config{
		PlanPath:        filepath.Join(planDir, "plan.md"),
		RepoURL:         originB,
		RepoURLExplicit: true,
		RepoDir:         repoDir,
		RepoDirExplicit: true,
		RunID:           "repo-mismatch",
		PlanningAgents:  1,
		OpenAIModel:     "test-model",
		Stdout:          io.Discard,
		Planner:         &fakePlanner{},
		CodexRunner:     &fakeCodexRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "origin URL does not match requested repository") {
		t.Fatalf("expected mismatched origin error, got %v", err)
	}
}

func TestExecuteRepositoryWorkflowMissingGitHubTokenFailsClearly(t *testing.T) {
	t.Setenv("JJ_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	_, err := Execute(context.Background(), Config{
		PlanText:        "Build a thing.",
		PlanInputName:   "web prompt",
		RepoURL:         "https://github.com/acme/private.git",
		RepoURLExplicit: true,
		RepoDir:         filepath.Join(t.TempDir(), "clone"),
		RepoDirExplicit: true,
		RunID:           "repo-missing-token",
		PlanningAgents:  1,
		OpenAIModel:     "test-model",
		Stdout:          io.Discard,
		Planner:         &fakePlanner{},
		CodexRunner:     &fakeCodexRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "GitHub token not found. Set JJ_GITHUB_TOKEN, GITHUB_TOKEN, or GH_TOKEN.") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func newBareRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	work := t.TempDir()
	runGit(t, work, "init")
	runGit(t, work, "checkout", "-b", "main")
	runGit(t, work, "config", "user.email", "jj@example.com")
	runGit(t, work, "config", "user.name", "jj test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# Test Repository\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(work, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "scripts", "validate.sh"), []byte("#!/bin/sh\nset -eu\nprintf 'ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("write validate.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte(".jj/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGit(t, work, "add", "README.md", "scripts/validate.sh", ".gitignore")
	runGit(t, work, "commit", "-m", "initial")

	origin := filepath.Join(t.TempDir(), "origin.git")
	runGitClone(t, "--bare", work, origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	return origin
}

func runGitClone(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"clone"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone %v failed: %v\n%s", args, err, out)
	}
}

func gitRefExists(t *testing.T, repoDir, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

func chdirForRepositoryTest(t *testing.T, dir string) {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func setGitCommitterEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "jj test")
	t.Setenv("GIT_AUTHOR_EMAIL", "jj@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "jj test")
	t.Setenv("GIT_COMMITTER_EMAIL", "jj@example.com")
}

type remoteBreakingCodexRunner struct {
	mutate bool
}

func (f *remoteBreakingCodexRunner) Run(ctx context.Context, req codex.Request) (codex.Result, error) {
	if _, err := runGitCommand(ctx, req.CWD, nil, "remote", "set-url", "origin", filepath.Join(req.CWD, "missing-origin.git")); err != nil {
		return codex.Result{}, err
	}
	return (&fakeCodexRunner{mutate: f.mutate}).Run(ctx, req)
}
