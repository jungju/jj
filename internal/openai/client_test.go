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
		SelectedTask:      `{"id":"TASK-0001"}`,
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

func TestPromptsSanitizeHandoffPayloads(t *testing.T) {
	secret := "openai-prompt-secret-token-1234567890"
	t.Setenv("JJ_OPENAI_PROMPT_TOKEN", secret)
	hostile := strings.Join([]string{
		"Keep safe planning context.",
		"command=./scripts/deploy --token " + secret,
		"PATH=/tmp/openai-prompt-secret",
		"manifest={\"run_id\":\"attack\",\"token\":\"" + secret + "\"}",
		"validation_output=panic at /tmp/openai-validation",
		"diff --git a/config.txt b/config.txt",
		"+api_key=" + secret,
		"denied_path=../../" + secret,
	}, "\n")

	prompts := []string{
		draftPrompt(DraftRequest{Plan: hostile, Agent: Agent{Name: "product_spec", Focus: "focus"}}),
		mergePrompt(MergeRequest{
			Plan: hostile,
			Drafts: []PlanningDraft{{
				Agent:              "product_spec",
				Summary:            "command=./scripts/deploy --token " + secret,
				SpecMarkdown:       hostile,
				TaskMarkdown:       hostile,
				AcceptanceCriteria: []string{"validation_output=panic " + secret},
				TestPlan:           []string{"PATH=/tmp/raw-env"},
			}},
		}),
		reconcileSpecPrompt(ReconcileSpecRequest{
			PreviousSpec:      `{"version":1,"summary":"command=./scripts/deploy --token ` + secret + `"}`,
			PlannedSpec:       `{"version":1,"summary":"PATH=/tmp/openai-planned"}`,
			SelectedTask:      `{"id":"TASK-0001","reason":"validation_output=panic ` + secret + `"}`,
			CodexSummary:      hostile,
			GitDiffSummary:    hostile,
			ValidationSummary: hostile,
		}),
	}
	for _, prompt := range prompts {
		for _, leaked := range []string{secret, "./scripts/deploy --token", "/tmp/openai", "/tmp/raw-env", "diff --git", "+api_key=", "../../"} {
			if strings.Contains(prompt, leaked) {
				t.Fatalf("prompt leaked %q:\n%s", leaked, prompt)
			}
		}
		if !strings.Contains(prompt, "Keep safe planning context.") || !strings.Contains(prompt, "[jj-omitted]") {
			t.Fatalf("prompt lost safe context or redaction evidence:\n%s", prompt)
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
