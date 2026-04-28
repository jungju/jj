package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jungju/jj/internal/artifact"
	ai "github.com/jungju/jj/internal/openai"
	"github.com/jungju/jj/internal/secrets"
	"github.com/jungju/jj/internal/security"
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

func (p Planner) ReconcileSpec(ctx context.Context, req ai.ReconcileSpecRequest) (ai.ReconcileSpecResult, []byte, error) {
	var out ai.ReconcileSpecResult
	raw, err := p.runJSON(ctx, "spec-reconcile", reconcileSpecPrompt(req), &out)
	if err != nil {
		return out, raw, fmt.Errorf("codex spec reconcile: %w", err)
	}
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
	_, _ = p.Store.WriteJSON(fmt.Sprintf("planning/%s.command.json", stage), map[string]any{
		"provider": "codex",
		"name":     commandName(firstNonEmpty(p.Bin, "codex")),
		"model":    secrets.Redact(p.Model),
		"cwd":      "[workspace]",
		"run_id":   secrets.Redact(p.Store.RunID),
		"stage":    secrets.Redact(stage),
		"argv": security.SanitizeCommandArgv(
			append([]string{firstNonEmpty(p.Bin, "codex")}, BuildArgs(Request{Bin: p.Bin, CWD: p.CWD, Model: p.Model, EventsPath: eventsPath, OutputLastMessage: lastMessagePath, AllowNoGit: p.AllowNoGit})...),
			security.CommandPathRoot{Path: p.Store.RunDir, Label: "[run]"},
			security.CommandPathRoot{Path: p.CWD, Label: "[workspace]"},
		),
		"status":      commandStatus(runErr),
		"exit_code":   result.ExitCode,
		"duration_ms": result.DurationMS,
	})
	if p.Record != nil {
		p.Record("planning_"+stage+"_events", eventsPath)
		p.Record("planning_"+stage+"_last_message", lastMessagePath)
	}
	_ = redactArtifact(eventsPath)
	_ = redactArtifact(lastMessagePath)
	raw := []byte(secrets.Redact(result.Summary))
	if strings.TrimSpace(string(raw)) == "" {
		if data, err := os.ReadFile(lastMessagePath); err == nil {
			raw = data
		}
	}
	if runErr != nil {
		return raw, runErr
	}
	if err := ai.DecodeStructured(raw, target); err != nil {
		return raw, err
	}
	return raw, nil
}

func redactArtifact(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	redacted := security.RedactContent(path, data)
	if string(redacted) == string(data) {
		return nil
	}
	return artifact.AtomicWriteFile(path, redacted, artifact.PrivateFileMode)
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
  "spec_markdown": "compact spec JSON draft or concise spec text",
  "task_markdown": "compact task JSON draft or concise task text",
  "risks": ["..."],
  "assumptions": ["..."],
  "acceptance_criteria": ["..."],
  "test_plan": ["..."]
}

Agent focus: %s

%s

Planning context:
%s

When a current .jj/spec.json state is present, treat it as the source of truth. Treat plan.md as product vision/background only. Do not propose tasks already completed unless fixing a regression.

Produce an implementation-ready planning draft.`, req.Agent.Name, req.Agent.Focus, taskProposalPromptBlock(req.TaskProposalMode, req.ResolvedTaskProposalMode, req.TaskProposalInstruction), req.Plan)
}

func mergePrompt(req ai.MergeRequest) string {
	var b strings.Builder
	b.WriteString(`You are jj's Codex fallback merge planner.

Return only one JSON object. Do not use Markdown fences.

Required JSON shape:
{
  "spec": "{\"version\":1,\"title\":\"...\",\"summary\":\"...\",\"goals\":[],\"non_goals\":[],\"requirements\":[],\"acceptance_criteria\":[],\"open_questions\":[]}",
  "task": "{\"version\":1,\"active_task_id\":null,\"tasks\":[{\"id\":\"T-FEATURE-001\",\"title\":\"...\",\"mode\":\"feature\",\"priority\":\"high\",\"status\":\"queued\",\"reason\":\"...\",\"acceptance_criteria\":[],\"validation_command\":\"./scripts/validate.sh\"}]}",
  "notes": ["..."]
}

Merge the planning context and drafts into final compact JSON state for .jj/spec.json and a proposed task batch for .jj/tasks.json.

Task proposal context:
`)
	b.WriteString(taskProposalPromptBlock(req.TaskProposalMode, req.ResolvedTaskProposalMode, req.TaskProposalInstruction))
	b.WriteString(`

The spec JSON must include version, title, summary, goals, non_goals, requirements, acceptance_criteria, open_questions, created_at, and updated_at.

When a current .jj/spec.json state is present in the planning context, it is the source of truth. plan.md is product vision/background only. Do not propose tasks already completed unless fixing a regression.

The task JSON must include version, active_task_id, and tasks. Treat this as append-only proposal input, not a full replacement for .jj/tasks.json or existing history. Do not include existing tasks from context. jj will assign fresh task IDs, append every proposed task, and select the first proposed task for the current full run. Each task must include id, title, mode, priority, status, reason, acceptance_criteria, and validation_command. Supported statuses are queued, active, in_progress, done, blocked, failed, skipped, and superseded. Do not reset done, failed, skipped, or superseded tasks from continuation context back to queued/in_progress, and do not reuse their task IDs. Propose the next subplan/task after completed work. Auto or balanced task proposal mode must resolve to a concrete task category such as feature, security, hardening, quality, bugfix, or docs.

Planning context:
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

func reconcileSpecPrompt(req ai.ReconcileSpecRequest) string {
	return fmt.Sprintf(`You are jj's Codex fallback SPEC reconciler.

Return only one JSON object. Do not use Markdown fences.

Required JSON shape:
{
  "spec": "{\"version\":1,\"title\":\"...\",\"summary\":\"...\",\"goals\":[],\"non_goals\":[],\"requirements\":[],\"acceptance_criteria\":[],\"open_questions\":[],\"created_at\":\"\",\"updated_at\":\"\"}",
  "notes": ["..."]
}

Reconcile jj's current SPEC state with one validated implementation turn.

Rules:
- Preserve the existing .jj/spec.json schema; do not add top-level fields.
- The previous SPEC is the source of truth.
- Incorporate only behavior supported by the selected task, Codex summary, git diff summary, and passed validation evidence.
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

func commandStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "success"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func commandName(command string) string {
	name := filepath.Base(strings.TrimSpace(command))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "command"
	}
	return name
}
