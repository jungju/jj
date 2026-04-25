package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigUsesJJRCValues(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"openai_api_key_env": "JJ_TEST_OPENAI_KEY",
		"openai_model": "file-openai",
		"codex_model": "file-codex",
		"codex_bin": "/tmp/file-codex",
		"planning_agents": 2,
		"spec_doc": "PRODUCT.md",
		"task_doc": "WORK.md",
		"eval_doc": "REVIEW.md",
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
	if cfg.SpecDoc != "PRODUCT.md" || cfg.TaskDoc != "WORK.md" || cfg.EvalDoc != "REVIEW.md" || !cfg.DryRun || !cfg.AllowNoGit {
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

func TestResolveConfigExplicitFlagsOverrideJJRCBooleansAndDocs(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"spec_path": "docs/PRODUCT.md",
		"task_path": "docs/WORK.md",
		"eval_path": "docs/REVIEW.md",
		"dry_run": true,
		"allow_no_git": true
	}`)

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir:    dir,
		PlanningAgents:     DefaultPlanningAgents,
		OpenAIModel:        defaultOpenAIModel,
		SpecDoc:            "custom/SPEC.md",
		TaskDoc:            "custom/TASK.md",
		EvalDoc:            "custom/EVAL.md",
		SpecDocExplicit:    true,
		TaskDocExplicit:    true,
		EvalDocExplicit:    true,
		DryRun:             false,
		AllowNoGit:         false,
		DryRunExplicit:     true,
		AllowNoGitExplicit: true,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.SpecDoc != "custom/SPEC.md" || cfg.TaskDoc != "custom/TASK.md" || cfg.EvalDoc != "custom/EVAL.md" || cfg.DryRun || cfg.AllowNoGit {
		t.Fatalf("explicit flags should override .jjrc defaults: %#v", cfg)
	}
}

func TestResolveConfigSpecPathUsesPathMode(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"spec_path": "SPEC.md",
		"task_path": "TASK.md",
		"eval_path": "EVAL.md"
	}`)

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir: dir,
		PlanningAgents:  DefaultPlanningAgents,
		OpenAIModel:     defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if !cfg.SpecDocPathMode || !cfg.TaskDocPathMode || !cfg.EvalDocPathMode {
		t.Fatalf("spec/task/eval path settings should use path mode: %#v", cfg)
	}
	if got := docRelPath(cfg.SpecDoc, cfg.SpecDocPathMode); got != "SPEC.md" {
		t.Fatalf("expected root spec path, got %q", got)
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
		"planning_agents": 2,
		"spec_path": "docs/FILE_SPEC.md",
		"task_path": "docs/FILE_TASK.md",
		"eval_path": "docs/FILE_EVAL.md",
		"dry_run": false,
		"allow_no_git": false
	}`)
	t.Setenv("JJ_CWD", "/env/workspace")
	t.Setenv("JJ_RUN_ID", "env-run")
	t.Setenv("JJ_PLANNING_AGENTS", "5")
	t.Setenv("JJ_SPEC_PATH", "docs/ENV_SPEC.md")
	t.Setenv("JJ_TASK_PATH", "docs/ENV_TASK.md")
	t.Setenv("JJ_EVAL_PATH", "docs/ENV_EVAL.md")
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
	if cfg.SpecDoc != "docs/ENV_SPEC.md" || cfg.TaskDoc != "docs/ENV_TASK.md" || cfg.EvalDoc != "docs/ENV_EVAL.md" {
		t.Fatalf("env should override document fields: %#v", cfg)
	}
	if !cfg.SpecDocPathMode || !cfg.TaskDocPathMode || !cfg.EvalDocPathMode || !cfg.DryRun || !cfg.AllowNoGit {
		t.Fatalf("env should set path mode and booleans: %#v", cfg)
	}
}

func TestResolveConfigExplicitFlagsOverrideEnvAndJJRC(t *testing.T) {
	dir := t.TempDir()
	writeJJRC(t, dir, `{
		"openai_model": "file-openai",
		"codex_model": "file-codex",
		"planning_agents": 2
	}`)
	t.Setenv("JJ_OPENAI_MODEL", "env-openai")
	t.Setenv("JJ_CODEX_MODEL", "env-codex")

	cfg, err := ResolveConfig(Config{
		ConfigSearchDir:        dir,
		PlanningAgents:         4,
		OpenAIModel:            "flag-openai",
		CodexModel:             "flag-codex",
		PlanningAgentsExplicit: true,
		OpenAIModelExplicit:    true,
		CodexModelExplicit:     true,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.PlanningAgents != 4 || cfg.OpenAIModel != "flag-openai" || cfg.CodexModel != "flag-codex" {
		t.Fatalf("explicit flags should override env and .jjrc: %#v", cfg)
	}
}

func TestResolveConfigAppliesDefaultDocumentNames(t *testing.T) {
	cfg, err := ResolveConfig(Config{
		PlanningAgents: DefaultPlanningAgents,
		OpenAIModel:    defaultOpenAIModel,
	})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.SpecDoc != DefaultSpecDoc || cfg.TaskDoc != DefaultTaskDoc || cfg.EvalDoc != DefaultEvalDoc {
		t.Fatalf("unexpected document defaults: %#v", cfg)
	}
	if cfg.CodexBin != DefaultCodexBinary {
		t.Fatalf("unexpected codex binary default: %#v", cfg)
	}
}

func TestValidateResolvedConfigAcceptsWorkspaceRelativeDocumentPaths(t *testing.T) {
	base := Config{
		PlanningAgents: DefaultPlanningAgents,
		OpenAIModel:    defaultOpenAIModel,
		SpecDoc:        DefaultSpecDoc,
		TaskDoc:        DefaultTaskDoc,
		EvalDoc:        DefaultEvalDoc,
	}
	for _, docPath := range []string{"SPEC.md", "docs/SPEC.md", "foo/SPEC.md"} {
		cfg := base
		cfg.SpecDoc = docPath
		if err := validateResolvedConfig(cfg); err != nil {
			t.Fatalf("expected valid spec path %q, got %v", docPath, err)
		}
	}
}

func TestValidateResolvedConfigRejectsUnsafeDocumentPaths(t *testing.T) {
	base := Config{
		PlanningAgents: DefaultPlanningAgents,
		OpenAIModel:    defaultOpenAIModel,
		SpecDoc:        DefaultSpecDoc,
		TaskDoc:        DefaultTaskDoc,
		EvalDoc:        DefaultEvalDoc,
	}
	for _, docName := range []string{"../SPEC.md", "/tmp/SPEC.md", "docs/../SPEC.md", "SPEC.txt", `foo\SPEC.md`} {
		cfg := base
		cfg.SpecDoc = docName
		if err := validateResolvedConfig(cfg); err == nil {
			t.Fatalf("expected invalid spec doc %q to fail", docName)
		}
	}
}

func TestDocRelPathDistinguishesPathAndLegacyDocModes(t *testing.T) {
	if got := docRelPath("SPEC.md", true); got != "SPEC.md" {
		t.Fatalf("path mode should preserve root-relative path, got %q", got)
	}
	if got := docRelPath("SPEC.md", false); got != "docs/SPEC.md" {
		t.Fatalf("legacy doc mode should place bare names under docs, got %q", got)
	}
	if got := docRelPath("docs/SPEC.md", false); got != "docs/SPEC.md" {
		t.Fatalf("legacy doc mode should preserve explicit docs path, got %q", got)
	}
}

func writeJJRC(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".jjrc"), []byte(data), 0o644); err != nil {
		t.Fatalf("write .jjrc: %v", err)
	}
}
