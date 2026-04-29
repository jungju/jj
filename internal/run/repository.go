package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jungju/jj/internal/security"
)

const (
	DefaultGitHubTokenEnv = "JJ_GITHUB_TOKEN"
	DefaultPushMode       = "branch"

	PushModeBranch        = "branch"
	PushModeCurrentBranch = "current-branch"
	PushModeNone          = "none"
)

type ManifestRepository struct {
	Enabled          bool   `json:"enabled"`
	Provider         string `json:"provider,omitempty"`
	RepoURL          string `json:"repo_url,omitempty"`
	SanitizedRepoURL string `json:"sanitized_repo_url,omitempty"`
	RepoDir          string `json:"repo_dir,omitempty"`
	BaseBranch       string `json:"base_branch,omitempty"`
	WorkBranch       string `json:"work_branch,omitempty"`
	PushEnabled      bool   `json:"push_enabled"`
	PushMode         string `json:"push_mode,omitempty"`
	Pushed           bool   `json:"pushed"`
	PushStatus       string `json:"push_status,omitempty"`
	PushedRef        string `json:"pushed_ref,omitempty"`
	PREnabled        bool   `json:"pr_enabled,omitempty"`
	PRStatus         string `json:"pr_status,omitempty"`
	PRNumber         int    `json:"pr_number,omitempty"`
	PRURL            string `json:"pr_url,omitempty"`
	PRTitle          string `json:"pr_title,omitempty"`
	Remote           string `json:"remote,omitempty"`
	HeadBefore       string `json:"head_before,omitempty"`
	HeadAfter        string `json:"head_after,omitempty"`
	Error            string `json:"error,omitempty"`
}

type repositoryRuntime struct {
	Manifest ManifestRepository
	Token    string
	TokenEnv string
	Events   []repositoryEvent
}

type repositoryEvent struct {
	Type   string
	Fields map[string]string
}

type gitAuth struct {
	token   string
	tempDir string
	askPass string
}

var branchUnsafePattern = regexp.MustCompile(`[\x00-\x20~^:?*\[\\]`)

func prepareRepositoryWorkspace(ctx context.Context, cfg Config) (Config, *repositoryRuntime, error) {
	rawRepo := strings.TrimSpace(cfg.RepoURL)
	if rawRepo == "" {
		return cfg, nil, nil
	}
	sanitizedRepo, err := SanitizeRepositoryURL(rawRepo)
	if err != nil {
		return cfg, nil, err
	}
	tokenEnvOverride := ""
	if cfg.GitHubTokenEnvExplicit {
		tokenEnvOverride = cfg.GitHubTokenEnv
	}
	token, tokenEnv, tokenPresent, err := ResolveGitHubToken(tokenEnvOverride, isGitHubHTTPSURL(sanitizedRepo))
	if err != nil {
		return cfg, nil, err
	}
	runtime := &repositoryRuntime{
		Token:    token,
		TokenEnv: tokenEnv,
		Events: []repositoryEvent{{
			Type: "github.token.resolved",
			Fields: map[string]string{
				"env":     tokenEnv,
				"present": fmt.Sprintf("%t", tokenPresent),
			},
		}},
	}
	auth, err := newGitAuth(token)
	if err != nil {
		return cfg, nil, err
	}
	if auth != nil {
		defer auth.cleanup()
	}

	repoDir, err := resolveRepositoryDir(cfg, sanitizedRepo)
	if err != nil {
		return cfg, nil, err
	}
	baseBranch := strings.TrimSpace(cfg.BaseBranch)
	pushMode := strings.TrimSpace(cfg.PushMode)
	if pushMode == "" {
		pushMode = DefaultPushMode
	}
	if err := validatePushMode(pushMode); err != nil {
		return cfg, nil, err
	}

	if _, err := os.Stat(repoDir); errors.Is(err, os.ErrNotExist) {
		runtime.Events = append(runtime.Events, repositoryEvent{
			Type:   "github.repo.clone.started",
			Fields: map[string]string{"repo_url": sanitizedRepo, "repo_dir": repoDir},
		})
		if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
			return cfg, nil, fmt.Errorf("create repo parent: %w", err)
		}
		if _, err := runGitCommand(ctx, filepath.Dir(repoDir), auth, "clone", sanitizedRepo, repoDir); err != nil {
			return cfg, nil, fmt.Errorf("clone repository %s: %w", sanitizedRepo, err)
		}
		runtime.Events = append(runtime.Events, repositoryEvent{
			Type:   "github.repo.clone.completed",
			Fields: map[string]string{"repo_url": sanitizedRepo, "repo_dir": repoDir},
		})
	} else if err != nil {
		return cfg, nil, fmt.Errorf("stat repo dir: %w", err)
	}

	if err := verifyGitRepository(ctx, repoDir); err != nil {
		return cfg, nil, err
	}
	if err := ensureOriginMatches(ctx, repoDir, sanitizedRepo); err != nil {
		return cfg, nil, err
	}
	if !cfg.RepoAllowDirty {
		status, err := runGitCommand(ctx, repoDir, nil, "status", "--short")
		if err != nil {
			return cfg, nil, fmt.Errorf("check repository status: %w", err)
		}
		if HasNonJJDirtyStatus(status) {
			return cfg, nil, errors.New("repo workspace is dirty; commit, stash, or pass --allow-dirty")
		}
	}

	runtime.Events = append(runtime.Events, repositoryEvent{
		Type:   "github.repo.fetch.started",
		Fields: map[string]string{"repo_url": sanitizedRepo},
	})
	if _, err := runGitCommand(ctx, repoDir, auth, "fetch", "--prune", "origin"); err != nil {
		return cfg, nil, fmt.Errorf("fetch origin: %w", err)
	}
	runtime.Events = append(runtime.Events, repositoryEvent{
		Type:   "github.repo.fetch.completed",
		Fields: map[string]string{"repo_url": sanitizedRepo},
	})

	if baseBranch == "" {
		baseBranch = detectDefaultBranch(ctx, repoDir)
	}
	if err := validateBranchName("base branch", baseBranch); err != nil {
		return cfg, nil, err
	}
	workBranch := strings.TrimSpace(cfg.WorkBranch)
	if workBranch == "" {
		workBranch = "jj/run-" + cfg.RunID
	}
	if err := validateBranchName("work branch", workBranch); err != nil {
		return cfg, nil, err
	}
	if workBranch == baseBranch {
		return cfg, nil, errors.New("work branch must not match base branch")
	}

	runtime.Events = append(runtime.Events, repositoryEvent{
		Type:   "github.repo.checkout.started",
		Fields: map[string]string{"branch": baseBranch},
	})
	if branchExists(ctx, repoDir, baseBranch) {
		if _, err := runGitCommand(ctx, repoDir, nil, "checkout", baseBranch); err != nil {
			return cfg, nil, fmt.Errorf("checkout base branch: %w", err)
		}
		if _, err := runGitCommand(ctx, repoDir, auth, "pull", "--ff-only", "origin", baseBranch); err != nil {
			return cfg, nil, fmt.Errorf("update base branch: %w", err)
		}
	} else {
		if _, err := runGitCommand(ctx, repoDir, nil, "checkout", "-B", baseBranch, "origin/"+baseBranch); err != nil {
			return cfg, nil, fmt.Errorf("checkout base branch from origin: %w", err)
		}
	}
	runtime.Events = append(runtime.Events, repositoryEvent{
		Type:   "github.repo.checkout.completed",
		Fields: map[string]string{"branch": baseBranch},
	})

	finalWorkBranch, created, err := checkoutWorkBranch(ctx, repoDir, baseBranch, workBranch)
	if err != nil {
		return cfg, nil, err
	}
	if created {
		runtime.Events = append(runtime.Events, repositoryEvent{
			Type:   "github.branch.created",
			Fields: map[string]string{"branch": finalWorkBranch},
		})
	}
	runtime.Events = append(runtime.Events, repositoryEvent{
		Type:   "github.branch.checked_out",
		Fields: map[string]string{"branch": finalWorkBranch},
	})
	headBefore := strings.TrimSpace(mustGitOutput(ctx, repoDir, "rev-parse", "HEAD"))

	cfg.CWD = repoDir
	cfg.CWDExplicit = true
	cfg.BaseBranch = baseBranch
	cfg.WorkBranch = finalWorkBranch
	cfg.PushMode = pushMode
	cfg.RepoURL = sanitizedRepo
	cfg.RepoDir = repoDir

	runtime.Manifest = ManifestRepository{
		Enabled:          true,
		Provider:         "github",
		RepoURL:          sanitizedRepo,
		SanitizedRepoURL: sanitizedRepo,
		RepoDir:          repoDir,
		BaseBranch:       baseBranch,
		WorkBranch:       finalWorkBranch,
		PushEnabled:      cfg.Push && pushMode != PushModeNone,
		PushMode:         pushMode,
		PushStatus:       "not_pushed",
		Remote:           "origin",
		HeadBefore:       headBefore,
		HeadAfter:        headBefore,
	}
	return cfg, runtime, nil
}

func ResolveGitHubToken(explicitEnv string, required bool) (token, envName string, present bool, err error) {
	if strings.TrimSpace(explicitEnv) != "" {
		envName = strings.TrimSpace(explicitEnv)
		token = os.Getenv(envName)
		if token == "" && required {
			return "", envName, false, fmt.Errorf("GitHub token not found. Set %s.", envName)
		}
		return token, envName, token != "", nil
	}
	for _, name := range []string{"JJ_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if value := os.Getenv(name); value != "" {
			return value, name, true, nil
		}
	}
	if required {
		return "", DefaultGitHubTokenEnv, false, errors.New("GitHub token not found. Set JJ_GITHUB_TOKEN, GITHUB_TOKEN, or GH_TOKEN.")
	}
	return "", DefaultGitHubTokenEnv, false, nil
}

func SanitizeRepositoryURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("repo URL is required")
	}
	if strings.HasPrefix(raw, "git@") {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw, nil
	}
	u.User = nil
	u.Fragment = ""
	return u.String(), nil
}

func resolveRepositoryDir(cfg Config, sanitizedRepo string) (string, error) {
	dir := strings.TrimSpace(cfg.RepoDir)
	if dir == "" && cfg.CWDExplicit {
		dir = strings.TrimSpace(cfg.CWD)
	}
	if dir == "" {
		base := strings.TrimSpace(cfg.ConfigSearchDir)
		if base == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			base = cwd
		}
		dir = filepath.Join(base, ".jj", "repos", repoSlug(sanitizedRepo))
	}
	return filepath.Abs(dir)
}

func repoSlug(repo string) string {
	sanitized := strings.TrimSuffix(strings.TrimSpace(repo), "/")
	if u, err := url.Parse(sanitized); err == nil && u.Path != "" {
		sanitized = strings.Trim(u.Path, "/")
	}
	sanitized = strings.TrimSuffix(sanitized, ".git")
	sanitized = strings.ReplaceAll(sanitized, "/", "-")
	sanitized = strings.ReplaceAll(sanitized, ":", "-")
	sanitized = strings.ReplaceAll(sanitized, string(filepath.Separator), "-")
	if sanitized == "" || sanitized == "." {
		return "repo"
	}
	return sanitized
}

func isGitHubHTTPSURL(repo string) bool {
	u, err := url.Parse(repo)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && strings.EqualFold(u.Hostname(), "github.com")
}

func validatePushMode(mode string) error {
	switch mode {
	case PushModeBranch, PushModeCurrentBranch, PushModeNone:
		return nil
	default:
		return fmt.Errorf("invalid push mode: %q\nvalid modes: branch, current-branch, none", mode)
	}
}

func validateBranchName(label, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("%s is required", label)
	}
	if strings.HasPrefix(branch, "-") || strings.HasSuffix(branch, ".") ||
		strings.Contains(branch, "..") || strings.Contains(branch, "@{") ||
		strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") ||
		branchUnsafePattern.MatchString(branch) {
		return fmt.Errorf("%s is not a safe git branch name: %s", label, branch)
	}
	return nil
}

func verifyGitRepository(ctx context.Context, repoDir string) error {
	if _, err := runGitCommand(ctx, repoDir, nil, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("repo directory is not a git repository: %w", err)
	}
	return nil
}

func ensureOriginMatches(ctx context.Context, repoDir, sanitizedRepo string) error {
	origin, err := runGitCommand(ctx, repoDir, nil, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("read origin URL: %w", err)
	}
	cleanOrigin, err := SanitizeRepositoryURL(strings.TrimSpace(origin))
	if err != nil {
		return err
	}
	if canonicalRepositoryURL(cleanOrigin) != canonicalRepositoryURL(sanitizedRepo) {
		return errors.New("repo directory already exists but origin URL does not match requested repository")
	}
	if _, err := runGitCommand(ctx, repoDir, nil, "remote", "set-url", "origin", sanitizedRepo); err != nil {
		return fmt.Errorf("sanitize origin URL: %w", err)
	}
	return nil
}

func canonicalRepositoryURL(raw string) string {
	sanitized, _ := SanitizeRepositoryURL(raw)
	if u, err := url.Parse(sanitized); err == nil && u.Scheme != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Path = strings.TrimSuffix(u.Path, ".git")
		return strings.TrimSuffix(u.String(), "/")
	}
	if abs, err := filepath.Abs(sanitized); err == nil {
		return filepath.Clean(abs)
	}
	return strings.TrimSuffix(strings.TrimSpace(sanitized), ".git")
}

func detectDefaultBranch(ctx context.Context, repoDir string) string {
	out, err := runGitCommand(ctx, repoDir, nil, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		branch := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "origin/"))
		if branch != "" {
			return branch
		}
	}
	return "main"
}

func branchExists(ctx context.Context, repoDir, branch string) bool {
	_, err := runGitCommand(ctx, repoDir, nil, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func checkoutWorkBranch(ctx context.Context, repoDir, baseBranch, requested string) (string, bool, error) {
	branch := requested
	for suffix := 1; ; suffix++ {
		if !branchExists(ctx, repoDir, branch) {
			if _, err := runGitCommand(ctx, repoDir, nil, "checkout", "-b", branch, baseBranch); err != nil {
				return "", false, fmt.Errorf("create work branch: %w", err)
			}
			return branch, true, nil
		}
		if suffix == 1 {
			if _, err := runGitCommand(ctx, repoDir, nil, "checkout", branch); err == nil {
				status, _ := runGitCommand(ctx, repoDir, nil, "status", "--short")
				if !HasNonJJDirtyStatus(status) && isAncestor(ctx, repoDir, baseBranch, branch) {
					return branch, false, nil
				}
			}
			if _, err := runGitCommand(ctx, repoDir, nil, "checkout", baseBranch); err != nil {
				return "", false, fmt.Errorf("return to base branch: %w", err)
			}
		}
		branch = fmt.Sprintf("%s-%d", requested, suffix+1)
	}
}

func isAncestor(ctx context.Context, repoDir, ancestor, descendant string) bool {
	commandCWD, err := security.ResolveCommandCWD(repoDir)
	if err != nil {
		return false
	}
	cmdCtx, cancel := context.WithTimeout(commandContext(ctx), defaultGitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = commandCWD
	cmd.Env = scrubGitEnv(os.Environ())
	return cmd.Run() == nil
}

func commitRepositoryTurn(ctx context.Context, repoDir string, proposal TaskProposalResolution, selectedTask TaskRecord, runID, verdict string) ManifestCommit {
	commit := ManifestCommit{Ran: true, Status: "skipped"}
	if err := stageCommitChanges(ctx, repoDir); err != nil {
		commit.Status = "failed"
		commit.Error = redactSecrets(err.Error())
		return commit
	}
	if gitCachedDiffQuiet(ctx, repoDir) {
		commit.Ran = false
		commit.Status = "skipped"
		commit.Error = "no changes to commit"
		return commit
	}
	subject := commitSubject(proposal, selectedTask)
	mode := cleanCommitText(selectedTask.Mode)
	if mode == "" {
		mode = cleanCommitText(string(proposal.Resolved))
	}
	body := fmt.Sprintf("- Mode: %s\n- Run: %s\n- Turn: %s\n- Verdict: %s", mode, runID, runID, strings.ToLower(strings.TrimSpace(verdict)))
	if _, err := runGitCommand(ctx, repoDir, nil, "commit", "-m", subject, "-m", body); err != nil {
		commit.Status = "failed"
		commit.Error = redactSecrets(err.Error())
		return commit
	}
	sha := strings.TrimSpace(mustGitOutput(ctx, repoDir, "rev-parse", "HEAD"))
	commit.Status = "success"
	commit.SHA = sha
	commit.Message = subject
	return commit
}

func commitSubject(proposal TaskProposalResolution, selectedTask TaskRecord) string {
	taskID := cleanCommitText(selectedTask.ID)
	if taskID == "" {
		taskID = cleanCommitText(proposal.SelectedTaskID)
	}
	if taskID == "" {
		taskID = cleanCommitText(TaskProposalTaskID(proposal.Resolved))
	}
	title := cleanCommitText(selectedTask.Title)
	if title == "" {
		title = cleanCommitText(TaskProposalTaskTitle(proposal.Resolved))
	}
	return cleanCommitText(fmt.Sprintf("jj: %s %s", taskID, title))
}

func cleanCommitText(value string) string {
	return strings.Join(strings.Fields(redactSecrets(value)), " ")
}

func stageCommitChanges(ctx context.Context, repoDir string) error {
	changed, err := runGitCommand(ctx, repoDir, nil, "ls-files", "--modified", "--deleted", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	paths := nonJJChangedPaths(changed)
	if len(paths) > 0 {
		args := append([]string{"add", "--all", "--"}, paths...)
		if _, err := runGitCommand(ctx, repoDir, nil, args...); err != nil {
			return err
		}
	}
	for _, rel := range []string{DefaultSpecStatePath, DefaultTasksStatePath} {
		path := filepath.Join(repoDir, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if _, err := runGitCommand(ctx, repoDir, nil, "add", "--force", "--", rel); err != nil {
			return err
		}
	}
	return nil
}

func nonJJChangedPaths(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, path := range strings.Split(raw, "\x00") {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" || path == ".jj" || strings.HasPrefix(path, ".jj/") || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func pushRepositoryBranch(ctx context.Context, repoDir string, authToken, pushMode, workBranch string) (pushedRef string, err error) {
	auth, err := newGitAuth(authToken)
	if err != nil {
		return "", err
	}
	if auth != nil {
		defer auth.cleanup()
	}
	branch := workBranch
	if pushMode == PushModeCurrentBranch {
		branch = strings.TrimSpace(mustGitOutput(ctx, repoDir, "branch", "--show-current"))
	}
	if branch == "" {
		return "", errors.New("current branch is unknown")
	}
	if _, err := runGitCommand(ctx, repoDir, auth, "push", "-u", "origin", branch); err != nil {
		return "", err
	}
	return "origin/" + branch, nil
}

func gitCachedDiffQuiet(ctx context.Context, repoDir string) bool {
	commandCWD, err := security.ResolveCommandCWD(repoDir)
	if err != nil {
		return false
	}
	cmdCtx, cancel := context.WithTimeout(commandContext(ctx), defaultGitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = commandCWD
	cmd.Env = scrubGitEnv(os.Environ())
	return cmd.Run() == nil
}

func mustGitOutput(ctx context.Context, repoDir string, args ...string) string {
	out, err := runGitCommand(ctx, repoDir, nil, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func newGitAuth(token string) (*gitAuth, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "jj-git-askpass-*")
	if err != nil {
		return nil, err
	}
	helper := filepath.Join(dir, "askpass.sh")
	script := `#!/bin/sh
case "$1" in
  *Username*) printf '%s\n' "x-access-token" ;;
  *) printf '%s\n' "$JJ_GITHUB_ASKPASS_TOKEN" ;;
esac
`
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &gitAuth{token: token, tempDir: dir, askPass: helper}, nil
}

func (a *gitAuth) cleanup() {
	if a != nil && a.tempDir != "" {
		_ = os.RemoveAll(a.tempDir)
	}
}

func runGitCommand(ctx context.Context, cwd string, auth *gitAuth, args ...string) (string, error) {
	commandCWD, err := security.ResolveCommandCWD(cwd)
	if err != nil {
		return "", err
	}
	cmdCtx, cancel := context.WithTimeout(commandContext(ctx), defaultGitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", args...)
	cmd.Dir = commandCWD
	cmd.Env = scrubGitEnv(os.Environ())
	if auth != nil {
		cmd.Env = append(cmd.Env,
			"GIT_ASKPASS="+auth.askPass,
			"GIT_TERMINAL_PROMPT=0",
			"JJ_GITHUB_ASKPASS_TOKEN="+auth.token,
		)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return stdout.String(), errors.New("git command timed out")
		}
		if errors.Is(cmdCtx.Err(), context.Canceled) {
			return stdout.String(), context.Canceled
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(redactSecrets(msg))
	}
	return stdout.String(), nil
}

func scrubGitEnv(env []string) []string {
	return security.FilterEnv(env)
}
