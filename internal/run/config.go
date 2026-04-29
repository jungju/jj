package run

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	jjconfig "github.com/jungju/jj/internal/config"
)

const DefaultPlanningAgents = 3
const defaultOpenAIModel = "gpt-5.5"
const DefaultCodexBinary = "codex"

type Config struct {
	PlanPath              string
	PlanText              string
	PlanInputName         string
	CWD                   string
	RunID                 string
	PlanningAgents        int
	OpenAIModel           string
	CodexModel            string
	CodexBin              string
	TaskProposalMode      TaskProposalMode
	RepoURL               string
	RepoDir               string
	BaseBranch            string
	WorkBranch            string
	Push                  bool
	PushMode              string
	AutoPR                bool
	GitHubTokenEnv        string
	RepoAllowDirty        bool
	AllowNoGit            bool
	DryRun                bool
	AdditionalPlanContext string
	CommitOnSuccess       bool
	CommitMessage         string
	LoopEnabled           bool
	LoopBaseRunID         string
	LoopTurn              int
	LoopMaxTurns          int
	LoopPreviousRunID     string

	ConfigSearchDir string
	ConfigFile      string
	OpenAIAPIKey    string
	OpenAIAPIKeyEnv string

	PlanningAgentsExplicit   bool
	CWDExplicit              bool
	RunIDExplicit            bool
	OpenAIModelExplicit      bool
	CodexModelExplicit       bool
	CodexBinExplicit         bool
	TaskProposalModeExplicit bool
	RepoURLExplicit          bool
	RepoDirExplicit          bool
	BaseBranchExplicit       bool
	WorkBranchExplicit       bool
	PushExplicit             bool
	PushModeExplicit         bool
	AutoPRExplicit           bool
	GitHubTokenEnvExplicit   bool
	RepoAllowDirtyExplicit   bool
	DryRunExplicit           bool
	AllowNoGitExplicit       bool

	Stdout io.Writer
	Stderr io.Writer

	Planner      PlanningClient
	CodexRunner  CodexRunner
	GitRunner    GitRunner
	GitHubClient GitHubPRClient

	PlannerCodexRunner CodexRunner
}

func DefaultOpenAIModel() string {
	if v := os.Getenv("JJ_OPENAI_MODEL"); v != "" {
		return v
	}
	return defaultOpenAIModel
}

func ResolveConfig(cfg Config) (Config, error) {
	fileCfg, err := loadProjectConfig(cfg)
	if err != nil {
		return cfg, err
	}
	if strings.TrimSpace(fileCfg.Path) != "" {
		cfg.ConfigFile = fileCfg.Path
	}

	if !cfg.CWDExplicit {
		if v := firstEnv("JJ_CWD", "JJ_WORKSPACE_CWD"); v != "" {
			cfg.CWD = v
		} else if v := strings.TrimSpace(fileCfg.CWD); v != "" {
			cfg.CWD = v
		}
	}
	if !cfg.RunIDExplicit {
		if v := firstEnv("JJ_RUN_ID", "JJ_RUNID"); v != "" {
			cfg.RunID = v
		} else if v := strings.TrimSpace(fileCfg.RunID); v != "" {
			cfg.RunID = v
		}
	}
	if !cfg.PlanningAgentsExplicit {
		if v := firstEnv("JJ_PLANNING_AGENTS", "JJ_PLANNER_AGENTS", "JJ_AGENT_COUNT", "JJ_AGENTS"); v != "" {
			parsed, err := strconv.Atoi(v)
			if err != nil {
				return cfg, fmt.Errorf("parse JJ_PLANNING_AGENTS: %w", err)
			}
			cfg.PlanningAgents = parsed
		} else if fileCfg.PlanningAgents != nil {
			cfg.PlanningAgents = *fileCfg.PlanningAgents
		}
	}
	if !cfg.DryRunExplicit {
		if v := firstEnv("JJ_DRY_RUN"); v != "" {
			parsed, err := parseBool(v)
			if err != nil {
				return cfg, fmt.Errorf("parse JJ_DRY_RUN: %w", err)
			}
			cfg.DryRun = parsed
		} else if fileCfg.DryRun != nil {
			cfg.DryRun = *fileCfg.DryRun
		}
	}
	if !cfg.AllowNoGitExplicit {
		if v := firstEnv("JJ_ALLOW_NO_GIT", "JJ_ALLOW_NOGIT"); v != "" {
			parsed, err := parseBool(v)
			if err != nil {
				return cfg, fmt.Errorf("parse JJ_ALLOW_NO_GIT: %w", err)
			}
			cfg.AllowNoGit = parsed
		} else if fileCfg.AllowNoGit != nil {
			cfg.AllowNoGit = *fileCfg.AllowNoGit
		}
	}
	if !cfg.OpenAIModelExplicit {
		if v := strings.TrimSpace(os.Getenv("JJ_OPENAI_MODEL")); v != "" {
			cfg.OpenAIModel = v
		} else if v := strings.TrimSpace(fileCfg.OpenAIModel); v != "" {
			cfg.OpenAIModel = v
		} else if strings.TrimSpace(cfg.OpenAIModel) == "" {
			cfg.OpenAIModel = defaultOpenAIModel
		}
	}
	if !cfg.CodexModelExplicit {
		if v := strings.TrimSpace(os.Getenv("JJ_CODEX_MODEL")); v != "" {
			cfg.CodexModel = v
		} else if v := strings.TrimSpace(fileCfg.CodexModel); v != "" {
			cfg.CodexModel = v
		}
	}
	if !cfg.CodexBinExplicit {
		if v := firstEnv("JJ_CODEX_BIN", "JJ_CODEX_BINARY"); v != "" {
			cfg.CodexBin = v
		} else if v := strings.TrimSpace(fileCfg.CodexBin); v != "" {
			cfg.CodexBin = v
		} else if v := strings.TrimSpace(fileCfg.CodexBinary); v != "" {
			cfg.CodexBin = v
		}
	}
	if strings.TrimSpace(cfg.CodexBin) == "" {
		cfg.CodexBin = DefaultCodexBinary
	}
	if !cfg.TaskProposalModeExplicit {
		if v := firstEnv("JJ_TASK_PROPOSAL_MODE", "JJ_PROPOSAL_MODE"); v != "" {
			mode, err := ParseTaskProposalMode(v)
			if err != nil {
				return cfg, err
			}
			cfg.TaskProposalMode = mode
		} else if v := strings.TrimSpace(fileCfg.TaskProposalMode); v != "" {
			mode, err := ParseTaskProposalMode(v)
			if err != nil {
				return cfg, err
			}
			cfg.TaskProposalMode = mode
		} else if strings.TrimSpace(string(cfg.TaskProposalMode)) == "" {
			cfg.TaskProposalMode = TaskProposalModeAuto
		}
	}
	if !cfg.RepoURLExplicit {
		if v := firstEnv("JJ_REPO", "JJ_REPOSITORY", "JJ_REPO_URL"); v != "" {
			cfg.RepoURL = v
		} else if v := strings.TrimSpace(fileCfg.RepoURL); v != "" {
			cfg.RepoURL = v
		}
	}
	if !cfg.RepoDirExplicit {
		if v := firstEnv("JJ_REPO_DIR"); v != "" {
			cfg.RepoDir = v
		} else if v := strings.TrimSpace(fileCfg.RepoDir); v != "" {
			cfg.RepoDir = v
		}
	}
	if !cfg.BaseBranchExplicit {
		if v := firstEnv("JJ_BASE_BRANCH"); v != "" {
			cfg.BaseBranch = v
		} else if v := strings.TrimSpace(fileCfg.BaseBranch); v != "" {
			cfg.BaseBranch = v
		}
	}
	if !cfg.WorkBranchExplicit {
		if v := firstEnv("JJ_WORK_BRANCH"); v != "" {
			cfg.WorkBranch = v
		} else if v := strings.TrimSpace(fileCfg.WorkBranch); v != "" {
			cfg.WorkBranch = v
		}
	}
	if !cfg.PushExplicit {
		if v := firstEnv("JJ_PUSH"); v != "" {
			parsed, err := parseBool(v)
			if err != nil {
				return cfg, fmt.Errorf("parse JJ_PUSH: %w", err)
			}
			cfg.Push = parsed
		} else if fileCfg.Push != nil {
			cfg.Push = *fileCfg.Push
		}
	}
	if !cfg.PushModeExplicit {
		if v := firstEnv("JJ_PUSH_MODE"); v != "" {
			cfg.PushMode = v
		} else if v := strings.TrimSpace(fileCfg.PushMode); v != "" {
			cfg.PushMode = v
		} else if strings.TrimSpace(cfg.PushMode) == "" {
			cfg.PushMode = DefaultPushMode
		}
	}
	if !cfg.AutoPRExplicit {
		if v := firstEnv("JJ_AUTO_PR", "JJ_AUTOPR"); v != "" {
			parsed, err := parseBool(v)
			if err != nil {
				return cfg, fmt.Errorf("parse JJ_AUTO_PR: %w", err)
			}
			cfg.AutoPR = parsed
		} else if fileCfg.AutoPR != nil {
			cfg.AutoPR = *fileCfg.AutoPR
		}
	}
	if !cfg.GitHubTokenEnvExplicit {
		if v := firstEnv("JJ_GITHUB_TOKEN_ENV"); v != "" {
			cfg.GitHubTokenEnv = v
			cfg.GitHubTokenEnvExplicit = true
		} else if v := strings.TrimSpace(fileCfg.GitHubTokenEnv); v != "" {
			cfg.GitHubTokenEnv = v
			cfg.GitHubTokenEnvExplicit = true
		} else if strings.TrimSpace(cfg.GitHubTokenEnv) == "" {
			cfg.GitHubTokenEnv = DefaultGitHubTokenEnv
		}
	}
	if !cfg.RepoAllowDirtyExplicit {
		if v := firstEnv("JJ_REPO_ALLOW_DIRTY", "JJ_ALLOW_DIRTY"); v != "" {
			parsed, err := parseBool(v)
			if err != nil {
				return cfg, fmt.Errorf("parse JJ_ALLOW_DIRTY: %w", err)
			}
			cfg.RepoAllowDirty = parsed
		} else if fileCfg.RepoAllowDirty != nil {
			cfg.RepoAllowDirty = *fileCfg.RepoAllowDirty
		}
	}
	if strings.TrimSpace(cfg.OpenAIAPIKeyEnv) == "" {
		if v := firstEnv("JJ_OPENAI_API_KEY_ENV"); v != "" {
			cfg.OpenAIAPIKeyEnv = v
		} else if v := strings.TrimSpace(fileCfg.OpenAIAPIKeyEnv); v != "" {
			cfg.OpenAIAPIKeyEnv = v
		} else {
			cfg.OpenAIAPIKeyEnv = "OPENAI_API_KEY"
		}
	}
	if err := validateEnvVarName("OpenAI API key env", cfg.OpenAIAPIKeyEnv); err != nil {
		return cfg, err
	}
	if strings.TrimSpace(cfg.OpenAIAPIKey) == "" {
		cfg.OpenAIAPIKey = os.Getenv(cfg.OpenAIAPIKeyEnv)
	}
	mode, err := ParseTaskProposalMode(string(cfg.TaskProposalMode))
	if err != nil {
		return cfg, err
	}
	cfg.TaskProposalMode = mode
	if strings.TrimSpace(cfg.PushMode) == "" {
		cfg.PushMode = DefaultPushMode
	}
	if err := validatePushMode(cfg.PushMode); err != nil {
		return cfg, err
	}
	if strings.TrimSpace(cfg.GitHubTokenEnv) == "" {
		cfg.GitHubTokenEnv = DefaultGitHubTokenEnv
	}
	if err := validateEnvVarName("GitHub token env", cfg.GitHubTokenEnv); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func loadProjectConfig(cfg Config) (jjconfig.File, error) {
	if strings.TrimSpace(cfg.ConfigFile) != "" {
		return jjconfig.Load(cfg.ConfigFile)
	}
	if cfg.CWDExplicit && strings.TrimSpace(cfg.CWD) != "" {
		if info, statErr := os.Stat(cfg.CWD); statErr == nil && info.IsDir() {
			fileCfg, found, err := jjconfig.LoadFrom(cfg.CWD)
			if err != nil || found {
				return fileCfg, err
			}
		}
	}
	if !cfg.CWDExplicit {
		if envCWD := firstEnv("JJ_CWD", "JJ_WORKSPACE_CWD"); envCWD != "" {
			if info, statErr := os.Stat(envCWD); statErr == nil && info.IsDir() {
				fileCfg, found, err := jjconfig.LoadFrom(envCWD)
				if err != nil || found {
					return fileCfg, err
				}
			}
		}
	}
	if strings.TrimSpace(cfg.ConfigSearchDir) == "" {
		if strings.TrimSpace(cfg.CWD) == "" {
			return jjconfig.File{}, nil
		}
		fileCfg, _, err := jjconfig.LoadFrom(cfg.CWD)
		return fileCfg, err
	}
	fileCfg, found, err := jjconfig.LoadFrom(cfg.ConfigSearchDir)
	if err != nil {
		return jjconfig.File{}, err
	}
	if found {
		return fileCfg, nil
	}
	if strings.TrimSpace(cfg.CWD) != "" {
		if info, statErr := os.Stat(cfg.CWD); statErr == nil && info.IsDir() {
			fileCfg, _, err := jjconfig.LoadFrom(cfg.CWD)
			return fileCfg, err
		}
	}
	return jjconfig.File{}, nil
}

func validateResolvedConfig(cfg Config) error {
	if cfg.PlanningAgents <= 0 {
		return fmt.Errorf("planning-agents must be greater than zero")
	}
	if strings.TrimSpace(cfg.OpenAIModel) == "" {
		return fmt.Errorf("openai model is required")
	}
	mode, err := ParseTaskProposalMode(string(cfg.TaskProposalMode))
	if err != nil {
		return err
	}
	cfg.TaskProposalMode = mode
	return nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", raw)
	}
}

func validateEnvVarName(label, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%s is required", label)
	}
	for i, r := range name {
		valid := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9')
		if !valid {
			return fmt.Errorf("%s must be an environment variable name", label)
		}
	}
	first := rune(name[0])
	if first >= '0' && first <= '9' {
		return fmt.Errorf("%s must be an environment variable name", label)
	}
	return nil
}
