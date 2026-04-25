package run

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	jjconfig "github.com/jungju/jj/internal/config"
)

const DefaultPlanningAgents = 3
const defaultOpenAIModel = "gpt-5.5"
const defaultDocsDir = "docs"
const DefaultCodexBinary = "codex"

const (
	DefaultSpecPath = "docs/SPEC.md"
	DefaultTaskPath = "docs/TASK.md"
	DefaultEvalPath = "docs/EVAL.md"

	// Default*Doc remain exported for older callers; values are now paths.
	DefaultSpecDoc = DefaultSpecPath
	DefaultTaskDoc = DefaultTaskPath
	DefaultEvalDoc = DefaultEvalPath
)

type Config struct {
	PlanPath              string
	CWD                   string
	RunID                 string
	PlanningAgents        int
	OpenAIModel           string
	CodexModel            string
	CodexBin              string
	AllowNoGit            bool
	DryRun                bool
	SpecDoc               string
	TaskDoc               string
	EvalDoc               string
	SpecDocPathMode       bool
	TaskDocPathMode       bool
	EvalDocPathMode       bool
	AdditionalPlanContext string
	CommitOnSuccess       bool
	CommitMessage         string

	ConfigSearchDir string
	ConfigFile      string
	OpenAIAPIKey    string
	OpenAIAPIKeyEnv string

	PlanningAgentsExplicit bool
	CWDExplicit            bool
	RunIDExplicit          bool
	OpenAIModelExplicit    bool
	CodexModelExplicit     bool
	CodexBinExplicit       bool
	SpecDocExplicit        bool
	TaskDocExplicit        bool
	EvalDocExplicit        bool
	DryRunExplicit         bool
	AllowNoGitExplicit     bool

	Stdout io.Writer
	Stderr io.Writer

	Planner     PlanningClient
	CodexRunner CodexRunner
	GitRunner   GitRunner

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
	if strings.TrimSpace(cfg.OpenAIAPIKeyEnv) == "" {
		if v := firstEnv("JJ_OPENAI_API_KEY_ENV"); v != "" {
			cfg.OpenAIAPIKeyEnv = v
		} else if v := strings.TrimSpace(fileCfg.OpenAIAPIKeyEnv); v != "" {
			cfg.OpenAIAPIKeyEnv = v
		} else {
			cfg.OpenAIAPIKeyEnv = "OPENAI_API_KEY"
		}
	}
	if strings.TrimSpace(cfg.OpenAIAPIKey) == "" {
		cfg.OpenAIAPIKey = os.Getenv(cfg.OpenAIAPIKeyEnv)
	}
	if !cfg.SpecDocExplicit {
		if v := firstEnv("JJ_SPEC_PATH"); v != "" {
			cfg.SpecDoc = v
			cfg.SpecDocPathMode = true
		} else if v := firstEnv("JJ_SPEC_DOC"); v != "" {
			cfg.SpecDoc = v
			cfg.SpecDocPathMode = false
		} else if strings.TrimSpace(fileCfg.SpecPath) != "" {
			cfg.SpecDoc = fileCfg.SpecPath
			cfg.SpecDocPathMode = true
		} else if strings.TrimSpace(fileCfg.SpecDoc) != "" {
			cfg.SpecDoc = fileCfg.SpecDoc
			cfg.SpecDocPathMode = false
		}
	}
	if !cfg.TaskDocExplicit {
		if v := firstEnv("JJ_TASK_PATH"); v != "" {
			cfg.TaskDoc = v
			cfg.TaskDocPathMode = true
		} else if v := firstEnv("JJ_TASK_DOC"); v != "" {
			cfg.TaskDoc = v
			cfg.TaskDocPathMode = false
		} else if strings.TrimSpace(fileCfg.TaskPath) != "" {
			cfg.TaskDoc = fileCfg.TaskPath
			cfg.TaskDocPathMode = true
		} else if strings.TrimSpace(fileCfg.TaskDoc) != "" {
			cfg.TaskDoc = fileCfg.TaskDoc
			cfg.TaskDocPathMode = false
		}
	}
	if !cfg.EvalDocExplicit {
		if v := firstEnv("JJ_EVAL_PATH"); v != "" {
			cfg.EvalDoc = v
			cfg.EvalDocPathMode = true
		} else if v := firstEnv("JJ_EVAL_DOC"); v != "" {
			cfg.EvalDoc = v
			cfg.EvalDocPathMode = false
		} else if strings.TrimSpace(fileCfg.EvalPath) != "" {
			cfg.EvalDoc = fileCfg.EvalPath
			cfg.EvalDocPathMode = true
		} else if strings.TrimSpace(fileCfg.EvalDoc) != "" {
			cfg.EvalDoc = fileCfg.EvalDoc
			cfg.EvalDocPathMode = false
		}
	}
	cfg = applyDocumentDefaults(cfg)
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
	if err := validateDocPath("spec-path", cfg.SpecDoc); err != nil {
		return err
	}
	if err := validateDocPath("task-path", cfg.TaskDoc); err != nil {
		return err
	}
	if err := validateDocPath("eval-path", cfg.EvalDoc); err != nil {
		return err
	}
	return nil
}

func applyDocumentDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.SpecDoc) == "" {
		cfg.SpecDoc = DefaultSpecPath
	} else {
		cfg.SpecDoc = strings.TrimSpace(cfg.SpecDoc)
	}
	if strings.TrimSpace(cfg.TaskDoc) == "" {
		cfg.TaskDoc = DefaultTaskPath
	} else {
		cfg.TaskDoc = strings.TrimSpace(cfg.TaskDoc)
	}
	if strings.TrimSpace(cfg.EvalDoc) == "" {
		cfg.EvalDoc = DefaultEvalPath
	} else {
		cfg.EvalDoc = strings.TrimSpace(cfg.EvalDoc)
	}
	return cfg
}

func validateDocPath(flagName, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%s is required", flagName)
	}
	if filepath.IsAbs(path) || strings.Contains(path, `\`) {
		return fmt.Errorf("%s must be a workspace-relative path: %s", flagName, path)
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay inside the workspace: %s", flagName, path)
	}
	if filepath.ToSlash(clean) != filepath.ToSlash(path) {
		return fmt.Errorf("%s must be a clean workspace-relative path: %s", flagName, path)
	}
	if !isMarkdownLikePath(path) {
		return fmt.Errorf("%s must be Markdown-like (.md or .markdown): %s", flagName, path)
	}
	return nil
}

func docRelPath(name string, pathMode bool) string {
	name = strings.TrimSpace(name)
	clean := filepath.Clean(name)
	if pathMode {
		return filepath.ToSlash(clean)
	}
	if strings.Contains(clean, string(filepath.Separator)) || strings.Contains(name, "/") {
		return filepath.ToSlash(clean)
	}
	return filepath.ToSlash(filepath.Join(defaultDocsDir, clean))
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
