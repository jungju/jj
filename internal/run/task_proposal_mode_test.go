package run

import (
	"strings"
	"testing"
)

func TestParseTaskProposalModeValidModes(t *testing.T) {
	for _, value := range ValidTaskProposalModeValues() {
		got, err := ParseTaskProposalMode(value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		if string(got) != value {
			t.Fatalf("parse %q returned %q", value, got)
		}
	}
}

func TestParseTaskProposalModeInvalidValue(t *testing.T) {
	_, err := ParseTaskProposalMode("fast")
	if err == nil || !strings.Contains(err.Error(), `invalid task proposal mode: "fast"`) || !strings.Contains(err.Error(), ValidTaskProposalModesString()) {
		t.Fatalf("expected clear invalid mode error, got %v", err)
	}
}

func TestParseTaskProposalModeDefaultsToAuto(t *testing.T) {
	got, err := ParseTaskProposalMode(" \n")
	if err != nil {
		t.Fatalf("parse default: %v", err)
	}
	if got != TaskProposalModeAuto {
		t.Fatalf("expected auto default, got %q", got)
	}
}

func TestTaskProposalTaskIDUsesGlobalSequenceFallback(t *testing.T) {
	for _, mode := range ValidTaskProposalModes() {
		if got := TaskProposalTaskID(mode); got != "TASK-0001" {
			t.Fatalf("mode %s should use global task fallback id, got %q", mode, got)
		}
	}
}

func TestResolveTaskProposalModeAutoBugfix(t *testing.T) {
	got := ResolveTaskProposalMode(TaskProposalModeAuto, "validation failed and tests fail")
	if got.Selected != TaskProposalModeAuto || got.Resolved != TaskProposalModeBugfix {
		t.Fatalf("expected auto to resolve bugfix, got %#v", got)
	}
	if !strings.Contains(got.Reason, "bugfix") || got.SelectedTaskID != "TASK-0001" {
		t.Fatalf("unexpected resolution metadata: %#v", got)
	}
}

func TestResolveTaskProposalModeAutoSecurityEvidence(t *testing.T) {
	got := ResolveTaskProposalMode(TaskProposalModeAuto, "unsafe secret exposure in artifacts")
	if got.Selected != TaskProposalModeAuto || got.Resolved != TaskProposalModeSecurity {
		t.Fatalf("expected auto to resolve security, got %#v", got)
	}
	if got.SelectedTaskID != "TASK-0001" {
		t.Fatalf("unexpected selected task id: %#v", got)
	}
}

func TestResolveTaskProposalModeConcreteFeatureWithoutBlocker(t *testing.T) {
	got := ResolveTaskProposalMode(TaskProposalModeFeature, "add dashboard controls")
	if got.Selected != TaskProposalModeFeature || got.Resolved != TaskProposalModeFeature {
		t.Fatalf("expected feature to remain feature, got %#v", got)
	}
}

func TestResolveTaskProposalModeConcreteFeatureIgnoresSecurityEvidence(t *testing.T) {
	got := ResolveTaskProposalMode(TaskProposalModeFeature, "unsafe secret exposure in artifacts")
	if got.Selected != TaskProposalModeFeature || got.Resolved != TaskProposalModeFeature {
		t.Fatalf("expected feature to remain feature despite security keywords, got %#v", got)
	}
	if strings.Contains(got.Reason, "overridden") {
		t.Fatalf("security evidence should not override concrete mode, got %#v", got)
	}
}

func TestResolveTaskProposalModeCriticalBlockerOverridesConcreteModeToBugfix(t *testing.T) {
	got := ResolveTaskProposalMode(TaskProposalModeFeature, "validation failed and tests fail")
	if got.Selected != TaskProposalModeFeature || got.Resolved != TaskProposalModeBugfix {
		t.Fatalf("expected bugfix override, got %#v", got)
	}
	if !strings.Contains(got.Reason, "overridden") || got.SelectedTaskID != "TASK-0001" {
		t.Fatalf("expected override metadata, got %#v", got)
	}
}

func TestTaskProposalPromptContextIncludesInstruction(t *testing.T) {
	resolution := ResolveTaskProposalMode(TaskProposalModeSecurity, "secret risk")
	got := TaskProposalPromptContext(resolution)
	for _, want := range []string{"Task Proposal Mode: security", "Resolved Mode: security", "secret redaction", "TASK-0001"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt context missing %q:\n%s", want, got)
		}
	}
}

func TestTaskProposalPromptContextIncludesNextIntentOverride(t *testing.T) {
	resolution := ResolveTaskProposalMode(TaskProposalModeFeature, "feature work")
	got := TaskProposalPromptContext(resolution, "Improve the web UI only.")
	for _, want := range []string{".jj/next-intent.md is active", "free-form intent", "Ignore task-proposal-mode", "Use mode only after satisfying the intent"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt context missing %q:\n%s", want, got)
		}
	}
}
