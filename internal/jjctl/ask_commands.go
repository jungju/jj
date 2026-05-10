package jjctl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newAskCommand(cc *commandContext) *cobra.Command {
	var mode string
	var repoName string
	cmd := &cobra.Command{
		Use:   "ask <request>",
		Short: "Run a natural-language Codex request in a registered repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			return runAsk(cmd.Context(), app, cc.stdout, args[0], mode, repoName)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "edit", "Codex mode: plan-only, edit, review")
	cmd.Flags().StringVar(&repoName, "repo", "", "registered GitHub repository full name")
	return cmd
}

func runAsk(ctx context.Context, app *App, stdout anyWriter, request, mode, repoName string) error {
	if containsUnsafeRequest(request) {
		return CodeError{Code: ErrCodexRunFailed, Message: "요청에 차단된 위험 동작이 포함되어 있습니다.", Remedy: "secret 출력, 강제 push, destructive kubectl 명령을 제거하세요."}
	}
	repo, err := resolveAskRepository(ctx, app, repoName)
	if err != nil {
		return err
	}
	if mode == "" {
		mode = "edit"
	}
	if mode != "plan-only" && mode != "edit" && mode != "review" {
		return fmt.Errorf("invalid ask mode %q", mode)
	}
	if mode == "edit" && !repo.CanPush {
		return CodeError{Code: ErrRepoPermissionDenied, Message: "코드 수정에는 repository push 권한이 필요합니다.", Remedy: "read-only 작업은 --mode plan-only 또는 --mode review를 사용하세요."}
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return CodeError{Code: ErrCodexNotInstalled, Message: "Codex CLI를 찾을 수 없습니다.", Remedy: "Codex CLI를 설치하거나 PATH에 추가하세요.", Err: err}
	}
	taskType := map[string]string{"plan-only": "analyze", "edit": "code", "review": "review"}[mode]
	now := app.timestamp()
	taskID := newID("task")
	if _, err := app.DB.ExecContext(ctx, `INSERT INTO tasks (
  id, user_id, repository_id, type, prompt, status, mode, branch_name, commit_sha, created_at, started_at
) VALUES (?, ?, ?, ?, ?, 'running', ?, ?, ?, ?, ?)`,
		taskID, repo.UserID, repo.ID, taskType, request, mode, gitCurrentBranch(ctx, repo.LocalPath), gitCommitSHA(ctx, repo.LocalPath), now, now); err != nil {
		return err
	}
	if gitWorkingTreeDirty(ctx, repo.LocalPath) {
		fmt.Fprintln(stdout, "! working tree가 이미 dirty입니다. jjctl은 자동 commit/push를 하지 않습니다.")
	}
	sandbox := "workspace-write"
	prompt := request
	if mode == "plan-only" || mode == "review" {
		sandbox = "read-only"
		prompt = "Do not edit files. " + request
	}
	args := []string{"exec", "--cd", repo.LocalPath, "--sandbox", sandbox, prompt}
	fmt.Fprintf(stdout, "Codex 실행: %s (%s)\n", repo.FullName, mode)
	out, runErr := runCommand(ctx, repo.LocalPath, "codex", args...)
	completed := app.timestamp()
	status := "succeeded"
	var errorCode any
	var errorMessage any
	if runErr != nil {
		status = "failed"
		errorCode = ErrCodexRunFailed
		errorMessage = runErr.Error()
	}
	if _, err := app.DB.ExecContext(ctx, `UPDATE tasks SET status = ?, completed_at = ?, error_code = ?, error_message = ? WHERE id = ?`,
		status, completed, errorCode, errorMessage, taskID); err != nil {
		return err
	}
	diffSummary, _ := runCommand(ctx, repo.LocalPath, "git", "diff", "--stat")
	changedFiles := changedFiles(ctx, repo.LocalPath)
	changedJSON, _ := json.Marshal(changedFiles)
	pathHash := sha256.Sum256([]byte(repo.LocalPath))
	if _, err := app.DB.ExecContext(ctx, `INSERT INTO codex_sessions (
  id, task_id, local_repo_path_hash, codex_mode, sandbox, summary, diff_summary, changed_files_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID("codex"), taskID, hex.EncodeToString(pathHash[:]), mode, sandbox, strings.TrimSpace(out), strings.TrimSpace(diffSummary), string(changedJSON), now); err != nil {
		return err
	}
	if err := app.Audit(ctx, repo.UserID, "codex.run", "repository", repo.ID, map[string]any{"task_id": taskID, "mode": mode, "status": status}); err != nil {
		return err
	}
	if runErr != nil {
		return CodeError{Code: ErrCodexRunFailed, Message: "Codex 실행에 실패했습니다.", Remedy: "Codex 인증 상태와 요청 내용을 확인하세요.", Err: runErr}
	}
	if strings.TrimSpace(out) != "" {
		fmt.Fprintln(stdout, strings.TrimSpace(out))
	}
	fmt.Fprintf(stdout, "\nTask: %s\nStatus: %s\n", taskID, status)
	if len(changedFiles) > 0 {
		fmt.Fprintln(stdout, "Changed files:")
		for _, file := range changedFiles {
			fmt.Fprintf(stdout, "- %s\n", file)
		}
	}
	return nil
}

func resolveAskRepository(ctx context.Context, app *App, repoName string) (CurrentRepositoryRecord, error) {
	if strings.TrimSpace(repoName) == "" {
		return app.CurrentRepository(ctx)
	}
	account, err := app.CurrentAccount(ctx)
	if err != nil {
		return CurrentRepositoryRecord{}, err
	}
	var repo CurrentRepositoryRecord
	repo.UserID = account.UserID
	repo.FullName = strings.TrimSpace(repoName)
	err = app.DB.QueryRowContext(ctx, `SELECT id, COALESCE(local_path, '')
FROM repositories
WHERE user_id = ? AND full_name = ? AND deleted_at IS NULL`, account.UserID, repo.FullName).Scan(&repo.ID, &repo.LocalPath)
	if err != nil {
		return CurrentRepositoryRecord{}, CodeError{Code: ErrRepoNotFound, Message: "등록된 repository를 찾을 수 없습니다.", Remedy: "jjctl repo list로 이름을 확인하세요.", Err: err}
	}
	if strings.TrimSpace(repo.LocalPath) == "" {
		return CurrentRepositoryRecord{}, CodeError{Code: ErrRepoNotFound, Message: "repository local_path가 없습니다.", Remedy: "해당 repository 디렉터리에서 jjctl repo add . 를 실행하세요."}
	}
	err = app.DB.QueryRowContext(ctx, `SELECT can_pull, can_push, can_admin
FROM repo_permissions
WHERE user_id = ? AND repository_id = ?
ORDER BY checked_at DESC
LIMIT 1`, account.UserID, repo.ID).Scan(&repo.CanPull, &repo.CanPush, &repo.CanAdmin)
	if err != nil {
		repo.CanPull = true
	}
	return repo, nil
}

func changedFiles(ctx context.Context, repoPath string) []string {
	out, err := runCommand(ctx, repoPath, "git", "diff", "--name-only")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

type anyWriter interface {
	Write([]byte) (int, error)
}
