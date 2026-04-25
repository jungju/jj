package run

import (
	"fmt"
	"strings"

	ai "github.com/jungju/jj/internal/openai"
)

func renderEvaluation(eval ai.EvaluationResult) string {
	ai.NormalizeEvaluation(&eval)
	var b strings.Builder
	b.WriteString("# EVAL\n\n")
	b.WriteString("## Summary\n\n")
	b.WriteString(strings.TrimSpace(eval.Summary))
	b.WriteString("\n\n")
	b.WriteString("## Result\n\n")
	b.WriteString(fmt.Sprintf("%s\n\n", eval.Result))
	b.WriteString("## Score\n\n")
	b.WriteString(fmt.Sprintf("%d\n\n", eval.Score))
	writeList(&b, "What Changed", eval.WhatChanged)
	writeList(&b, "Requirements Coverage", eval.RequirementsCoverage)
	writeList(&b, "Test Coverage", eval.TestCoverage)
	writeList(&b, "Risks", eval.Risks)
	writeList(&b, "Regressions", eval.Regressions)
	writeList(&b, "Recommended Follow-ups", eval.RecommendedFollowups)
	return b.String()
}

func ensureSpecSections(spec, plan string) string {
	return ensureMarkdownSections(spec, "# SPEC", []string{
		"## Overview",
		"## Goals",
		"## Non-Goals",
		"## User Stories",
		"## Functional Requirements",
		"## CLI Behavior",
		"## Pipeline Behavior",
		"## Artifact Layout",
		"## Configuration",
		"## Error Handling",
		"## Security and Privacy",
		"## Observability",
		"## Acceptance Criteria",
	}, "Original plan reference:\n\n"+strings.TrimSpace(plan))
}

func ensureTaskSections(task, plan string) string {
	return ensureMarkdownSections(task, "# TASK", []string{
		"## Objective",
		"## Constraints",
		"## Implementation Steps",
		"## Files and Packages to Inspect",
		"## Required Changes",
		"## Testing Requirements",
		"## Manual Verification",
		"## Done Criteria",
	}, "Implement and verify the requested plan:\n\n"+strings.TrimSpace(plan))
}

func ensureMarkdownSections(markdown, title string, sections []string, fallback string) string {
	content := strings.TrimSpace(markdown)
	if content == "" {
		content = title + "\n\n" + fallback
	}
	if !strings.HasPrefix(content, title) {
		content = title + "\n\n" + content
	}
	for _, section := range sections {
		if !strings.Contains(content, section) {
			content += "\n\n" + section + "\n\nTBD based on the original plan and planner outputs."
		}
	}
	return content + "\n"
}

func writeList(b *strings.Builder, title string, items []string) {
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	if len(items) == 0 {
		b.WriteString("- (none)\n\n")
		return
	}
	for _, item := range items {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(item))
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func codexPrompt(specPath, taskPath string) string {
	return fmt.Sprintf(`You are running inside jj, a Go CLI that orchestrates planning, Codex implementation, tests, and evaluation.

Implement the work described by:
- %s
- %s

Requirements:
- Read both files before editing.
- Make the necessary code changes in this working tree.
- Choose and run the relevant tests yourself.
- Keep changes focused on the SPEC/TASK.
- In your final response, include changed files, tests run with results, and any remaining risks.
`, specPath, taskPath)
}
