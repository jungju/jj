package jjctl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const defaultGitHubAPIURL = "https://api.github.com"

type GitHubClient struct {
	Token      string
	APIBaseURL string
	HTTPClient *http.Client
}

type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type GitHubRepo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	Visibility    string `json:"visibility"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	Archived      bool   `json:"archived"`
	Disabled      bool   `json:"disabled"`
	Permissions   struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
		Pull  bool `json:"pull"`
	} `json:"permissions"`
	Owner struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type ActiveAccount struct {
	UserID         string
	GitHubUserID   int64
	GitHubLogin    string
	AccessTokenRef string
}

func NewGitHubClient(token string) *GitHubClient {
	apiBase := strings.TrimRight(firstNonEmpty(os.Getenv("JJ_GITHUB_API_URL"), defaultGitHubAPIURL), "/")
	return &GitHubClient{Token: token, APIBaseURL: apiBase, HTTPClient: http.DefaultClient}
}

func (c *GitHubClient) CurrentUser(ctx context.Context) (GitHubUser, error) {
	var user GitHubUser
	if err := c.getJSON(ctx, "/user", &user); err != nil {
		return user, err
	}
	return user, nil
}

func (c *GitHubClient) Repository(ctx context.Context, owner, repo string) (GitHubRepo, error) {
	var ghRepo GitHubRepo
	path := fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	if err := c.getJSON(ctx, path, &ghRepo); err != nil {
		return ghRepo, err
	}
	if ghRepo.Visibility == "" {
		if ghRepo.Private {
			ghRepo.Visibility = "private"
		} else {
			ghRepo.Visibility = "public"
		}
	}
	return ghRepo, nil
}

func (c *GitHubClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if strings.TrimSpace(c.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return CodeError{Code: ErrAuthGitHubTokenRevoked, Message: "GitHub token이 유효하지 않습니다.", Remedy: "jjctl auth login을 다시 실행하세요."}
	}
	if resp.StatusCode == http.StatusForbidden {
		return CodeError{Code: ErrRepoPermissionDenied, Message: "GitHub API 권한이 부족합니다.", Remedy: "repository 접근 scope와 SSO 승인을 확인하세요."}
	}
	if resp.StatusCode == http.StatusNotFound {
		return CodeError{Code: ErrRepoNotFound, Message: "GitHub repository를 찾을 수 없습니다.", Remedy: "owner/repo 이름과 접근 권한을 확인하세요."}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github api status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (a *App) CurrentAccount(ctx context.Context) (ActiveAccount, error) {
	var account ActiveAccount
	err := a.DB.QueryRowContext(ctx, `SELECT u.id, u.github_user_id, u.github_login, ga.access_token_ref
FROM users u
JOIN github_accounts ga ON ga.user_id = u.id
WHERE ga.revoked_at IS NULL
ORDER BY ga.updated_at DESC
LIMIT 1`).Scan(&account.UserID, &account.GitHubUserID, &account.GitHubLogin, &account.AccessTokenRef)
	if errors.Is(err, sql.ErrNoRows) {
		return account, CodeError{
			Code:    ErrAuthNotLoggedIn,
			Message: "GitHub 로그인이 필요합니다.",
			Remedy:  "jjctl auth login을 먼저 실행하세요.",
		}
	}
	return account, err
}

func (a *App) GitHubToken(ctx context.Context) (ActiveAccount, string, error) {
	account, err := a.CurrentAccount(ctx)
	if err != nil {
		return account, "", err
	}
	token, err := a.Secrets.Load(account.AccessTokenRef)
	if err != nil {
		return account, "", CodeError{
			Code:    ErrAuthGitHubTokenRevoked,
			Message: "저장된 GitHub token을 읽을 수 없습니다.",
			Remedy:  "jjctl auth login을 다시 실행하세요.",
			Err:     err,
		}
	}
	return account, string(token), nil
}

func (a *App) SaveGitHubLogin(ctx context.Context, token string, scopes []string) (GitHubUser, error) {
	client := NewGitHubClient(token)
	user, err := client.CurrentUser(ctx)
	if err != nil {
		return user, CodeError{Code: ErrAuthGitHubLoginFailed, Message: "GitHub 사용자 정보를 가져오지 못했습니다.", Remedy: "token 권한과 네트워크 상태를 확인하세요.", Err: err}
	}
	now := a.timestamp()
	userID := newID("usr")
	_, err = a.DB.ExecContext(ctx, `INSERT INTO users (
  id, github_user_id, github_login, display_name, avatar_url, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(github_user_id) DO UPDATE SET
  github_login = excluded.github_login,
  display_name = excluded.display_name,
  avatar_url = excluded.avatar_url,
  updated_at = excluded.updated_at`,
		userID, user.ID, user.Login, nullable(user.Name), nullable(user.AvatarURL), now, now)
	if err != nil {
		return user, err
	}
	if err := a.DB.QueryRowContext(ctx, "SELECT id FROM users WHERE github_user_id = ?", user.ID).Scan(&userID); err != nil {
		return user, err
	}
	ref, err := a.Secrets.Save("github access token for "+user.Login, []byte(token), now)
	if err != nil {
		return user, err
	}
	scopesJSON, _ := json.Marshal(scopes)
	if _, err := a.DB.ExecContext(ctx, "UPDATE github_accounts SET revoked_at = ?, updated_at = ? WHERE user_id = ? AND revoked_at IS NULL", now, now, userID); err != nil {
		return user, err
	}
	if _, err := a.DB.ExecContext(ctx, `INSERT INTO github_accounts (
  id, user_id, github_user_id, access_token_ref, scopes_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID("gha"), userID, user.ID, ref, string(scopesJSON), now, now); err != nil {
		return user, err
	}
	if err := a.Audit(ctx, userID, "auth.login", "github_account", "", map[string]any{"github_login": user.Login}); err != nil {
		return user, err
	}
	return user, nil
}

func parseGitHubFullName(input string) (string, string, error) {
	value := strings.TrimSpace(input)
	value = strings.TrimSuffix(value, ".git")
	if strings.HasPrefix(value, "https://github.com/") || strings.HasPrefix(value, "http://github.com/") {
		u, err := url.Parse(value)
		if err != nil {
			return "", "", err
		}
		value = strings.TrimPrefix(u.Path, "/")
	}
	if strings.HasPrefix(value, "git@github.com:") {
		value = strings.TrimPrefix(value, "git@github.com:")
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", CodeError{Code: ErrRepoRemoteNotGitHub, Message: "GitHub owner/repo 형식을 인식하지 못했습니다.", Remedy: "owner/repo 또는 GitHub remote URL을 사용하세요."}
	}
	return parts[0], parts[1], nil
}

func currentGitHubRemote(ctx context.Context, repoPath string) (string, string, error) {
	out, err := runCommand(ctx, repoPath, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", "", CodeError{Code: ErrRepoRemoteNotGitHub, Message: "origin remote를 읽을 수 없습니다.", Remedy: "GitHub origin remote를 설정하거나 owner/repo를 직접 지정하세요.", Err: err}
	}
	return parseGitHubFullName(strings.TrimSpace(out))
}

func updateLocalRepoConfig(repoPath string, payload map[string]any) error {
	jjDir := filepath.Join(repoPath, ".jj")
	if err := os.MkdirAll(jjDir, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(jjDir, "config.json")
	existing := map[string]any{}
	if data, err := os.ReadFile(configPath); err == nil && len(bytes.TrimSpace(data)) > 0 {
		_ = json.Unmarshal(data, &existing)
	}
	existing["version"] = 1
	existing["repo"] = payload
	return writeJSONFile(configPath, existing, 0o600)
}

func gitCurrentBranch(ctx context.Context, repoPath string) string {
	out, err := runCommand(ctx, repoPath, "git", "branch", "--show-current")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitCommitSHA(ctx context.Context, repoPath string) string {
	out, err := runCommand(ctx, repoPath, "git", "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitWorkingTreeDirty(ctx context.Context, repoPath string) bool {
	out, err := runCommand(ctx, repoPath, "git", "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

var unsafeCodexPattern = regexp.MustCompile(`(?i)(kubectl\s+(delete\s+namespace|delete\s+secret|create\s+clusterrolebinding)|git\s+push\s+--force|rm\s+-rf\s+/|kubeconfig|registry\s+password|ssh\s+private\s+key)`)

func containsUnsafeRequest(request string) bool {
	return unsafeCodexPattern.MatchString(request)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
