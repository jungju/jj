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

func TestNormalizeEvaluation(t *testing.T) {
	eval := EvaluationResult{Verdict: "pass_with_risks"}
	NormalizeEvaluation(&eval)
	if eval.Result != "PARTIAL" || eval.Score != 50 {
		t.Fatalf("unexpected normalized eval: %#v", eval)
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
