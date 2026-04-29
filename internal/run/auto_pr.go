package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
)

const defaultGitHubAPIBaseURL = "https://api.github.com"

type GitHubPRClient interface {
	FindOpenPullRequest(context.Context, GitHubPullRequestRequest) (GitHubPullRequest, bool, error)
	CreatePullRequest(context.Context, GitHubPullRequestRequest) (GitHubPullRequest, error)
}

type GitHubPullRequestRequest struct {
	Owner string
	Repo  string
	Base  string
	Head  string
	Title string
	Body  string
}

type GitHubPullRequest struct {
	Number int
	URL    string
	Title  string
}

type githubRESTClient struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

type autoPRRuntime struct {
	Manifest       ManifestRepository
	Token          string
	Client         GitHubPRClient
	Owner          string
	Repo           string
	BaseBranch     string
	WorkBranch     string
	IntentContent  string
	NextIntentHash string
	Events         []repositoryEvent
}

type autoPRBranchPlan struct {
	Branch         string
	NextIntentHash string
}

func prepareLocalAutoPRWorkspace(ctx context.Context, cfg Config, gitState GitState, tasks TaskState, existing TaskRecord, useExisting bool, nextIntent string, proposal TaskProposalResolution) (*autoPRRuntime, GitState, error) {
	if !cfg.AutoPR || cfg.DryRun || strings.TrimSpace(cfg.RepoURL) != "" {
		return nil, gitState, nil
	}
	if !gitState.Available {
		return nil, gitState, errors.New("auto-pr requires a git repository")
	}
	baseBranch := strings.TrimSpace(cfg.BaseBranch)
	if baseBranch == "" {
		baseBranch = detectDefaultBranch(ctx, gitState.Root)
	}
	if strings.TrimSpace(gitState.Branch) != baseBranch {
		return nil, gitState, nil
	}
	if HasNonJJDirtyStatus(gitState.InitialStatus) {
		return nil, gitState, errors.New("auto-pr requires a clean workspace before branching")
	}
	remote, err := runGitCommand(ctx, cfg.CWD, nil, "remote", "get-url", "origin")
	if err != nil {
		return nil, gitState, fmt.Errorf("auto-pr requires an origin remote: %w", err)
	}
	owner, repo, sanitizedRemote, err := githubRepositoryFromRemote(remote)
	if err != nil {
		return nil, gitState, err
	}
	tokenEnvOverride := ""
	if cfg.GitHubTokenEnvExplicit {
		tokenEnvOverride = cfg.GitHubTokenEnv
	}
	token, tokenEnv, tokenPresent, err := ResolveGitHubToken(tokenEnvOverride, true)
	if err != nil {
		return nil, gitState, err
	}

	branchPlan := localAutoPRBranchPlan(tasks, existing, useExisting, nextIntent, proposal)
	if err := validateBranchName("auto-pr work branch", branchPlan.Branch); err != nil {
		return nil, gitState, err
	}
	finalBranch, created, err := checkoutWorkBranch(ctx, cfg.CWD, baseBranch, branchPlan.Branch)
	if err != nil {
		return nil, gitState, err
	}
	runtime := &autoPRRuntime{
		Token:          token,
		Client:         cfg.GitHubClient,
		Owner:          owner,
		Repo:           repo,
		BaseBranch:     baseBranch,
		WorkBranch:     finalBranch,
		IntentContent:  nextIntent,
		NextIntentHash: branchPlan.NextIntentHash,
		Events: []repositoryEvent{{
			Type: "github.token.resolved",
			Fields: map[string]string{
				"env":     tokenEnv,
				"present": fmt.Sprintf("%t", tokenPresent),
			},
		}, {
			Type: "github.branch.auto.selected",
			Fields: map[string]string{
				"base_branch": baseBranch,
				"work_branch": finalBranch,
			},
		}},
	}
	if runtime.Client == nil {
		runtime.Client = githubRESTClient{Token: token}
	}
	if created {
		runtime.Events = append(runtime.Events, repositoryEvent{
			Type:   "github.branch.created",
			Fields: map[string]string{"branch": finalBranch},
		})
	}
	runtime.Events = append(runtime.Events, repositoryEvent{
		Type:   "github.branch.checked_out",
		Fields: map[string]string{"branch": finalBranch},
	})
	updatedGitState, err := InspectGit(ctx, cfg.CWD, cfg.GitRunner)
	if err != nil {
		return nil, gitState, err
	}
	runtime.Manifest = ManifestRepository{
		Enabled:          true,
		Provider:         "github",
		RepoURL:          sanitizedRemote,
		SanitizedRepoURL: sanitizedRemote,
		RepoDir:          cfg.CWD,
		BaseBranch:       baseBranch,
		WorkBranch:       finalBranch,
		PushEnabled:      true,
		PushMode:         PushModeBranch,
		PushStatus:       "not_pushed",
		PREnabled:        true,
		PRStatus:         "not_created",
		Remote:           "origin",
		HeadBefore:       gitState.Head,
		HeadAfter:        updatedGitState.Head,
	}
	return runtime, updatedGitState, nil
}

func localAutoPRBranchPlan(tasks TaskState, existing TaskRecord, useExisting bool, nextIntent string, proposal TaskProposalResolution) autoPRBranchPlan {
	if useExisting && strings.TrimSpace(existing.WorkBranch) != "" {
		return autoPRBranchPlan{Branch: strings.TrimSpace(existing.WorkBranch), NextIntentHash: strings.TrimSpace(existing.NextIntentHash)}
	}
	if strings.TrimSpace(nextIntent) != "" {
		hash := shortHash(sanitizeHandoffText(nextIntent))
		title := prTitleFromIntent(nextIntent, TaskProposalTaskTitle(proposal.Resolved))
		return autoPRBranchPlan{
			Branch:         "jj/intent-" + branchSlug(title) + "-" + hash,
			NextIntentHash: hash,
		}
	}
	taskID := ""
	title := ""
	if useExisting {
		taskID = existing.ID
		title = existing.Title
	} else {
		taskID = nextTaskID(tasks, proposal.Resolved)
		title = TaskProposalTaskTitle(proposal.Resolved)
	}
	identity := strings.TrimSpace(taskID + " " + title)
	return autoPRBranchPlan{
		Branch: "jj/task-" + branchSlug(taskID) + "-" + shortHash(identity),
	}
}

func applyTaskWorkstreamMetadata(state TaskState, selected TaskRecord, autoPR *autoPRRuntime) (TaskState, TaskRecord) {
	if autoPR == nil || strings.TrimSpace(autoPR.WorkBranch) == "" || strings.TrimSpace(selected.ID) == "" {
		return state, selected
	}
	idx := taskIndexByID(state, selected.ID)
	if idx < 0 {
		return state, selected
	}
	state.Tasks[idx].WorkBranch = autoPR.WorkBranch
	if strings.TrimSpace(autoPR.NextIntentHash) != "" {
		state.Tasks[idx].NextIntentHash = autoPR.NextIntentHash
	}
	redacted := redactTaskState(state)
	return redacted, taskByID(redacted, selected.ID, TaskProposalResolution{})
}

func ensureAutoPRPullRequest(ctx context.Context, runtime *autoPRRuntime, selected TaskRecord, runID, validationStatus, commitSHA string) (GitHubPullRequest, string, error) {
	if runtime == nil {
		return GitHubPullRequest{}, "skipped", nil
	}
	title := prTitleFromIntent(runtime.IntentContent, selected.Title)
	body := prBodyFromIntent(runtime.IntentContent, selected, runID, validationStatus, commitSHA)
	req := GitHubPullRequestRequest{
		Owner: runtime.Owner,
		Repo:  runtime.Repo,
		Base:  runtime.BaseBranch,
		Head:  runtime.WorkBranch,
		Title: title,
		Body:  body,
	}
	if existing, ok, err := runtime.Client.FindOpenPullRequest(ctx, req); err != nil {
		return GitHubPullRequest{}, "failed", err
	} else if ok {
		return existing, "existing", nil
	}
	created, err := runtime.Client.CreatePullRequest(ctx, req)
	if err != nil {
		return GitHubPullRequest{}, "failed", err
	}
	return created, "created", nil
}

func prTitleFromIntent(intent, fallback string) string {
	for _, line := range strings.Split(sanitizeHandoffText(intent), "\n") {
		candidate := cleanIntentTitleLine(line)
		if candidate != "" {
			return truncateRunes(candidate, 90)
		}
	}
	fallback = cleanCommitText(fallback)
	if fallback == "" {
		fallback = "jj run update"
	}
	return truncateRunes(fallback, 90)
}

func prBodyFromIntent(intent string, selected TaskRecord, runID, validationStatus, commitSHA string) string {
	intent = strings.TrimSpace(sanitizeHandoffText(intent))
	if intent == "" {
		intent = strings.TrimSpace(sanitizeHandoffText(selected.Title))
	}
	var b strings.Builder
	b.WriteString("## Intent\n\n")
	b.WriteString(intent)
	b.WriteString("\n\n## jj Run\n\n")
	b.WriteString("- Task: ")
	b.WriteString(cleanCommitText(strings.TrimSpace(selected.ID + " " + selected.Title)))
	b.WriteByte('\n')
	b.WriteString("- Run: ")
	b.WriteString(cleanCommitText(runID))
	b.WriteByte('\n')
	b.WriteString("- Validation: ")
	b.WriteString(cleanCommitText(validationStatus))
	b.WriteByte('\n')
	if strings.TrimSpace(commitSHA) != "" {
		b.WriteString("- Commit: ")
		b.WriteString(cleanCommitText(commitSHA))
		b.WriteByte('\n')
	}
	return sanitizeHandoffText(b.String())
}

func cleanIntentTitleLine(line string) string {
	line = strings.TrimSpace(sanitizeHandoffText(line))
	line = strings.TrimLeft(line, "#>-*+ \t")
	line = regexp.MustCompile(`^\d+[\.)]\s+`).ReplaceAllString(line, "")
	line = strings.TrimSpace(strings.Trim(line, "`*_ "))
	if line == "" {
		return ""
	}
	label := strings.ToLower(line)
	for _, prefix := range []string{"acceptance:", "acceptance criteria:", "criteria:", "test:", "tests:", "validation:"} {
		if strings.HasPrefix(label, prefix) {
			return ""
		}
	}
	return strings.Join(strings.Fields(line), " ")
}

func branchSlug(value string) string {
	value = strings.ToLower(sanitizeHandoffText(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 12 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "work"
	}
	return slug
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(sanitizeHandoffText(value)))
	return hex.EncodeToString(sum[:])[:8]
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func githubRepositoryFromRemote(remote string) (owner, repo, sanitized string, err error) {
	sanitized, _ = SanitizeRepositoryURL(strings.TrimSpace(remote))
	trimmed := strings.TrimSuffix(strings.TrimSpace(sanitized), ".git")
	if strings.HasPrefix(trimmed, "git@github.com:") {
		parts := strings.Split(strings.TrimPrefix(trimmed, "git@github.com:"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], sanitized, nil
		}
	}
	u, parseErr := url.Parse(trimmed)
	if parseErr == nil && strings.EqualFold(u.Hostname(), "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], sanitized, nil
		}
	}
	return "", "", sanitized, errors.New("auto-pr requires a GitHub origin remote")
}

func (c githubRESTClient) FindOpenPullRequest(ctx context.Context, req GitHubPullRequestRequest) (GitHubPullRequest, bool, error) {
	endpoint, err := c.endpoint("/repos/" + path.Join(req.Owner, req.Repo, "pulls"))
	if err != nil {
		return GitHubPullRequest{}, false, err
	}
	q := endpoint.Query()
	q.Set("state", "open")
	q.Set("head", req.Owner+":"+req.Head)
	q.Set("base", req.Base)
	endpoint.RawQuery = q.Encode()
	httpReq, err := c.newRequest(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return GitHubPullRequest{}, false, err
	}
	var prs []githubPullResponse
	if err := c.do(httpReq, &prs); err != nil {
		return GitHubPullRequest{}, false, err
	}
	if len(prs) == 0 {
		return GitHubPullRequest{}, false, nil
	}
	return prs[0].pullRequest(), true, nil
}

func (c githubRESTClient) CreatePullRequest(ctx context.Context, req GitHubPullRequestRequest) (GitHubPullRequest, error) {
	endpoint, err := c.endpoint("/repos/" + path.Join(req.Owner, req.Repo, "pulls"))
	if err != nil {
		return GitHubPullRequest{}, err
	}
	body, err := json.Marshal(map[string]string{
		"title": req.Title,
		"head":  req.Head,
		"base":  req.Base,
		"body":  req.Body,
	})
	if err != nil {
		return GitHubPullRequest{}, err
	}
	httpReq, err := c.newRequest(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return GitHubPullRequest{}, err
	}
	var pr githubPullResponse
	if err := c.do(httpReq, &pr); err != nil {
		return GitHubPullRequest{}, err
	}
	return pr.pullRequest(), nil
}

func (c githubRESTClient) endpoint(rel string) (*url.URL, error) {
	base := strings.TrimSpace(c.BaseURL)
	if base == "" {
		base = defaultGitHubAPIBaseURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + rel
	return u, nil
}

func (c githubRESTClient) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(commandContext(ctx), method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(c.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

func (c githubRESTClient) do(req *http.Request, target any) error {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub API request failed: status %d: %s", resp.StatusCode, sanitizeHandoffText(string(data)))
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}

type githubPullResponse struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
}

func (p githubPullResponse) pullRequest() GitHubPullRequest {
	return GitHubPullRequest{
		Number: p.Number,
		URL:    sanitizeHandoffText(p.HTMLURL),
		Title:  sanitizeHandoffText(p.Title),
	}
}
