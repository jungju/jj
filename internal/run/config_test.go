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
		"planning_agents": 2
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
	if cfg.OpenAIAPIKeyEnv != "JJ_TEST_OPENAI_KEY" || cfg.OpenAIAPIKey != "sk-test-value" {
		t.Fatalf("unexpected API key resolution: %#v", cfg)
	}
	if cfg.ConfigFile != filepath.Join(dir, ".jjrc") {
		t.Fatalf("unexpected config file: %q", cfg.ConfigFile)
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
	t.Setenv("JJ_CODEX_BIN", "/tmp/env-codex")

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

func writeJJRC(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".jjrc"), []byte(data), 0o644); err != nil {
		t.Fatalf("write .jjrc: %v", err)
	}
}
