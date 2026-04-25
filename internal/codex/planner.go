package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jungju/jj/internal/artifact"
	ai "github.com/jungju/jj/internal/openai"
)

type Executor interface {
	Run(context.Context, Request) (Result, error)
}

type Planner struct {
	CWD        string
	Bin        string
	Model      string
	AllowNoGit bool
	Store      artifact.Store
	Runner     Executor
	Record     func(string, string)
}

func (p Planner) Draft(ctx context.Context, req ai.DraftRequest) (ai.PlanningDraft, []byte, error) {
	var out ai.PlanningDraft
	raw, err := p.runJSON(ctx, req.Agent.Name, draftPrompt(req), &out)
	if err != nil {
		return out, raw, fmt.Errorf("codex draft %s: %w", req.Agent.Name, err)
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

func (p Planner) Merge(ctx context.Context, req ai.MergeRequest) (ai.MergeResult, []byte, error) {
	var out ai.MergeResult
	raw, err := p.runJSON(ctx, "merge", mergePrompt(req), &out)
	if err != nil {
		return out, raw, fmt.Errorf("codex merge: %w", err)
	}
	return out, mustPrettyJSON(out), nil
}

func (p Planner) Evaluate(ctx context.Context, req ai.EvaluationRequest) (ai.EvaluationResult, []byte, error) {
	var out ai.EvaluationResult
	raw, err := p.runJSON(ctx, "eval", evaluationPrompt(req), &out)
	if err != nil {
		return out, raw, fmt.Errorf("codex eval: %w", err)
	}
	ai.NormalizeEvaluation(&out)
	return out, mustPrettyJSON(out), nil
}

func (p Planner) runJSON(ctx context.Context, stage, prompt string, target any) ([]byte, error) {
	eventsPath, err := p.Store.Path(fmt.Sprintf("planning/%s.events.jsonl", stage))
	if err != nil {
		return nil, err
	}
	lastMessagePath, err := p.Store.Path(fmt.Sprintf("planning/%s.last-message.txt", stage))
	if err != nil {
		return nil, err
	}
	runner := p.Runner
	if runner == nil {
		runner = Runner{}
	}
	result, runErr := runner.Run(ctx, Request{
		Bin:               p.Bin,
		CWD:               p.CWD,
		Model:             p.Model,
		Prompt:            prompt,
		EventsPath:        eventsPath,
		OutputLastMessage: lastMessagePath,
		AllowNoGit:        p.AllowNoGit,
	})
	if p.Record != nil {
		p.Record("planning_"+stage+"_events", eventsPath)
		p.Record("planning_"+stage+"_last_message", lastMessagePath)
	}
	raw := []byte(result.Summary)
	if runErr != nil {
		return raw, runErr
	}
	if err := ai.DecodeStructured(raw, target); err != nil {
		return raw, err
	}
	return raw, nil
}

func mustPrettyJSON(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func draftPrompt(req ai.DraftRequest) string {
	return fmt.Sprintf(`You are jj's Codex fallback planning agent.

Return only one JSON object. Do not use Markdown fences.

Required JSON shape:
{
  "agent": "%s",
  "summary": "...",
  "spec_markdown": "...",
  "task_markdown": "...",
  "risks": ["..."],
  "assumptions": ["..."],
  "acceptance_criteria": ["..."],
  "test_plan": ["..."]
}

Agent focus: %s

Original plan:
%s

Produce an implementation-ready planning draft.`, req.Agent.Name, req.Agent.Focus, req.Plan)
}

func mergePrompt(req ai.MergeRequest) string {
	var b strings.Builder
	b.WriteString(`You are jj's Codex fallback merge planner.

Return only one JSON object. Do not use Markdown fences.

Required JSON shape:
{
  "spec": "# SPEC\n...",
  "task": "# TASK\n...",
  "notes": ["..."]
}

Merge the original plan and drafts into final SPEC.md and TASK.md contents.

SPEC.md must include:
# SPEC
## Overview
## Goals
## Non-Goals
## User Stories
## Functional Requirements
## CLI Behavior
## Pipeline Behavior
## Artifact Layout
## Configuration
## Error Handling
## Security and Privacy
## Observability
## Acceptance Criteria

TASK.md must include:
# TASK
## Objective
## Constraints
## Implementation Steps
## Files and Packages to Inspect
## Required Changes
## Testing Requirements
## Manual Verification
## Done Criteria

Original plan:
`)
	b.WriteString(req.Plan)
	b.WriteString("\n\nDrafts:\n")
	for _, draft := range req.Drafts {
		data, _ := json.MarshalIndent(draft, "", "  ")
		b.Write(data)
		b.WriteString("\n\n")
	}
	return b.String()
}

func evaluationPrompt(req ai.EvaluationRequest) string {
	return fmt.Sprintf(`You are jj's Codex fallback evaluator.

Return only one JSON object. Do not use Markdown fences.

Required JSON shape:
{
  "result": "PASS|PARTIAL|FAIL",
  "score": 0,
  "summary": "...",
  "what_changed": ["..."],
  "requirements_coverage": ["..."],
  "test_coverage": ["..."],
  "risks": ["..."],
  "regressions": ["..."],
  "recommended_followups": ["..."]
}

Be strict. If evidence is incomplete, use PARTIAL rather than PASS.

Original plan:
%s

SPEC.md:
%s

TASK.md:
%s

Codex summary:
%s

Codex JSONL events:
%s

Git diff/status summary:
%s
`, req.Plan, req.Spec, req.Task, req.CodexSummary, truncate(req.CodexEvents, 60000), req.GitDiff)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]..."
}
