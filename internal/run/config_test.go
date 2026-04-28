package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigUsesJJRCValues(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"openai_api_key_env": "JJ_TEST_OPENAI_KEY",
		"openai_model": "file-openai",
		"codex_model": "file-codex",
		"codex_bin": "/tmp/file-codex",
		"task_proposal_mode": "security",
		"repo": "https://github.com/acme/app.git",
		"repo_dir": "/tmp/acme-app",
		"base_branch": "main",
		"work_branch": "jj/file-work",
		"push": true,
		"push_mode": "branch",
		"github_token_env": "MY_GITHUB_TOKEN",
		"allow_dirty": true,
		"planning_agents": 2,
		"dry_run": true,
		"allow_no_git": true
	}`)
	t.Setenv("JJ_TEST_OPENAI_KEY", "sk-test-value")
	t.Setenv("JJ_OPENAI_MODEL", "")
	t.Setenv("JJ_CODEX_MODEL", "")
	t.Setenv("JJ_CODEX_BIN", "")

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir: dir,
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.PlanningAgents != 2 || cfg.OpenAIModel != "file-openai" || cfg.CodexModel != "file-codex" || cfg.CodexBin != "/tmp/file-codex" {
		t.Fatalf("unexpected resolved config: %#v", cfg)
	}
	if cfg.TaskProposalMode != TaskProposalModeSecurity {
		t.Fatalf("unexpected task proposal mode: %#v", cfg)
	}
	if cfg.RepoURL != "https://github.com/acme/app.git" || cfg.RepoDir != "/tmp/acme-app" || cfg.BaseBranch != "main" || cfg.WorkBranch != "jj/file-work" {
		t.Fatalf("unexpected repository config: %#v", cfg)
	}
	if !cfg.Push || cfg.PushMode != PushModeBranch || cfg.GitHubTokenEnv != "MY_GITHUB_TOKEN" || !cfg.RepoAllowDirty {
		t.Fatalf("unexpected repository policy config: %#v", cfg)
	}
	if !cfg.GitHubTokenEnvExplicit {
		t.Fatalf("configured github_token_env should lock token env selection: %#v", cfg)
	}
	if !cfg.DryRun || !cfg.AllowNoGit {
		t.Fatalf("unexpected file defaults: %#v", cfg)
	}
	if cfg.OpenAIAPIKeyEnv != "JJ_TEST_OPENAI_KEY" || cfg.OpenAIAPIKey != "sk-test-value" {
		t.Fatalf("unexpected API key resolution: %#v", cfg)
	}
	if cfg.ConfigFile != filepath.Join(dir, ".jjrc") {
		t.Fatalf("unexpected config file: %q", cfg.ConfigFile)
	}
}

func TestResolveConfigPrefersTargetCWDJJRCWhenExplicit(t *testing.T) {
	invocation := t.TempDir()
	target := t.TempDir()
	writeJJRC(t, invocation, `{"openai_model":"invocation-model"}`)
	writeJJRC(t, target, `{"openai_model":"target-model"}`)

	cfg, err := ResolveConfig(Config{
		CWD:             target,
		CWDExplicit:     true,
		ConfigSearchDir: invocation,
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.OpenAIModel != "target-model" || cfg.ConfigFile != filepath.Join(target, ".jjrc") {
		t.Fatalf("expected target .jjrc to win, got %#v", cfg)
	}
}

func TestResolveConfigUsesEnvTargetCWDJJRC(t *testing.T) {
	invocation := t.TempDir()
	target := t.TempDir()
	writeJJRC(t, target, `{"openai_model":"target-env-model"}`)
	t.Setenv("JJ_CWD", target)

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir: invocation,
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.CWD != target || cfg.OpenAIModel != "target-env-model" || cfg.ConfigFile != filepath.Join(target, ".jjrc") {
		t.Fatalf("expected env target .jjrc to apply, got %#v", cfg)
	}
}

func TestResolveConfigExplicitFlagsOverrideJJRCBooleans(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"dry_run": true,
		"allow_no_git": true
	}`)

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir:    dir,
		PlanningAgents:     DefaultPlanningAgents,
		OpenAIModel:        defaultOpenAIModel,
		DryRun:             false,
		AllowNoGit:         false,
		DryRunExplicit:     true,
		AllowNoGitExplicit: true,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.DryRun || cfg.AllowNoGit {
		t.Fatalf("explicit flags should override .jjrc defaults: %#v", cfg)
	}
}

func TestResolveConfigEnvOverridesJJRC(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"openai_model": "file-openai",
		"codex_model": "file-codex",
		"codex_bin": "/tmp/file-codex"
	}`)
	t.Setenv("JJ_OPENAI_MODEL", "env-openai")
	t.Setenv("JJ_CODEX_MODEL", "env-codex")
	t.Setenv("JJ_CODEX_BIN", "")
	t.Setenv("JJ_CODEX_BINARY", "/tmp/env-codex")

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir: dir,
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.OpenAIModel != "env-openai" || cfg.CodexModel != "env-codex" || cfg.CodexBin != "/tmp/env-codex" {
		t.Fatalf("env should override .jjrc: %#v", cfg)
	}
}

func TestResolveConfigEnvOverridesJJRCForWorkflowFields(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"cwd": "/file/workspace",
		"run_id": "file-run",
		"task_proposal_mode": "docs",
		"repo": "https://github.com/file/app.git",
		"repo_dir": "/file/repo",
		"base_branch": "file-main",
		"work_branch": "jj/file-work",
		"push": false,
		"push_mode": "none",
		"github_token_env": "FILE_GITHUB_TOKEN",
		"allow_dirty": false,
		"planning_agents": 2,
		"dry_run": false,
		"allow_no_git": false
	}`)
	t.Setenv("JJ_CWD", "/env/workspace")
	t.Setenv("JJ_RUN_ID", "env-run")
	t.Setenv("JJ_TASK_PROPOSAL_MODE", "quality")
	t.Setenv("JJ_REPO", "https://github.com/env/app.git")
	t.Setenv("JJ_REPO_DIR", "/env/repo")
	t.Setenv("JJ_BASE_BRANCH", "env-main")
	t.Setenv("JJ_WORK_BRANCH", "jj/env-work")
	t.Setenv("JJ_PUSH", "true")
	t.Setenv("JJ_PUSH_MODE", "branch")
	t.Setenv("JJ_GITHUB_TOKEN_ENV", "ENV_GITHUB_TOKEN")
	t.Setenv("JJ_REPO_ALLOW_DIRTY", "true")
	t.Setenv("JJ_PLANNING_AGENTS", "5")
	t.Setenv("JJ_DRY_RUN", "true")
	t.Setenv("JJ_ALLOW_NO_GIT", "true")

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir: dir,
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.CWD != "/env/workspace" || cfg.RunID != "env-run" || cfg.PlanningAgents != 5 {
		t.Fatalf("env should override workflow fields: %#v", cfg)
	}
	if cfg.TaskProposalMode != TaskProposalModeQuality {
		t.Fatalf("env should override task proposal mode: %#v", cfg)
	}
	if cfg.RepoURL != "https://github.com/env/app.git" || cfg.RepoDir != "/env/repo" || cfg.BaseBranch != "env-main" || cfg.WorkBranch != "jj/env-work" {
		t.Fatalf("env should override repository fields: %#v", cfg)
	}
	if !cfg.Push || cfg.PushMode != PushModeBranch || cfg.GitHubTokenEnv != "ENV_GITHUB_TOKEN" || !cfg.RepoAllowDirty {
		t.Fatalf("env should override repository policy fields: %#v", cfg)
	}
	if !cfg.GitHubTokenEnvExplicit {
		t.Fatalf("JJ_GITHUB_TOKEN_ENV should lock token env selection: %#v", cfg)
	}
	if !cfg.DryRun || !cfg.AllowNoGit {
		t.Fatalf("env should set booleans: %#v", cfg)
	}
}

func TestResolveConfigExplicitFlagsOverrideEnvAndJJRC(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"openai_model": "file-openai",
		"codex_model": "file-codex",
		"task_proposal_mode": "security",
		"planning_agents": 2
	}`)
	t.Setenv("JJ_OPENAI_MODEL", "env-openai")
	t.Setenv("JJ_CODEX_MODEL", "env-codex")
	t.Setenv("JJ_TASK_PROPOSAL_MODE", "docs")

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir:          dir,
		PlanningAgents:           4,
		OpenAIModel:              "flag-openai",
		CodexModel:               "flag-codex",
		TaskProposalMode:         TaskProposalModeHardening,
		PlanningAgentsExplicit:   true,
		OpenAIModelExplicit:      true,
		CodexModelExplicit:       true,
		TaskProposalModeExplicit: true,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.PlanningAgents != 4 || cfg.OpenAIModel != "flag-openai" || cfg.CodexModel != "flag-codex" {
		t.Fatalf("explicit flags should override env and .jjrc: %#v", cfg)
	}
	if cfg.TaskProposalMode != TaskProposalModeHardening {
		t.Fatalf("explicit task proposal mode should override env and .jjrc: %#v", cfg)
	}
}

func TestResolveConfigAppliesDefaults(t *testing.T) {
	cfg, err := ResolveConfig(Config{
		PlanningAgents: DefaultPlanningAgents,
		OpenAIModel:    defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.CodexBin != DefaultCodexBinary {
		t.Fatalf("unexpected codex binary default: %#v", cfg)
	}
	if cfg.TaskProposalMode != TaskProposalModeAuto {
		t.Fatalf("unexpected task proposal mode default: %#v", cfg)
	}
}

func TestResolveConfigRejectsSecretLikeEnvNameMetadata(t *testing.T) {
	dir := t.TempDir()
	secret := "literal-jjrc-secret"
	writeJJRC(t, dir, `{"openai_api_key_env":"`+secret+`"}`)

	_, err := ResolveConfig(Config{
		ConfigSearchDir: dir,
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
	})
	if err == nil || !strings.Contains(err.Error(), "OpenAI API key env must be an environment variable name") {
		t.Fatalf("expected safe env-name validation error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("env-name validation leaked configured value: %v", err)
	}
}

func writeJJRC(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".jjrc"), []byte(data), 0o644); err != nil {
		t.Fatalf("write .jjrc: %v", err)
	}
}
