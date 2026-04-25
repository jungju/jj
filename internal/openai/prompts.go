package openai

import (
	"encoding/json"
	"fmt"
	"strings"
)

const plannerInstructions = `You are an expert software planning agent inside jj, a Go CLI that prepares specs, tasks, Codex implementation prompts, tests, and evaluation reports. Return only JSON matching the schema. Do not include secrets or environment variable values.`

func draftPrompt(req DraftRequest) string {
	return fmt.Sprintf(`Create a planning draft for the following product/development request.

Agent name: %s
Agent focus: %s

Original plan:
%s

Return a concrete summary, spec_markdown, task_markdown, risks, assumptions, acceptance_criteria, and test_plan. Keep the draft implementation-ready.`, req.Agent.Name, req.Agent.Focus, req.Plan)
}

func mergePrompt(req MergeRequest) string {
	var b strings.Builder
	b.WriteString("Merge the parallel planning drafts into final implementation documents.\n\n")
	b.WriteString("Original plan:\n")
	b.WriteString(req.Plan)
	b.WriteString("\n\nDrafts:\n")
	for _, draft := range req.Drafts {
		data, _ := json.MarshalIndent(draft, "", "  ")
		b.Write(data)
		b.WriteString("\n\n")
	}
	b.WriteString(`Return final Markdown strings for SPEC.md and TASK.md.

SPEC.md must include these sections:
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

TASK.md must include these sections:
# TASK
## Objective
## Constraints
## Implementation Steps
## Files and Packages to Inspect
## Required Changes
## Testing Requirements
## Manual Verification
## Done Criteria

Merge important acceptance criteria, remove duplicates, and put unresolved ambiguity under assumptions or risks.`)
	return b.String()
}

func evaluationPrompt(req EvaluationRequest) string {
	return fmt.Sprintf(`Evaluate whether the implementation satisfies the original plan.

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

Answer these questions strictly from the evidence: were requirements reflected, did Codex make relevant changes, does the diff match, were tests run, did tests pass, is scope controlled, are there security risks, and what should happen next.

Return result as PASS, PARTIAL, or FAIL. Return score as an integer 0-100. If uncertain, use PARTIAL rather than PASS.`, req.Plan, req.Spec, req.Task, req.CodexSummary, truncate(req.CodexEvents, 60000), req.GitDiff)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]..."
}
