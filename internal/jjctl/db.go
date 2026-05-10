package jjctl

import (
	"context"
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

type migration struct {
	id  string
	sql string
}

var migrations = []migration{
	{id: "001_initial", sql: initialSchemaSQL},
}

func OpenDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  id TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	for _, m := range migrations {
		var exists int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE id = ?", m.id).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (id, applied_at) VALUES (?, datetime('now'))", m.id); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

const initialSchemaSQL = `
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  github_user_id INTEGER NOT NULL UNIQUE,
  github_login TEXT NOT NULL,
  display_name TEXT,
  avatar_url TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS github_accounts (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  github_user_id INTEGER NOT NULL,
  access_token_ref TEXT NOT NULL,
  refresh_token_ref TEXT,
  token_expires_at TEXT,
  scopes_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  revoked_at TEXT,
  FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS repositories (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'github',
  github_repo_id INTEGER NOT NULL,
  owner TEXT NOT NULL,
  name TEXT NOT NULL,
  full_name TEXT NOT NULL,
  visibility TEXT,
  default_branch TEXT,
  clone_url TEXT,
  ssh_url TEXT,
  local_path TEXT,
  archived INTEGER NOT NULL DEFAULT 0,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  UNIQUE(user_id, full_name),
  FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS repo_permissions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  repository_id TEXT NOT NULL,
  can_pull INTEGER NOT NULL DEFAULT 0,
  can_push INTEGER NOT NULL DEFAULT 0,
  can_admin INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL,
  checked_at TEXT NOT NULL,
  expires_at TEXT,
  FOREIGN KEY(user_id) REFERENCES users(id),
  FOREIGN KEY(repository_id) REFERENCES repositories(id)
);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  repository_id TEXT,
  type TEXT NOT NULL,
  prompt TEXT NOT NULL,
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  branch_name TEXT,
  commit_sha TEXT,
  pr_url TEXT,
  created_at TEXT NOT NULL,
  started_at TEXT,
  completed_at TEXT,
  error_code TEXT,
  error_message TEXT,
  FOREIGN KEY(user_id) REFERENCES users(id),
  FOREIGN KEY(repository_id) REFERENCES repositories(id)
);

CREATE TABLE IF NOT EXISTS codex_sessions (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  local_repo_path_hash TEXT,
  codex_mode TEXT NOT NULL,
  sandbox TEXT NOT NULL,
  summary TEXT,
  diff_summary TEXT,
  changed_files_json TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES tasks(id)
);

CREATE TABLE IF NOT EXISTS k8s_credentials (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'kubernetes',
  cluster_name TEXT,
  context_name TEXT,
  server_url TEXT,
  default_namespace TEXT,
  credential_type TEXT NOT NULL,
  credential_ref TEXT NOT NULL,
  has_exec_plugin INTEGER NOT NULL DEFAULT 0,
  last_verified_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  UNIQUE(user_id, name),
  FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS registries (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  registry_url TEXT NOT NULL,
  credential_type TEXT NOT NULL,
  credential_ref TEXT NOT NULL,
  last_verified_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  UNIQUE(user_id, name),
  FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS deployment_pools (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  UNIQUE(user_id, name),
  FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS deployment_targets (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  pool_id TEXT NOT NULL,
  credential_id TEXT NOT NULL,
  name TEXT NOT NULL,
  environment TEXT NOT NULL,
  namespace TEXT NOT NULL,
  allowed_repo_id TEXT,
  deploy_strategy TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  UNIQUE(pool_id, name),
  FOREIGN KEY(user_id) REFERENCES users(id),
  FOREIGN KEY(pool_id) REFERENCES deployment_pools(id),
  FOREIGN KEY(credential_id) REFERENCES k8s_credentials(id)
);

CREATE TABLE IF NOT EXISTS repo_deployment_configs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  repository_id TEXT NOT NULL,
  config_path TEXT NOT NULL DEFAULT 'jj.deploy.yaml',
  default_pool_id TEXT,
  default_target_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id, repository_id),
  FOREIGN KEY(user_id) REFERENCES users(id),
  FOREIGN KEY(repository_id) REFERENCES repositories(id)
);

CREATE TABLE IF NOT EXISTS deployments (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  repository_id TEXT NOT NULL,
  pool_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  status TEXT NOT NULL,
  strategy TEXT NOT NULL,
  git_branch TEXT,
  git_commit_sha TEXT,
  image_ref TEXT,
  manifest_path TEXT,
  namespace TEXT NOT NULL,
  plan_summary TEXT,
  diff_summary TEXT,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  error_code TEXT,
  error_message TEXT,
  FOREIGN KEY(user_id) REFERENCES users(id),
  FOREIGN KEY(repository_id) REFERENCES repositories(id),
  FOREIGN KEY(pool_id) REFERENCES deployment_pools(id),
  FOREIGN KEY(target_id) REFERENCES deployment_targets(id)
);

CREATE TABLE IF NOT EXISTS deployment_events (
  id TEXT PRIMARY KEY,
  deployment_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  message TEXT,
  metadata_json TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(deployment_id) REFERENCES deployments(id)
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id TEXT PRIMARY KEY,
  user_id TEXT,
  action TEXT NOT NULL,
  target_type TEXT,
  target_id TEXT,
  metadata_json TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(user_id) REFERENCES users(id)
);
`
