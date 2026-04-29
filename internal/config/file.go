package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jungju/jj/internal/security"
)

const FileName = ".jjrc"

type File struct {
	CWD              string `json:"cwd"`
	RunID            string `json:"run_id"`
	OpenAIAPIKeyEnv  string `json:"openai_api_key_env"`
	OpenAIModel      string `json:"openai_model"`
	CodexModel       string `json:"codex_model"`
	CodexBin         string `json:"codex_bin"`
	CodexBinary      string `json:"codex_binary"`
	TaskProposalMode string `json:"task_proposal_mode"`
	RepoURL          string `json:"repo"`
	RepoDir          string `json:"repo_dir"`
	BaseBranch       string `json:"base_branch"`
	WorkBranch       string `json:"work_branch"`
	Push             *bool  `json:"push"`
	PushMode         string `json:"push_mode"`
	AutoPR           *bool  `json:"auto_pr"`
	GitHubTokenEnv   string `json:"github_token_env"`
	RepoAllowDirty   *bool  `json:"allow_dirty"`
	PlanningAgents   *int   `json:"planning_agents"`
	DryRun           *bool  `json:"dry_run"`
	AllowNoGit       *bool  `json:"allow_no_git"`
	ServeAddr        string `json:"serve_addr"`
	ServeHost        string `json:"serve_host"`
	ServePort        *int   `json:"serve_port"`

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
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("open config file %s: %w", path, err)
	}
	security.RegisterSensitiveConfigJSON(data)

	dec := json.NewDecoder(bytes.NewReader(data))
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
