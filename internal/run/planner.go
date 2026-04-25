package run

import (
	"strings"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/codex"
	ai "github.com/jungju/jj/internal/openai"
)

const (
	plannerProviderInjected = "injected"
	plannerProviderOpenAI   = "openai"
	plannerProviderCodex    = "codex"
)

type plannerSelection struct {
	Client   PlanningClient
	Provider string
}

func selectPlanner(cfg Config, store artifact.Store, record func(string, string)) (plannerSelection, error) {
	if cfg.Planner != nil {
		return plannerSelection{Client: cfg.Planner, Provider: plannerProviderInjected}, nil
	}

	if apiKey := strings.TrimSpace(cfg.OpenAIAPIKey); apiKey != "" {
		client, err := ai.NewClient(apiKey)
		if err != nil {
			return plannerSelection{}, err
		}
		return plannerSelection{Client: client, Provider: plannerProviderOpenAI}, nil
	}

	return plannerSelection{
		Client: codex.Planner{
			CWD:        cfg.CWD,
			Bin:        cfg.CodexBin,
			Model:      cfg.CodexModel,
			AllowNoGit: cfg.AllowNoGit,
			Store:      store,
			Runner:     cfg.PlannerCodexRunner,
			Record:     record,
		},
		Provider: plannerProviderCodex,
	}, nil
}
