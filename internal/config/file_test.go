package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromFindsConfigInParent(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	configPath := filepath.Join(root, FileName)
	if err := os.WriteFile(configPath, []byte(`{
		"openai_api_key_env": "JJ_TEST_OPENAI_KEY",
		"openai_model": "model-from-file",
		"codex_model": "codex-from-file",
		"codex_bin": "/tmp/codex",
		"planning_agents": 2
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, found, err := LoadFrom(child)
	if err != nil {
		t.Fatalf("load from child: %v", err)
	}
	if !found {
		t.Fatal("expected config to be found")
	}
	if cfg.Path != configPath || cfg.OpenAIAPIKeyEnv != "JJ_TEST_OPENAI_KEY" || cfg.OpenAIModel != "model-from-file" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.PlanningAgents == nil || *cfg.PlanningAgents != 2 {
		t.Fatalf("unexpected planning agents: %#v", cfg.PlanningAgents)
	}
}

func TestLoadFromMissingConfig(t *testing.T) {
	_, found, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if found {
		t.Fatal("expected no config")
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"openai_model":`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "parse config file") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"openai_api_key": "sk-secret"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadRejectsMultipleJSONValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{} {}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "single JSON object") {
		t.Fatalf("expected single object error, got %v", err)
	}
}
