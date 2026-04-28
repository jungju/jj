package openai

import (
	"net/http"
	"strings"
	"testing"

	sdk "github.com/openai/openai-go/v3"
)

func TestDecodeStructured(t *testing.T) {
	var draft PlanningDraft
	err := DecodeStructured([]byte(`{
		"agent": "product_spec",
		"summary": "summary",
		"spec_markdown": "spec",
		"task_markdown": "task",
		"risks": [],
		"assumptions": [],
		"acceptance_criteria": ["works"],
		"test_plan": ["go test ./..."]
	}`), &draft)
	if err != nil {
		t.Fatalf("decode structured response: %v", err)
	}
	if draft.Agent != "product_spec" || draft.SpecMarkdown != "spec" {
		t.Fatalf("unexpected draft: %#v", draft)
	}
}

func TestDecodeStructuredExtractsFencedJSON(t *testing.T) {
	var draft PlanningDraft
	err := DecodeStructured([]byte("```json\n{\"agent\":\"product_spec\"}\n```"), &draft)
	if err != nil {
		t.Fatalf("decode fenced JSON: %v", err)
	}
	if draft.Agent != "product_spec" {
		t.Fatalf("unexpected draft: %#v", draft)
	}
}

func TestMergePromptRequestsCanonicalTaskQueue(t *testing.T) {
	prompt := mergePrompt(MergeRequest{
		Plan: "Make generated docs canonical.",
		Drafts: []PlanningDraft{{
			Agent:        "product_spec",
			Summary:      "summary",
			SpecMarkdown: "# SPEC",
			TaskMarkdown: "# TASK",
		}},
	})
	for _, want := range []string{
		".jj/spec.json",
		".jj/tasks.json",
		`"version": 1`,
		`"active_task_id": null`,
		`"mode": "feature"`,
		`"status": "queued"`,
		`"validation_command": "./scripts/validate.sh"`,
		"append-only proposal input, not a full replacement for .jj/tasks.json",
		"Do not include existing tasks from context.",
		"jj will assign fresh task IDs, append every proposed task to existing history",
		"current .jj/spec.json state is present in the planning context, it is the source of truth",
		"plan.md is product vision/background only",
		"Use task statuses queued, active, in_progress, done, blocked, failed, skipped, or superseded.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("merge prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "TASK.md must include these sections") || strings.Contains(prompt, "# jj TASK Queue") || strings.Contains(prompt, "## Implementation Steps") {
		t.Fatalf("merge prompt should not request legacy task sections:\n%s", prompt)
	}
}

func TestReconcileSpecPromptRequestsResultBasedSchema(t *testing.T) {
	prompt := reconcileSpecPrompt(ReconcileSpecRequest{
		PreviousSpec:      `{"version":1,"title":"Before"}`,
		PlannedSpec:       `{"version":1,"title":"Planned"}`,
		SelectedTask:      `{"id":"T-FEATURE-001"}`,
		CodexSummary:      "Changed code.",
		GitDiffSummary:    "diff summary",
		ValidationSummary: "Validation status: passed",
	})

	for _, want := range []string{
		`"spec" field as a JSON string shaped exactly like .jj/spec.json`,
		"Preserve the existing schema; do not add top-level fields.",
		"The previous SPEC is the source of truth.",
		"Incorporate only behavior supported by the selected task, Codex summary, git diff summary, and passed validation evidence.",
		`"created_at": ""`,
		`"updated_at": ""`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reconcile prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestDecodeStructuredInvalidJSON(t *testing.T) {
	var draft PlanningDraft
	if err := DecodeStructured([]byte(`{"agent":`), &draft); err == nil {
		t.Fatal("expected invalid JSON to fail")
	}
}

func TestSummarizeOpenAIErrorDoesNotExposeAPIMessage(t *testing.T) {
	msg := summarizeOpenAIError(&sdk.Error{
		StatusCode: http.StatusUnauthorized,
		Code:       "invalid_api_key",
		Type:       "invalid_request_error",
		Message:    "Incorrect API key provided: sk-proj-secretsecret",
	})
	if !strings.Contains(msg, "status 401 Unauthorized") || !strings.Contains(msg, "invalid_api_key") {
		t.Fatalf("unexpected summary: %q", msg)
	}
	if strings.Contains(msg, "sk-proj") || strings.Contains(msg, "Incorrect API key") {
		t.Fatalf("summary should not expose raw API message: %q", msg)
	}
}
