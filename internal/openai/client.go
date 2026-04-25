package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/jungju/jj/internal/secrets"
)

type Client struct {
	client sdk.Client
}

func NewClient(apiKey string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("OPENAI_API_KEY is required")
	}
	return &Client{client: sdk.NewClient(option.WithAPIKey(apiKey))}, nil
}

func (c *Client) Draft(ctx context.Context, req DraftRequest) (PlanningDraft, []byte, error) {
	var out PlanningDraft
	raw, err := c.structured(ctx, req.Model, plannerInstructions, draftPrompt(req), "planning_draft", planningDraftSchema())
	if err != nil {
		return out, nil, err
	}
	if err := DecodeStructured(raw, &out); err != nil {
		return out, raw, err
	}
	if strings.TrimSpace(out.Agent) == "" {
		out.Agent = req.Agent.Name
	}
	if strings.TrimSpace(out.SpecDraft) == "" {
		out.SpecDraft = out.SpecMarkdown
	}
	if strings.TrimSpace(out.TaskDraft) == "" {
		out.TaskDraft = out.TaskMarkdown
	}
	if len(out.TestingGuidance) == 0 {
		out.TestingGuidance = out.TestPlan
	}
	return out, mustPrettyJSON(out), nil
}

func (c *Client) Merge(ctx context.Context, req MergeRequest) (MergeResult, []byte, error) {
	var out MergeResult
	raw, err := c.structured(ctx, req.Model, plannerInstructions, mergePrompt(req), "merged_plan", mergeSchema())
	if err != nil {
		return out, nil, err
	}
	if err := DecodeStructured(raw, &out); err != nil {
		return out, raw, err
	}
	return out, mustPrettyJSON(out), nil
}

func (c *Client) Evaluate(ctx context.Context, req EvaluationRequest) (EvaluationResult, []byte, error) {
	var out EvaluationResult
	raw, err := c.structured(ctx, req.Model, plannerInstructions, evaluationPrompt(req), "evaluation", evaluationSchema())
	if err != nil {
		return out, nil, err
	}
	if err := DecodeStructured(raw, &out); err != nil {
		return out, raw, err
	}
	NormalizeEvaluation(&out)
	return out, mustPrettyJSON(out), nil
}

func (c *Client) structured(ctx context.Context, model, instructions, prompt, schemaName string, schema map[string]any) ([]byte, error) {
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("openai model is required")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	params := responses.ResponseNewParams{
		Model:             shared.ResponsesModel(model),
		Instructions:      sdk.String(instructions),
		Input:             responses.ResponseNewParamsInputUnion{OfString: sdk.String(prompt)},
		Store:             sdk.Bool(false),
		MaxOutputTokens:   sdk.Int(20000),
		ParallelToolCalls: sdk.Bool(false),
		Text: responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:        schemaName,
					Description: sdk.String("Structured output for jj orchestration."),
					Schema:      schema,
					Strict:      sdk.Bool(true),
				},
			},
		},
	}
	var resp *responses.Response
	var err error
	backoff := 250 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = c.client.Responses.New(ctx, params)
		if err == nil || !retryableOpenAIError(err) || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
	if err != nil {
		return nil, fmt.Errorf("openai responses call failed: %s", summarizeOpenAIError(err))
	}
	text := strings.TrimSpace(resp.OutputText())
	if text == "" {
		return nil, errors.New("openai returned an empty structured response")
	}
	return []byte(text), nil
}

func summarizeOpenAIError(err error) string {
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		status := http.StatusText(apiErr.StatusCode)
		if status == "" {
			status = "HTTP error"
		}
		parts := []string{fmt.Sprintf("status %d %s", apiErr.StatusCode, status)}
		if strings.TrimSpace(apiErr.Code) != "" {
			parts = append(parts, "code "+apiErr.Code)
		}
		if strings.TrimSpace(apiErr.Type) != "" {
			parts = append(parts, "type "+apiErr.Type)
		}
		return strings.Join(parts, ", ")
	}
	return redactOpenAIErrorText(err.Error())
}

func redactOpenAIErrorText(s string) string {
	return secrets.Redact(strings.ReplaceAll(s, "\n", " "))
}

func retryableOpenAIError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func DecodeStructured(data []byte, target any) error {
	extracted, err := ExtractJSONObject(data)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(extracted, target); err != nil {
		return fmt.Errorf("decode structured response: %w", err)
	}
	return nil
}

func ExtractJSONObject(data []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, errors.New("structured response is empty")
	}
	if json.Valid([]byte(trimmed)) {
		return []byte(trimmed), nil
	}
	if fenced := extractFencedJSON(trimmed); fenced != "" && json.Valid([]byte(fenced)) {
		return []byte(fenced), nil
	}
	obj, err := firstJSONObject(trimmed)
	if err != nil {
		return nil, fmt.Errorf("extract structured JSON: %w", err)
	}
	if !json.Valid([]byte(obj)) {
		return nil, errors.New("extract structured JSON: extracted object is invalid JSON")
	}
	return []byte(obj), nil
}

func NormalizeEvaluation(out *EvaluationResult) {
	if out.Result == "" && out.Verdict != "" {
		switch strings.ToLower(out.Verdict) {
		case "pass":
			out.Result = "PASS"
		case "pass_with_risks", "partial":
			out.Result = "PARTIAL"
		case "fail":
			out.Result = "FAIL"
		}
	}
	if out.Result == "" {
		out.Result = "PARTIAL"
	}
	switch out.Result {
	case "PASS", "PARTIAL", "FAIL":
	default:
		out.Result = "PARTIAL"
	}
	if out.Score < 0 {
		out.Score = 0
	}
	if out.Score > 100 {
		out.Score = 100
	}
	if out.Score == 0 {
		switch out.Result {
		case "PASS":
			out.Score = 85
		case "PARTIAL":
			out.Score = 50
		case "FAIL":
			out.Score = 20
		}
	}
	if len(out.RequirementsCoverage) == 0 {
		out.RequirementsCoverage = out.Reasons
	}
	if len(out.TestCoverage) == 0 {
		out.TestCoverage = out.TestResults
	}
}

func extractFencedJSON(s string) string {
	start := strings.Index(s, "```")
	for start >= 0 {
		bodyStart := start + 3
		if newline := strings.IndexByte(s[bodyStart:], '\n'); newline >= 0 {
			bodyStart += newline + 1
		}
		end := strings.Index(s[bodyStart:], "```")
		if end < 0 {
			return ""
		}
		body := strings.TrimSpace(s[bodyStart : bodyStart+end])
		if strings.HasPrefix(strings.ToLower(body), "json\n") {
			body = strings.TrimSpace(body[5:])
		}
		if strings.HasPrefix(body, "{") {
			return body
		}
		next := bodyStart + end + 3
		start = strings.Index(s[next:], "```")
		if start >= 0 {
			start += next
		}
	}
	return ""
}

func firstJSONObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", errors.New("no JSON object found")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[start : i+1]), nil
			}
		}
	}
	return "", errors.New("unterminated JSON object")
}

func mustPrettyJSON(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return []byte("{}\n")
	}
	return append(data, '\n')
}
