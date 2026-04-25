package run

import (
	"testing"

	"github.com/jungju/jj/internal/artifact"
)

func TestSelectPlannerProviderOpenAIWhenKeyExists(t *testing.T) {
	store := newTestStore(t)

	selection, err := selectPlanner(Config{CWD: store.CWD, OpenAIAPIKey: "test-key"}, store, nil)
	if err != nil {
		t.Fatalf("select planner: %v", err)
	}
	if selection.Provider != plannerProviderOpenAI {
		t.Fatalf("expected openai provider, got %q", selection.Provider)
	}
}

func TestSelectPlannerProviderCodexWhenKeyMissing(t *testing.T) {
	store := newTestStore(t)

	selection, err := selectPlanner(Config{CWD: store.CWD}, store, nil)
	if err != nil {
		t.Fatalf("select planner: %v", err)
	}
	if selection.Provider != plannerProviderCodex {
		t.Fatalf("expected codex provider, got %q", selection.Provider)
	}
}

func TestSelectPlannerProviderInjectedWins(t *testing.T) {
	store := newTestStore(t)

	selection, err := selectPlanner(Config{
		CWD:     store.CWD,
		Planner: &fakePlanner{},
	}, store, nil)
	if err != nil {
		t.Fatalf("select planner: %v", err)
	}
	if selection.Provider != plannerProviderInjected {
		t.Fatalf("expected injected provider, got %q", selection.Provider)
	}
}

func newTestStore(t *testing.T) artifact.Store {
	t.Helper()
	store, err := artifact.NewStore(t.TempDir(), "test-run")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return store
}
