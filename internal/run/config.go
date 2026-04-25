package run

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	jjconfig "github.com/jungju/jj/internal/config"
)

const DefaultPlanningAgents = 3
const defaultOpenAIModel = "gpt-5.5"
const defaultDocsDir = "docs"

const (
	DefaultSpecDoc = "SPEC.md"
	DefaultTaskDoc = "TASK.md"
	DefaultEvalDoc = "EVAL.md"
)

type Config struct {
	PlanPath       string
	CWD            string
	RunID          string
	PlanningAgents int
	OpenAIModel    string
	CodexModel     string
	CodexBin       string
	AllowNoGit     bool
	DryRun         bool
	SpecDoc        string
	TaskDoc        string
	EvalDoc        string

	ConfigSearchDir string
	ConfigFile      string
	OpenAIAPIKey    string
	OpenAIAPIKeyEnv string

	PlanningAgentsExplicit bool
	OpenAIModelExplicit    bool
	CodexModelExplicit     bool

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

	if !cfg.PlanningAgentsExplicit && fileCfg.PlanningAgents != nil {
		cfg.PlanningAgents = *fileCfg.PlanningAgents
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
	if strings.TrimSpace(cfg.CodexBin) == "" {
		if v := strings.TrimSpace(os.Getenv("JJ_CODEX_BIN")); v != "" {
			cfg.CodexBin = v
		} else if v := strings.TrimSpace(fileCfg.CodexBin); v != "" {
			cfg.CodexBin = v
		}
	}
	if strings.TrimSpace(cfg.OpenAIAPIKeyEnv) == "" {
		if v := strings.TrimSpace(fileCfg.OpenAIAPIKeyEnv); v != "" {
			cfg.OpenAIAPIKeyEnv = v
		} else {
			cfg.OpenAIAPIKeyEnv = "OPENAI_API_KEY"
		}
	}
	if strings.TrimSpace(cfg.OpenAIAPIKey) == "" {
		cfg.OpenAIAPIKey = os.Getenv(cfg.OpenAIAPIKeyEnv)
	}
	cfg = applyDocumentDefaults(cfg)
	return cfg, nil
}

func loadProjectConfig(cfg Config) (jjconfig.File, error) {
	if strings.TrimSpace(cfg.ConfigFile) != "" {
		return jjconfig.Load(cfg.ConfigFile)
	}
	if strings.TrimSpace(cfg.ConfigSearchDir) == "" {
		return jjconfig.File{}, nil
	}
	fileCfg, found, err := jjconfig.LoadFrom(cfg.ConfigSearchDir)
	if err != nil {
		return jjconfig.File{}, err
	}
	if !found {
		return jjconfig.File{}, nil
	}
	return fileCfg, nil
}

func validateResolvedConfig(cfg Config) error {
	if cfg.PlanningAgents <= 0 {
		return fmt.Errorf("planning-agents must be greater than zero")
	}
	if strings.TrimSpace(cfg.OpenAIModel) == "" {
		return fmt.Errorf("openai model is required")
	}
	if err := validateDocName("spec-doc", cfg.SpecDoc); err != nil {
		return err
	}
	if err := validateDocName("task-doc", cfg.TaskDoc); err != nil {
		return err
	}
	if err := validateDocName("eval-doc", cfg.EvalDoc); err != nil {
		return err
	}
	return nil
}

func applyDocumentDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.SpecDoc) == "" {
		cfg.SpecDoc = DefaultSpecDoc
	} else {
		cfg.SpecDoc = strings.TrimSpace(cfg.SpecDoc)
	}
	if strings.TrimSpace(cfg.TaskDoc) == "" {
		cfg.TaskDoc = DefaultTaskDoc
	} else {
		cfg.TaskDoc = strings.TrimSpace(cfg.TaskDoc)
	}
	if strings.TrimSpace(cfg.EvalDoc) == "" {
		cfg.EvalDoc = DefaultEvalDoc
	} else {
		cfg.EvalDoc = strings.TrimSpace(cfg.EvalDoc)
	}
	return cfg
}

func validateDocName(flagName, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%s is required", flagName)
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%s must be a file name, not a path: %s", flagName, name)
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || clean != name {
		return fmt.Errorf("%s must be a file name, not a path: %s", flagName, name)
	}
	return nil
}

func docRelPath(name string) string {
	return filepath.ToSlash(filepath.Join(defaultDocsDir, strings.TrimSpace(name)))
}
