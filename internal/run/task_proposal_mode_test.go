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

func TestResolveTaskProposalModePositiveBugfixEvidence(t *testing.T) {
	tests := []string{
		"validation failed after implementation",
		"tests failed in validation summary",
		`{"validation":{"status":"failed","failed_count":0}}`,
		`{"validation":{"status":"passed","failed_count":2}}`,
		"provider failure while planning",
		"panic: runtime error",
		"fatal error: provider exited",
		"regression detected in dashboard run inspection",
		"current blocker prevents progress",
		`{"tasks":[{"id":"TASK-0002","status":"blocked"}]}`,
	}

	for _, evidence := range tests {
		t.Run(evidence, func(t *testing.T) {
			for _, mode := range []TaskProposalMode{TaskProposalModeAuto, TaskProposalModeBalanced} {
				got := ResolveTaskProposalMode(mode, evidence)
				if got.Resolved != TaskProposalModeBugfix {
					t.Fatalf("%s should resolve bugfix for %q, got %#v", mode, evidence, got)
				}
			}
		})
	}
}

func TestResolveTaskProposalModeHealthyAndNegatedEvidenceDoesNotResolveBugfix(t *testing.T) {
	tests := []string{
		"no blocker",
		"not blocked",
		"no validation failed; validation passed",
		`{"validation":{"status":"passed","failed_count":0}}`,
		"failed_count: 0",
		"status passed",
		"tests pass",
		"no regressions found",
		"all tasks done",
		"no runnable tasks",
		"regression detection needs work",
		"Current SPEC requirements and open questions:\n- Auto mode resolves to bugfix for failed validation, failed tests, provider failure, panic, fatal error, and regression.\n\nNon-terminal task state:\n\nClosed task history count: 12\n",
	}

	for _, evidence := range tests {
		t.Run(evidence, func(t *testing.T) {
			for _, mode := range []TaskProposalMode{TaskProposalModeAuto, TaskProposalModeBalanced} {
				got := ResolveTaskProposalMode(mode, evidence)
				if got.Resolved == TaskProposalModeBugfix {
					t.Fatalf("%s should not resolve bugfix for healthy or negated evidence %q, got %#v", mode, evidence, got)
				}
			}
		})
	}
}

func TestResolveTaskProposalModeAutoSecurityEvidence(t *testing.T) {
	got := ResolveTaskProposalMode(TaskProposalModeAuto, "scripts/validate.sh failed: raw API key leaked in dashboard audit export")
	if got.Selected != TaskProposalModeAuto || got.Resolved != TaskProposalModeSecurity {
		t.Fatalf("expected auto to resolve security, got %#v", got)
	}
	if got.SelectedTaskID != "TASK-0001" {
		t.Fatalf("unexpected selected task id: %#v", got)
	}
}

func TestResolveTaskProposalModeSecurityRequiresConcreteEvidence(t *testing.T) {
	tests := []string{
		"unsafe secret exposure in artifacts",
		"security risk exists because artifacts, commands, manifests, and dashboard pages are sensitive",
		"completed security guardrails remain closed unless scripts/validate.sh, focused tests, CI, or disclosure evidence fails",
		"all release-gate evidence remains green and no concrete regression exists",
		"Current SPEC requirements and open questions:\n- No persisted artifact or served dashboard response contains raw API keys.\n- Completed security guardrails remain closed unless scripts/validate.sh fails.\n\nNon-terminal task state:\n\nClosed task history count: 40\n",
	}

	for _, evidence := range tests {
		t.Run(evidence, func(t *testing.T) {
			for _, mode := range []TaskProposalMode{TaskProposalModeAuto, TaskProposalModeBalanced} {
				got := ResolveTaskProposalMode(mode, evidence)
				if got.Resolved == TaskProposalModeSecurity {
					t.Fatalf("%s should not resolve security from policy, healthy, or background risk wording %q, got %#v", mode, evidence, got)
				}
			}
		})
	}
}

func TestResolveTaskProposalModeConcreteSecurityEvidencePrecedesBugfix(t *testing.T) {
	tests := []string{
		"scripts/validate.sh failed: raw API key leaked in dashboard audit export",
		"CI failed with security regression: symlink escape read outside workspace",
		"confirmed disclosure: validation output rendered raw bearer token in run detail",
		"Recent security evidence:\n- secret_disclosure\n- dashboard_exposure\n",
	}

	for _, evidence := range tests {
		t.Run(evidence, func(t *testing.T) {
			for _, mode := range []TaskProposalMode{TaskProposalModeAuto, TaskProposalModeBalanced} {
				got := ResolveTaskProposalMode(mode, evidence)
				if got.Resolved != TaskProposalModeSecurity {
					t.Fatalf("%s should resolve security for concrete regression evidence %q, got %#v", mode, evidence, got)
				}
			}
		})
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

func TestResolveTaskProposalModeConcreteModesOverrideOnlyForPositiveBugfixEvidence(t *testing.T) {
	concreteModes := []TaskProposalMode{
		TaskProposalModeFeature,
		TaskProposalModeSecurity,
		TaskProposalModeHardening,
		TaskProposalModeQuality,
		TaskProposalModeDocs,
	}

	for _, mode := range concreteModes {
		t.Run(string(mode)+" positive", func(t *testing.T) {
			got := ResolveTaskProposalMode(mode, `{"validation":{"status":"failed","failed_count":1}}`)
			if got.Resolved != TaskProposalModeBugfix {
				t.Fatalf("expected %s to be overridden to bugfix by positive evidence, got %#v", mode, got)
			}
		})
		t.Run(string(mode)+" healthy", func(t *testing.T) {
			got := ResolveTaskProposalMode(mode, "validation passed; tests pass; no blocker; failed_count: 0")
			if got.Resolved != mode {
				t.Fatalf("expected %s to remain selected for healthy evidence, got %#v", mode, got)
			}
		})
	}
}

func TestResolveTaskProposalModeReasonDoesNotEchoBugfixEvidencePayload(t *testing.T) {
	secret := "sk-proj-taskproposalpayload1234567890"
	got := ResolveTaskProposalMode(TaskProposalModeAuto, "validation failed Authorization: Bearer "+secret+" /tmp/unsafe-command")
	if got.Resolved != TaskProposalModeBugfix {
		t.Fatalf("expected bugfix for validation failure, got %#v", got)
	}
	if strings.Contains(got.Reason, secret) || strings.Contains(got.Reason, "/tmp/unsafe-command") {
		t.Fatalf("resolution reason echoed raw evidence payload: %#v", got)
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
