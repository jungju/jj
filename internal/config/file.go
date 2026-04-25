package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const FileName = ".jjrc"

type File struct {
	CWD             string `json:"cwd"`
	RunID           string `json:"run_id"`
	OpenAIAPIKeyEnv string `json:"openai_api_key_env"`
	OpenAIModel     string `json:"openai_model"`
	CodexModel      string `json:"codex_model"`
	CodexBin        string `json:"codex_bin"`
	CodexBinary     string `json:"codex_binary"`
	PlanningAgents  *int   `json:"planning_agents"`
	SpecPath        string `json:"spec_path"`
	TaskPath        string `json:"task_path"`
	EvalPath        string `json:"eval_path"`
	SpecDoc         string `json:"spec_doc"`
	TaskDoc         string `json:"task_doc"`
	EvalDoc         string `json:"eval_doc"`
	DryRun          *bool  `json:"dry_run"`
	AllowNoGit      *bool  `json:"allow_no_git"`
	ServeAddr       string `json:"serve_addr"`
	ServeHost       string `json:"serve_host"`
	ServePort       *int   `json:"serve_port"`

	Path string `json:"-"`
}

func LoadFrom(startDir string) (File, bool, error) {
	path, found, err := Find(startDir)
	if err != nil || !found {
		return File{}, found, err
	}
	cfg, err := Load(path)
	return cfg, true, err
}

func Find(startDir string) (string, bool, error) {
	if strings.TrimSpace(startDir) == "" {
		return "", false, nil
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", false, fmt.Errorf("stat config search directory: %w", err)
	}
	if !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		candidate := filepath.Join(dir, FileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", false, fmt.Errorf("stat config file %s: %w", candidate, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

func Load(path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("open config file %s: %w", path, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var cfg File
	if err := dec.Decode(&cfg); err != nil {
		return File{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return File{}, fmt.Errorf("parse config file %s: expected a single JSON object", path)
		}
		return File{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}
