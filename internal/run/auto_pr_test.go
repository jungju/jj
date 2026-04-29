package run

import (
	"context"
	"strings"
	"testing"
)

func TestLocalAutoPRBranchPlanUsesIntentSlugAndHash(t *testing.T) {
	intent := "# Web UI About page feature\n\nAcceptance: pass validation\n"
	proposal := TaskProposalResolution{Resolved: TaskProposalModeFeature}

	plan := localAutoPRBranchPlan(TaskState{}, TaskRecord{}, false, intent, proposal)

	hash := shortHash(sanitizeHandoffText(intent))
	if plan.NextIntentHash != hash {
		t.Fatalf("unexpected intent hash: %#v", plan)
	}
	if plan.Branch != "jj/intent-web-ui-about-"+hash {
		t.Fatalf("unexpected branch: %q", plan.Branch)
	}
}

func TestLocalAutoPRBranchPlanReusesExistingTaskBranch(t *testing.T) {
	existing := TaskRecord{
		ID:             "TASK-0102",
		Title:          "Old intent",
		WorkBranch:     "jj/intent-old-12345678",
		NextIntentHash: "12345678",
	}

	plan := localAutoPRBranchPlan(TaskState{}, existing, true, "New intent", TaskProposalResolution{Resolved: TaskProposalModeFeature})

	if plan.Branch != existing.WorkBranch || plan.NextIntentHash != existing.NextIntentHash {
		t.Fatalf("existing task branch should win, got %#v", plan)
	}
}

func TestAutoPRTitleAndBodyUseIntentWithoutHash(t *testing.T) {
	secret := "autopr-secret-value"
	intent := "## Web UI About page feature\n\nAuthorization: Bearer " + secret + "\n"
	hash := shortHash(intent)
	task := TaskRecord{ID: "TASK-0102", Title: "Fallback title"}

	title := prTitleFromIntent(intent, task.Title)
	body := prBodyFromIntent(intent, task, "run-1", "passed", "abc123")

	if title != "Web UI About page feature" {
		t.Fatalf("unexpected title: %q", title)
	}
	for _, text := range []string{title, body} {
		if strings.Contains(text, hash) || strings.Contains(text, secret) || strings.Contains(text, "jj/intent-") {
			t.Fatalf("PR text should not contain hash, branch, or secret:\n%s", text)
		}
	}
	if !strings.Contains(body, "Web UI About page feature") || !strings.Contains(body, "TASK-0102 Fallback title") || !strings.Contains(body, "Validation: passed") {
		t.Fatalf("PR body missing expected metadata:\n%s", body)
	}
}

func TestAutoPRTitleSkipsAcceptanceAndFallsBack(t *testing.T) {
	title := prTitleFromIntent("Acceptance: validation passes\n", "Selected task title")
	if title != "Selected task title" {
		t.Fatalf("expected fallback title, got %q", title)
	}
}

func TestEnsureAutoPRPullRequestReusesExisting(t *testing.T) {
	client := &fakeGitHubPRClient{existing: &GitHubPullRequest{Number: 7, URL: "https://github.com/acme/app/pull/7", Title: "Existing PR"}}
	runtime := &autoPRRuntime{
		Client:        client,
		Owner:         "acme",
		Repo:          "app",
		BaseBranch:    "main",
		WorkBranch:    "jj/intent-web-ui-12345678",
		IntentContent: "Web UI About page",
	}

	pr, status, err := ensureAutoPRPullRequest(context.Background(), runtime, TaskRecord{ID: "TASK-0102", Title: "Fallback"}, "run-1", "passed", "abc123")
	if err != nil {
		t.Fatalf("ensure PR: %v", err)
	}
	if status != "existing" || pr.Number != 7 || client.createCalls != 0 {
		t.Fatalf("expected existing PR reuse, status=%s pr=%#v createCalls=%d", status, pr, client.createCalls)
	}
}

type fakeGitHubPRClient struct {
	existing    *GitHubPullRequest
	findReq     GitHubPullRequestRequest
	createReq   GitHubPullRequestRequest
	findCalls   int
	createCalls int
	err         error
}

func (f *fakeGitHubPRClient) FindOpenPullRequest(_ context.Context, req GitHubPullRequestRequest) (GitHubPullRequest, bool, error) {
	f.findCalls++
	f.findReq = req
	if f.err != nil {
		return GitHubPullRequest{}, false, f.err
	}
	if f.existing != nil {
		return *f.existing, true, nil
	}
	return GitHubPullRequest{}, false, nil
}

func (f *fakeGitHubPRClient) CreatePullRequest(_ context.Context, req GitHubPullRequestRequest) (GitHubPullRequest, error) {
	f.createCalls++
	f.createReq = req
	if f.err != nil {
		return GitHubPullRequest{}, f.err
	}
	return GitHubPullRequest{Number: 12, URL: "https://github.com/acme/app/pull/12", Title: req.Title}, nil
}
