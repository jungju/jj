package openai

import (
	"encoding/json"
	"fmt"
	"strings"
)

const plannerInstructions = `You are an expert software planning agent inside jj, a Go CLI that prepares compact JSON spec state, task state, implementation prompts, and deterministic validation guidance. Return only JSON matching the schema. Do not include secrets or environment variable values.`

func draftPrompt(req DraftRequest) string {
	return fmt.Sprintf(`Create a planning draft for the following jj planning context.

Agent name: %s
Agent focus: %s

%s

Planning context:
%s

When a current .jj/spec.json state is present, treat it as the source of truth. Treat plan.md as product vision/background only. Do not propose tasks already completed unless fixing a regression.

Return a concrete summary, spec_markdown, task_markdown, risks, assumptions, acceptance_criteria, and test_plan. The spec/task draft fields may contain compact JSON-oriented content rather than Markdown; keep the draft implementation-ready.`, req.Agent.Name, req.Agent.Focus, taskProposalPromptBlock(req.TaskProposalMode, req.ResolvedTaskProposalMode, req.TaskProposalInstruction), req.Plan)
}

func mergePrompt(req MergeRequest) string {
	var b strings.Builder
	b.WriteString("Merge the parallel planning drafts into final compact JSON state.\n\n")
	b.WriteString(taskProposalPromptBlock(req.TaskProposalMode, req.ResolvedTaskProposalMode, req.TaskProposalInstruction))
	b.WriteString("\n\n")
	b.WriteString("Planning context:\n")
	b.WriteString(req.Plan)
	b.WriteString("\n\nDrafts:\n")
	for _, draft := range req.Drafts {
		data, _ := json.MarshalIndent(draft, "", "  ")
		b.Write(data)
		b.WriteString("\n\n")
	}
	b.WriteString(`Return the "spec" field as a JSON string shaped like .jj/spec.json:
{
  "version": 1,
  "title": "...",
  "summary": "...",
  "goals": [],
  "non_goals": [],
  "requirements": [],
  "acceptance_criteria": [],
  "open_questions": [],
  "created_at": "",
  "updated_at": ""
}

Return the "task" field as a JSON string containing only the next proposed task batch shaped like .jj/tasks.json:
{
  "version": 1,
  "active_task_id": null,
  "tasks": [
    {
      "id": "TASK-0001",
      "title": "Short imperative title",
      "mode": "feature",
      "priority": "high",
      "status": "queued",
      "reason": "Why this task is next.",
      "acceptance_criteria": [],
      "validation_command": "./scripts/validate.sh"
    }
  ]
}

This task JSON is append-only proposal input, not a full replacement for .jj/tasks.json. Do not include existing tasks from context. jj will assign fresh task IDs, append every proposed task to existing history, and select the first proposed task for the current full run.

When a current .jj/spec.json state is present in the planning context, it is the source of truth. plan.md is product vision/background only. Do not propose tasks already completed unless fixing a regression.

Use task statuses queued, active, in_progress, done, blocked, failed, skipped, or superseded. The first proposed task must be small enough for one implementation turn and must include mode metadata. Existing terminal tasks from continuation context are history: do not reset done, failed, skipped, or superseded tasks back to queued/in_progress, and do not reuse their task IDs. Propose the next subplan/task after completed work. Merge important acceptance criteria, remove duplicates, and put unresolved ambiguity under assumptions, risks, or open questions.`)
	return b.String()
}

func reconcileSpecPrompt(req ReconcileSpecRequest) string {
	return fmt.Sprintf(`Reconcile jj's current SPEC state with the result of one validated implementation turn.

Return the "spec" field as a JSON string shaped exactly like .jj/spec.json:
{
  "version": 1,
  "title": "...",
  "summary": "...",
  "goals": [],
  "non_goals": [],
  "requirements": [],
  "acceptance_criteria": [],
  "open_questions": [],
  "created_at": "",
  "updated_at": ""
}

Rules:
- Preserve the existing schema; do not add top-level fields.
- The previous SPEC is the source of truth.
- Incorporate only behavior supported by the selected task, Codex summary, git diff summary, and passed validation evidence.
- Keep durable product requirements and acceptance criteria; remove or resolve obsolete open questions only when the implementation evidence supports it.
- Do not include secrets, raw logs, or environment values.

Previous SPEC:
%s

Planned SPEC:
%s

Selected task:
%s

Codex summary:
%s

Git diff summary:
%s

Validation summary:
%s`, req.PreviousSpec, req.PlannedSpec, req.SelectedTask, req.CodexSummary, req.GitDiffSummary, req.ValidationSummary)
}

func taskProposalPromptBlock(selected, resolved, instruction string) string {
	selected = strings.TrimSpace(selected)
	resolved = strings.TrimSpace(resolved)
	instruction = strings.TrimSpace(instruction)
	if selected == "" && resolved == "" && instruction == "" {
		return "Task Proposal Mode: auto\nResolved Mode: feature\nInstruction: Choose a small next task from current evidence."
	}
	var b strings.Builder
	if selected != "" {
		b.WriteString("Task Proposal Mode: ")
		b.WriteString(selected)
		b.WriteByte('\n')
	}
	if resolved != "" {
		b.WriteString("Resolved Mode: ")
		b.WriteString(resolved)
		b.WriteByte('\n')
	}
	if instruction != "" {
		b.WriteString(instruction)
	}
	return strings.TrimSpace(b.String())
}
