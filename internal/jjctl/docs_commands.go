package jjctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type docsFileTemplate struct {
	Path    string
	Title   string
	Content string
}

var docsTemplates = []docsFileTemplate{
	{Path: "docs/README.md", Title: "Documentation", Content: "Repository documentation index for jj-managed development and deployment."},
	{Path: "docs/product/requirements.md", Title: "Requirements", Content: "Product requirements and acceptance criteria."},
	{Path: "docs/product/user-stories.md", Title: "User Stories", Content: "User stories, roles, and expected outcomes."},
	{Path: "docs/product/roadmap.md", Title: "Roadmap", Content: "Planned delivery phases and priorities."},
	{Path: "docs/architecture/overview.md", Title: "Architecture Overview", Content: "High-level architecture and important boundaries."},
	{Path: "docs/architecture/system-context.md", Title: "System Context", Content: "External systems, users, and trust boundaries."},
	{Path: "docs/architecture/data-model.md", Title: "Data Model", Content: "Core data structures and persistence rules."},
	{Path: "docs/architecture/github-integration.md", Title: "GitHub Integration", Content: "Repository provider behavior and permissions."},
	{Path: "docs/architecture/codex-runner.md", Title: "Codex Runner", Content: "Codex execution policy, sandboxing, and outputs."},
	{Path: "docs/architecture/sqlite-storage.md", Title: "SQLite Storage", Content: "Local SQLite storage and migration notes."},
	{Path: "docs/architecture/deployment-architecture.md", Title: "Deployment Architecture", Content: "Deployment pools, targets, and runtime flow."},
	{Path: "docs/cli/commands.md", Title: "CLI Commands", Content: "Supported jjctl commands and examples."},
	{Path: "docs/cli/examples.md", Title: "CLI Examples", Content: "Common local workflows."},
	{Path: "docs/cli/error-codes.md", Title: "Error Codes", Content: "Actionable error codes and remedies."},
	{Path: "docs/deployment/overview.md", Title: "Deployment Overview", Content: "Deployment strategy and approval model."},
	{Path: "docs/deployment/deployment-pools.md", Title: "Deployment Pools", Content: "Pool and target configuration."},
	{Path: "docs/deployment/kubernetes-credentials.md", Title: "Kubernetes Credentials", Content: "Credential registration and verification rules."},
	{Path: "docs/deployment/repo-deploy-config.md", Title: "Repository Deploy Config", Content: "jj.deploy.yaml structure and target mapping."},
	{Path: "docs/deployment/rollout-and-rollback.md", Title: "Rollout and Rollback", Content: "Rollout status and rollback planning."},
	{Path: "docs/deployment/registry.md", Title: "Registry", Content: "Image registry configuration and credentials."},
	{Path: "docs/api/overview.md", Title: "API Overview", Content: "Local API surface and future integration notes."},
	{Path: "docs/security/threat-model.md", Title: "Threat Model", Content: "Threats, mitigations, and residual risk."},
	{Path: "docs/security/permissions.md", Title: "Permissions", Content: "GitHub and Kubernetes permission model."},
	{Path: "docs/security/token-storage.md", Title: "Token Storage", Content: "Secret storage and redaction requirements."},
	{Path: "docs/security/kubernetes-security.md", Title: "Kubernetes Security", Content: "Namespace-scoped deployment security."},
	{Path: "docs/security/data-retention.md", Title: "Data Retention", Content: "Local data retention and deletion behavior."},
	{Path: "docs/operations/local-development.md", Title: "Local Development", Content: "Local setup and development workflow."},
	{Path: "docs/operations/backup-and-restore.md", Title: "Backup and Restore", Content: "Local backup and restore procedure."},
	{Path: "docs/operations/monitoring.md", Title: "Monitoring", Content: "Deployment and CLI observability notes."},
	{Path: "docs/operations/incident-response.md", Title: "Incident Response", Content: "Incident response checklist."},
	{Path: "docs/adr/0001-use-sqlite-for-v1.md", Title: "ADR 0001: Use SQLite for v1", Content: "Status: Accepted\n\njj v1 uses local SQLite for CLI-first storage."},
	{Path: "docs/adr/0002-run-codex-locally-first.md", Title: "ADR 0002: Run Codex Locally First", Content: "Status: Accepted\n\njj v1 runs Codex from the user's local repository."},
	{Path: "docs/adr/0003-use-kubernetes-credential-pools.md", Title: "ADR 0003: Use Kubernetes Credential Pools", Content: "Status: Accepted\n\njj groups deployment targets into local credential-backed pools."},
	{Path: "docs/adr/0004-require-user-approval-before-deploy.md", Title: "ADR 0004: Require User Approval Before Deploy", Content: "Status: Accepted\n\njj requires explicit approval before kubectl apply."},
}

func newDocsCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "docs", Short: "Manage repository documentation"}
	root.AddCommand(newDocsInitCommand(cc))
	root.AddCommand(newDocsStatusCommand(cc))
	root.AddCommand(newDocsValidateCommand(cc))
	return root
}

func newDocsInitCommand(cc *commandContext) *cobra.Command {
	var force bool
	var noCodex bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create jj docs structure and AGENTS.md",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			repo, err := app.CurrentRepository(cmd.Context())
			if err != nil {
				return err
			}
			result, err := initDocs(repo.LocalPath, repo.FullName, force)
			if err != nil {
				return err
			}
			if err := app.Audit(cmd.Context(), repo.UserID, "docs.init", "repository", repo.ID, map[string]any{"created": result.Created, "skipped": result.Skipped, "force": force, "no_codex": noCodex}); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ docs 초기화 완료: created=%d skipped=%d\n", result.Created, result.Skipped)
			if !noCodex {
				fmt.Fprintln(cc.stdout, "Codex 보강은 jjctl ask \"docs를 검토하고 빈 섹션을 채워줘\" 로 실행할 수 있습니다.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing docs template files")
	cmd.Flags().BoolVar(&noCodex, "no-codex", false, "skip Codex-assisted draft guidance")
	return cmd
}

func newDocsStatusCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show docs initialization status",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := ensureInsideRepo(currentWorkingDirectory())
			if err != nil {
				return err
			}
			manifest := filepath.Join(root, ".jj", "docs-manifest.json")
			fmt.Fprintf(cc.stdout, "Repo: %s\n", root)
			if fileExists(manifest) {
				fmt.Fprintf(cc.stdout, "✓ manifest: %s\n", manifest)
			} else {
				fmt.Fprintln(cc.stdout, "! manifest: missing")
			}
			present := 0
			for _, tpl := range docsTemplates {
				if fileExists(filepath.Join(root, tpl.Path)) {
					present++
				}
			}
			fmt.Fprintf(cc.stdout, "Docs files: %d/%d\n", present, len(docsTemplates))
			if fileExists(filepath.Join(root, "AGENTS.md")) {
				fmt.Fprintln(cc.stdout, "✓ AGENTS.md")
			} else {
				fmt.Fprintln(cc.stdout, "! AGENTS.md missing")
			}
			return nil
		},
	}
}

func newDocsValidateCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate docs structure",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := ensureInsideRepo(currentWorkingDirectory())
			if err != nil {
				return err
			}
			missing := missingDocsFiles(root)
			if !fileExists(filepath.Join(root, ".jj", "docs-manifest.json")) {
				missing = append(missing, ".jj/docs-manifest.json")
			}
			if !fileExists(filepath.Join(root, "AGENTS.md")) {
				missing = append(missing, "AGENTS.md")
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				for _, path := range missing {
					fmt.Fprintf(cc.stdout, "! missing: %s\n", path)
				}
				return CodeError{Code: ErrDocsManifestMissing, Message: "docs 구조가 완성되지 않았습니다.", Remedy: "jjctl docs init을 실행하세요."}
			}
			fmt.Fprintln(cc.stdout, "✓ docs validate passed")
			return nil
		},
	}
}

type docsInitResult struct {
	Created int
	Skipped int
}

func initDocs(repoPath, fullName string, force bool) (docsInitResult, error) {
	var result docsInitResult
	for _, tpl := range docsTemplates {
		path := filepath.Join(repoPath, tpl.Path)
		if fileExists(path) && !force {
			result.Skipped++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return result, CodeError{Code: ErrDocsInitFailed, Message: "docs 디렉터리를 만들 수 없습니다.", Err: err}
		}
		content := "# " + tpl.Title + "\n\n" + strings.TrimSpace(tpl.Content) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return result, CodeError{Code: ErrDocsInitFailed, Message: "docs 파일을 쓸 수 없습니다.", Err: err}
		}
		result.Created++
	}
	if err := mergeAgentsFile(filepath.Join(repoPath, "AGENTS.md")); err != nil {
		return result, err
	}
	files := make([]string, 0, len(docsTemplates))
	for _, tpl := range docsTemplates {
		files = append(files, tpl.Path)
	}
	manifest := map[string]any{
		"version":   1,
		"repo":      fullName,
		"generated": true,
		"files":     files,
	}
	if err := writeJSONFile(filepath.Join(repoPath, ".jj", "docs-manifest.json"), manifest, 0o600); err != nil {
		return result, err
	}
	return result, nil
}

func mergeAgentsFile(path string) error {
	const marker = "This repository is managed by jj."
	if data, err := os.ReadFile(path); err == nil {
		text := string(data)
		if strings.Contains(text, marker) {
			return nil
		}
		merged := strings.TrimRight(text, "\n") + "\n\n" + jjAgentsSection()
		return os.WriteFile(path, []byte(merged), 0o644)
	}
	return os.WriteFile(path, []byte(jjAgentsSection()), 0o644)
}

func jjAgentsSection() string {
	return `# AGENTS.md

## Product

This repository is managed by jj.

jj is a CLI-first development and deployment tool for GitHub repositories.
It uses Codex for development assistance and Kubernetes deployment pools for deployment.

## Working rules

- Before making changes, explain the plan.
- Prefer minimal, reviewable changes.
- Do not overwrite existing files unless explicitly requested.
- Do not read, print, or expose secrets.
- Do not commit automatically unless the user explicitly asks.
- Do not push automatically unless the user explicitly asks.
- Do not create a pull request automatically unless the user explicitly asks.
- When changing public behavior, update docs/.
- After code changes, run the smallest relevant test command.
- Summarize changed files after every task.

## Docs rules

- Keep docs concise and implementation-oriented.
- Use docs/product for product decisions.
- Use docs/architecture for system design.
- Use docs/deployment for deployment behavior.
- Use docs/security for security and permission rules.
- Use docs/adr for irreversible decisions.

## Git rules

- Never force-push.
- Never merge without user approval.
- Do not modify unrelated files.
- Show the diff before suggesting commit.

## Deployment rules

- Do not deploy automatically.
- Do not run kubectl apply unless the user explicitly asks.
- Do not print kubeconfig, tokens, certificates, or registry credentials.
- Do not create cluster-wide RBAC resources unless explicitly requested.
- Prefer namespace-scoped manifests.
- Before changing deployment files, update docs/deployment/.
- Before deployment, show the deployment plan and expected resources.
- Production deployment requires explicit user approval.
`
}

func missingDocsFiles(repoPath string) []string {
	var missing []string
	for _, tpl := range docsTemplates {
		if !fileExists(filepath.Join(repoPath, tpl.Path)) {
			missing = append(missing, tpl.Path)
		}
	}
	return missing
}

type CurrentRepositoryRecord struct {
	ID        string
	UserID    string
	FullName  string
	LocalPath string
	CanPull   bool
	CanPush   bool
	CanAdmin  bool
}

func (a *App) CurrentRepository(ctx context.Context) (CurrentRepositoryRecord, error) {
	account, err := a.CurrentAccount(ctx)
	if err != nil {
		return CurrentRepositoryRecord{}, err
	}
	fullName, repoPath, err := currentRepoIdentity(ctx)
	if err != nil {
		return CurrentRepositoryRecord{}, err
	}
	var repo CurrentRepositoryRecord
	repo.UserID = account.UserID
	repo.FullName = fullName
	repo.LocalPath = repoPath
	err = a.DB.QueryRowContext(ctx, `SELECT id, COALESCE(local_path, '')
FROM repositories
WHERE user_id = ? AND full_name = ? AND deleted_at IS NULL`, account.UserID, fullName).Scan(&repo.ID, &repo.LocalPath)
	if errors.Is(err, sql.ErrNoRows) {
		return CurrentRepositoryRecord{}, CodeError{Code: ErrRepoNotFound, Message: "현재 repository가 jj에 등록되어 있지 않습니다.", Remedy: "jjctl repo add . 를 먼저 실행하세요."}
	}
	if err != nil {
		return CurrentRepositoryRecord{}, err
	}
	if strings.TrimSpace(repo.LocalPath) == "" {
		repo.LocalPath = repoPath
	}
	err = a.DB.QueryRowContext(ctx, `SELECT can_pull, can_push, can_admin
FROM repo_permissions
WHERE user_id = ? AND repository_id = ?
ORDER BY checked_at DESC
LIMIT 1`, account.UserID, repo.ID).Scan(&repo.CanPull, &repo.CanPush, &repo.CanAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		repo.CanPull = true
		return repo, nil
	}
	return repo, err
}
