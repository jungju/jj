package jjctl

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newRepoCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "repo", Short: "Manage GitHub repositories"}
	root.AddCommand(newRepoAddCommand(cc))
	root.AddCommand(newRepoListCommand(cc))
	root.AddCommand(newRepoStatusCommand(cc))
	root.AddCommand(newRepoRemoveCommand(cc))
	root.AddCommand(newRepoSyncCommand(cc))
	return root
}

func newRepoAddCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "add <owner/repo|.>",
		Short: "Register a GitHub repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			repoPath := ""
			owner := ""
			name := ""
			if args[0] == "." {
				root, err := ensureInsideRepo(currentWorkingDirectory())
				if err != nil {
					return err
				}
				repoPath = root
				owner, name, err = currentGitHubRemote(cmd.Context(), root)
				if err != nil {
					return err
				}
			} else {
				owner, name, err = parseGitHubFullName(args[0])
				if err != nil {
					return err
				}
				if root, gitErr := ensureInsideRepo(currentWorkingDirectory()); gitErr == nil {
					if ro, rn, remoteErr := currentGitHubRemote(cmd.Context(), root); remoteErr == nil && strings.EqualFold(ro+"/"+rn, owner+"/"+name) {
						repoPath = root
					}
				}
			}
			record, permissions, err := app.RegisterRepository(cmd.Context(), owner, name, repoPath)
			if err != nil {
				return err
			}
			if repoPath != "" {
				if err := updateLocalRepoConfig(repoPath, map[string]any{
					"provider":  "github",
					"full_name": record.FullName,
					"id":        record.ID,
				}); err != nil {
					return err
				}
			}
			fmt.Fprintf(cc.stdout, "✓ repository 등록 완료: %s\n", record.FullName)
			fmt.Fprintf(cc.stdout, "권한: pull=%t push=%t admin=%t\n", permissions.CanPull, permissions.CanPush, permissions.CanAdmin)
			if repoPath != "" {
				fmt.Fprintf(cc.stdout, "Local path: %s\n", repoPath)
			}
			return nil
		},
	}
}

func newRepoListCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			account, err := app.CurrentAccount(cmd.Context())
			if err != nil {
				return err
			}
			rows, err := app.DB.QueryContext(cmd.Context(), `SELECT full_name, visibility, default_branch, COALESCE(local_path, '')
FROM repositories
WHERE user_id = ? AND deleted_at IS NULL
ORDER BY full_name`, account.UserID)
			if err != nil {
				return err
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				found = true
				var fullName, visibility, branch, localPath string
				if err := rows.Scan(&fullName, &visibility, &branch, &localPath); err != nil {
					return err
				}
				if localPath != "" {
					fmt.Fprintf(cc.stdout, "%s\t%s\t%s\t%s\n", fullName, visibility, branch, localPath)
				} else {
					fmt.Fprintf(cc.stdout, "%s\t%s\t%s\n", fullName, visibility, branch)
				}
			}
			if !found {
				fmt.Fprintln(cc.stdout, "등록된 repository가 없습니다.")
			}
			return rows.Err()
		},
	}
}

func newRepoStatusCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current repository registration",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			account, err := app.CurrentAccount(cmd.Context())
			if err != nil {
				return err
			}
			fullName, repoPath, err := currentRepoIdentity(cmd.Context())
			if err != nil {
				return err
			}
			var id, visibility, branch string
			err = app.DB.QueryRowContext(cmd.Context(), `SELECT id, COALESCE(visibility, ''), COALESCE(default_branch, '')
FROM repositories WHERE user_id = ? AND full_name = ? AND deleted_at IS NULL`, account.UserID, fullName).Scan(&id, &visibility, &branch)
			if errors.Is(err, sql.ErrNoRows) {
				return CodeError{Code: ErrRepoNotFound, Message: "현재 repository가 jj에 등록되어 있지 않습니다.", Remedy: "jjctl repo add . 를 실행하세요."}
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "Repo:    %s\n", fullName)
			fmt.Fprintf(cc.stdout, "ID:      %s\n", id)
			fmt.Fprintf(cc.stdout, "Branch:  %s\n", branch)
			fmt.Fprintf(cc.stdout, "Visible: %s\n", visibility)
			fmt.Fprintf(cc.stdout, "Path:    %s\n", repoPath)
			return nil
		},
	}
}

func newRepoRemoveCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <owner/repo>",
		Short: "Remove a registered repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			account, err := app.CurrentAccount(cmd.Context())
			if err != nil {
				return err
			}
			owner, name, err := parseGitHubFullName(args[0])
			if err != nil {
				return err
			}
			now := app.timestamp()
			res, err := app.DB.ExecContext(cmd.Context(), "UPDATE repositories SET deleted_at = ?, updated_at = ? WHERE user_id = ? AND full_name = ? AND deleted_at IS NULL", now, now, account.UserID, owner+"/"+name)
			if err != nil {
				return err
			}
			affected, _ := res.RowsAffected()
			if affected == 0 {
				return CodeError{Code: ErrRepoNotFound, Message: "등록된 repository를 찾을 수 없습니다.", Remedy: "jjctl repo list로 이름을 확인하세요."}
			}
			if err := app.Audit(cmd.Context(), account.UserID, "repo.remove", "repository", owner+"/"+name, nil); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ repository 제거 완료: %s\n", owner+"/"+name)
			return nil
		},
	}
}

func newRepoSyncCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Refresh current repository metadata and permissions",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			fullName, repoPath, err := currentRepoIdentity(cmd.Context())
			if err != nil {
				return err
			}
			owner, name, err := parseGitHubFullName(fullName)
			if err != nil {
				return err
			}
			record, permissions, err := app.RegisterRepository(cmd.Context(), owner, name, repoPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ repository 동기화 완료: %s\n", record.FullName)
			fmt.Fprintf(cc.stdout, "권한: pull=%t push=%t admin=%t\n", permissions.CanPull, permissions.CanPush, permissions.CanAdmin)
			return nil
		},
	}
}

type RepositoryRecord struct {
	ID       string
	FullName string
}

type PermissionRecord struct {
	CanPull  bool
	CanPush  bool
	CanAdmin bool
}

func (a *App) RegisterRepository(ctx context.Context, owner, name, localPath string) (RepositoryRecord, PermissionRecord, error) {
	account, token, err := a.GitHubToken(ctx)
	if err != nil {
		return RepositoryRecord{}, PermissionRecord{}, err
	}
	ghRepo, err := NewGitHubClient(token).Repository(ctx, owner, name)
	if err != nil {
		return RepositoryRecord{}, PermissionRecord{}, err
	}
	if ghRepo.Archived {
		return RepositoryRecord{}, PermissionRecord{}, CodeError{Code: ErrRepoArchived, Message: "Archived repository는 등록할 수 없습니다.", Remedy: "GitHub에서 archive 상태를 해제하세요."}
	}
	if ghRepo.Disabled {
		return RepositoryRecord{}, PermissionRecord{}, CodeError{Code: ErrRepoDisabled, Message: "Disabled repository는 등록할 수 없습니다.", Remedy: "GitHub repository 상태를 확인하세요."}
	}
	canAdmin := ghRepo.Permissions.Admin
	canPush := ghRepo.Permissions.Push || canAdmin
	canPull := ghRepo.Permissions.Pull || canPush
	if !canPull {
		return RepositoryRecord{}, PermissionRecord{}, CodeError{Code: ErrRepoPermissionDenied, Message: "repository pull 권한이 없습니다.", Remedy: "GitHub repository 접근 권한을 확인하세요."}
	}
	now := a.timestamp()
	repoID := newID("repo")
	fullName := firstNonEmpty(ghRepo.FullName, owner+"/"+name)
	repoName := firstNonEmpty(ghRepo.Name, name)
	repoOwner := owner
	if ghRepo.Owner.Login != "" {
		repoOwner = ghRepo.Owner.Login
	}
	_, err = a.DB.ExecContext(ctx, `INSERT INTO repositories (
  id, user_id, provider, github_repo_id, owner, name, full_name, visibility, default_branch,
  clone_url, ssh_url, local_path, archived, disabled, created_at, updated_at
) VALUES (?, ?, 'github', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id, full_name) DO UPDATE SET
  github_repo_id = excluded.github_repo_id,
  owner = excluded.owner,
  name = excluded.name,
  visibility = excluded.visibility,
  default_branch = excluded.default_branch,
  clone_url = excluded.clone_url,
  ssh_url = excluded.ssh_url,
  local_path = COALESCE(excluded.local_path, repositories.local_path),
  archived = excluded.archived,
  disabled = excluded.disabled,
  deleted_at = NULL,
  updated_at = excluded.updated_at`,
		repoID, account.UserID, ghRepo.ID, repoOwner, repoName, fullName, nullable(ghRepo.Visibility), nullable(ghRepo.DefaultBranch), nullable(ghRepo.CloneURL), nullable(ghRepo.SSHURL), nullable(localPath), boolInt(ghRepo.Archived), boolInt(ghRepo.Disabled), now, now)
	if err != nil {
		return RepositoryRecord{}, PermissionRecord{}, err
	}
	if err := a.DB.QueryRowContext(ctx, "SELECT id FROM repositories WHERE user_id = ? AND full_name = ?", account.UserID, fullName).Scan(&repoID); err != nil {
		return RepositoryRecord{}, PermissionRecord{}, err
	}
	_, err = a.DB.ExecContext(ctx, `INSERT INTO repo_permissions (
  id, user_id, repository_id, can_pull, can_push, can_admin, source, checked_at
) VALUES (?, ?, ?, ?, ?, ?, 'github_api', ?)`,
		newID("perm"), account.UserID, repoID, boolInt(canPull), boolInt(canPush), boolInt(canAdmin), now)
	if err != nil {
		return RepositoryRecord{}, PermissionRecord{}, err
	}
	if err := a.Audit(ctx, account.UserID, "repo.add", "repository", repoID, map[string]any{"full_name": fullName}); err != nil {
		return RepositoryRecord{}, PermissionRecord{}, err
	}
	if err := a.Audit(ctx, account.UserID, "repo.permission.verify", "repository", repoID, map[string]any{"pull": canPull, "push": canPush, "admin": canAdmin}); err != nil {
		return RepositoryRecord{}, PermissionRecord{}, err
	}
	return RepositoryRecord{ID: repoID, FullName: fullName}, PermissionRecord{CanPull: canPull, CanPush: canPush, CanAdmin: canAdmin}, nil
}

func currentRepoIdentity(ctx context.Context) (string, string, error) {
	root, err := ensureInsideRepo(currentWorkingDirectory())
	if err != nil {
		return "", "", err
	}
	configPath := filepath.Join(root, ".jj", "config.json")
	if data, err := os.ReadFile(configPath); err == nil {
		var cfg struct {
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		}
		if json.Unmarshal(data, &cfg) == nil && strings.TrimSpace(cfg.Repo.FullName) != "" {
			return cfg.Repo.FullName, root, nil
		}
	}
	owner, name, err := currentGitHubRemote(ctx, root)
	if err != nil {
		return "", "", err
	}
	return owner + "/" + name, root, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
