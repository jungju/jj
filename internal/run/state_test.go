package run

import (
	"strings"
	"testing"
	"time"

	ai "github.com/jungju/jj/internal/openai"
)

func TestUpdateTaskAfterValidationPassedMarksDone(t *testing.T) {
	state := TaskState{
		Version: 1,
		Tasks: []TaskRecord{{
			ID:     "T-SEC-001",
			Status: "in_progress",
		}},
	}
	active := "T-SEC-001"
	state.ActiveTaskID = &active

	updated := updateTaskAfterValidation(state, "T-SEC-001", "run-1", ManifestValidation{Status: validationStatusPassed}, "", time.Unix(0, 0).UTC())

	if updated.ActiveTaskID != nil {
		t.Fatalf("active task should be cleared: %#v", updated.ActiveTaskID)
	}
	if got := updated.Tasks[0].Status; got != "done" {
		t.Fatalf("passed validation should mark task done, got %q", got)
	}
	if updated.Tasks[0].Verdict == nil || *updated.Tasks[0].Verdict != validationStatusPassed {
		t.Fatalf("verdict should be preserved: %#v", updated.Tasks[0].Verdict)
	}
}

func TestBuildTaskStatePreservesDoneTaskAndRenumbersPlannerCollision(t *testing.T) {
	before := TaskState{
		Version: 1,
		Tasks: []TaskRecord{{
			ID:        "T-SEC-001",
			Title:     "Add security boundary",
			Mode:      "security",
			Status:    "done",
			CreatedAt: "2026-04-27T00:00:00Z",
			UpdatedAt: "2026-04-27T00:00:00Z",
			Verdict:   stringPtr(validationStatusPassed),
		}},
	}
	planned := `{
		"version": 1,
		"active_task_id": null,
		"tasks": [{
			"id": "T-SEC-001",
			"title": "Centralize secret redaction",
			"mode": "security",
			"priority": "high",
			"status": "queued",
			"reason": "next",
			"acceptance_criteria": ["works"],
			"validation_command": "./scripts/validate.sh"
		}]
	}`
	state, selected := buildTaskState(before, "plan", ai.MergeResult{Task: planned}, nil, TaskProposalResolution{
		Selected: TaskProposalModeAuto,
		Resolved: TaskProposalModeSecurity,
		Reason:   "security",
	}, "run-2", true, time.Unix(0, 0).UTC())

	if len(state.Tasks) != 2 {
		t.Fatalf("expected done history plus new task, got %#v", state.Tasks)
	}
	if state.Tasks[0].ID != "T-SEC-001" || state.Tasks[0].Status != "done" {
		t.Fatalf("existing terminal task should be preserved, got %#v", state.Tasks[0])
	}
	if selected.ID != "T-SEC-002" || selected.Status != "in_progress" {
		t.Fatalf("new colliding task should be renumbered and selected, got %#v", selected)
	}
	if state.ActiveTaskID == nil || *state.ActiveTaskID != "T-SEC-002" {
		t.Fatalf("active task should point at new task, got %#v", state.ActiveTaskID)
	}
}

func TestBuildTaskStateAlwaysAppendsDuplicateTaskIntent(t *testing.T) {
	before := TaskState{
		Version: 1,
		Tasks: []TaskRecord{{
			ID:     "T-SEC-001",
			Title:  "Centralize secret redaction",
			Mode:   "security",
			Status: "done",
		}},
	}
	planned := `{
		"version": 1,
		"tasks": [{
			"id": "T-SEC-001",
			"title": "Centralize secret redaction",
			"mode": "security",
			"priority": "high",
			"status": "queued",
			"reason": "same",
			"acceptance_criteria": ["works"]
		}]
	}`
	state, selected := buildTaskState(before, "plan", ai.MergeResult{Task: planned}, nil, TaskProposalResolution{
		Selected: TaskProposalModeAuto,
		Resolved: TaskProposalModeSecurity,
		Reason:   "security",
	}, "run-2", true, time.Unix(0, 0).UTC())

	if len(state.Tasks) != 2 {
		t.Fatalf("expected duplicate proposal to be appended, got %#v", state.Tasks)
	}
	if state.Tasks[0].Status != "done" {
		t.Fatalf("terminal task should remain done, got %#v", state.Tasks[0])
	}
	if selected.ID != "T-SEC-002" || selected.Status != "in_progress" || selected.Title != "Centralize secret redaction" {
		t.Fatalf("duplicate proposal should be renumbered and selected, got %#v", selected)
	}
	if state.ActiveTaskID == nil || *state.ActiveTaskID != "T-SEC-002" {
		t.Fatalf("active task should point at appended duplicate, got %#v", state.ActiveTaskID)
	}
}

func TestBuildTaskStateDemotesExistingActiveAndSelectsNewProposal(t *testing.T) {
	active := "T-FEATURE-001"
	before := TaskState{
		Version:      1,
		ActiveTaskID: &active,
		Tasks: []TaskRecord{{
			ID:     "T-FEATURE-001",
			Title:  "Old active work",
			Mode:   "feature",
			Status: "in_progress",
		}, {
			ID:     "T-FEATURE-002",
			Title:  "Existing queued work",
			Mode:   "feature",
			Status: "queued",
		}},
	}
	planned := `{
		"version": 1,
		"tasks": [{
			"id": "T-FEATURE-001",
			"title": "Fresh proposal",
			"mode": "feature",
			"priority": "high",
			"status": "queued",
			"reason": "new",
			"acceptance_criteria": ["works"]
		}]
	}`
	state, selected := buildTaskState(before, "plan", ai.MergeResult{Task: planned}, nil, TaskProposalResolution{
		Selected: TaskProposalModeFeature,
		Resolved: TaskProposalModeFeature,
		Reason:   "feature",
	}, "run-3", true, time.Unix(0, 0).UTC())

	if len(state.Tasks) != 3 {
		t.Fatalf("expected existing tasks plus appended proposal, got %#v", state.Tasks)
	}
	if state.Tasks[0].Status != "queued" || state.Tasks[1].Status != "queued" {
		t.Fatalf("existing active/in-progress task should be queued without losing history, got %#v", state.Tasks[:2])
	}
	if selected.ID != "T-FEATURE-003" || selected.Title != "Fresh proposal" || selected.Status != "in_progress" {
		t.Fatalf("new proposal should be selected with fresh id, got %#v", selected)
	}
	if state.ActiveTaskID == nil || *state.ActiveTaskID != "T-FEATURE-003" {
		t.Fatalf("active task should point at fresh proposal, got %#v", state.ActiveTaskID)
	}
}

func TestBuildTaskStateAppendsMultiplePlannerTasksAndSelectsFirst(t *testing.T) {
	before := TaskState{
		Version: 1,
		Tasks: []TaskRecord{{
			ID:     "T-DOCS-001",
			Title:  "Existing docs work",
			Mode:   "docs",
			Status: "done",
		}},
	}
	planned := `{
		"version": 1,
		"tasks": [{
			"id": "ignored-1",
			"title": "First proposed docs task",
			"mode": "docs",
			"priority": "high",
			"status": "done",
			"reason": "first",
			"acceptance_criteria": ["first works"]
		}, {
			"id": "ignored-2",
			"title": "Second proposed docs task",
			"mode": "docs",
			"priority": "medium",
			"status": "failed",
			"reason": "second",
			"acceptance_criteria": ["second works"]
		}]
	}`
	state, selected := buildTaskState(before, "plan", ai.MergeResult{Task: planned}, nil, TaskProposalResolution{
		Selected: TaskProposalModeDocs,
		Resolved: TaskProposalModeDocs,
		Reason:   "docs",
	}, "run-4", true, time.Unix(0, 0).UTC())

	if len(state.Tasks) != 3 {
		t.Fatalf("expected all proposed tasks to be appended, got %#v", state.Tasks)
	}
	if selected.ID != "T-DOCS-002" || selected.Title != "First proposed docs task" || selected.Status != "in_progress" {
		t.Fatalf("first appended task should be selected, got %#v", selected)
	}
	if state.Tasks[2].ID != "T-DOCS-003" || state.Tasks[2].Title != "Second proposed docs task" || state.Tasks[2].Status != "queued" {
		t.Fatalf("second proposed task should be appended as queued with fresh id, got %#v", state.Tasks[2])
	}
}

func TestBuildTaskStateDryRunAppendsQueuedProposalWithoutChangingActive(t *testing.T) {
	active := "T-FEATURE-001"
	before := TaskState{
		Version:      1,
		ActiveTaskID: &active,
		Tasks: []TaskRecord{{
			ID:     "T-FEATURE-001",
			Title:  "Existing active work",
			Mode:   "feature",
			Status: "in_progress",
		}},
	}
	planned := `{
		"version": 1,
		"tasks": [{
			"title": "Dry-run proposal",
			"mode": "feature",
			"priority": "high",
			"status": "queued",
			"reason": "preview",
			"acceptance_criteria": ["works"]
		}]
	}`
	state, selected := buildTaskState(before, "plan", ai.MergeResult{Task: planned}, nil, TaskProposalResolution{
		Selected: TaskProposalModeFeature,
		Resolved: TaskProposalModeFeature,
		Reason:   "feature",
	}, "run-5", false, time.Unix(0, 0).UTC())

	if len(state.Tasks) != 2 {
		t.Fatalf("expected dry-run proposal snapshot to append one task, got %#v", state.Tasks)
	}
	if state.Tasks[0].Status != "in_progress" || state.ActiveTaskID == nil || *state.ActiveTaskID != "T-FEATURE-001" {
		t.Fatalf("dry-run should preserve existing active task in snapshot, got state=%#v active=%#v", state.Tasks[0], state.ActiveTaskID)
	}
	if selected.ID != "T-FEATURE-002" || selected.Title != "Dry-run proposal" || selected.Status != "queued" {
		t.Fatalf("dry-run selected task should describe the appended queued proposal, got %#v", selected)
	}
}

func TestBuildPlanningContextUsesExistingSpecBeforePlanSeed(t *testing.T) {
	spec := SpecState{
		Version:      1,
		Title:        "Current Product SPEC",
		Summary:      "Current behavior is canonical.",
		Requirements: []string{"Keep current requirement."},
	}

	context := buildPlanningContext("Old plan seed.", spec, TaskState{Version: 1}, "Recent validation passed.", "")

	specIndex := strings.Index(context, "# Current SPEC State (source of truth)")
	planIndex := strings.Index(context, "# plan.md Seed (background product vision only)")
	if specIndex < 0 || planIndex < 0 || specIndex > planIndex {
		t.Fatalf("SPEC should be labeled source of truth before plan seed:\n%s", context)
	}
	if !strings.Contains(context, `"title": "Current Product SPEC"`) || !strings.Contains(context, "Recent validation passed.") {
		t.Fatalf("planning context missing spec or recent evidence:\n%s", context)
	}
}

func TestBuildPlanningContextBootstrapsFromPlanWithoutSpec(t *testing.T) {
	context := buildPlanningContext("Initial product vision.", SpecState{Version: 1}, TaskState{Version: 1}, "", "")

	for _, want := range []string{
		"No existing .jj/spec.json was found",
		"# plan.md Seed (initial source of truth)",
		"Initial product vision.",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("planning context missing %q:\n%s", want, context)
		}
	}
}

func TestBuildPlanningContextIncludesPriorityTaskIntentOverrideFirst(t *testing.T) {
	context := buildPlanningContext("Initial product vision.", SpecState{Version: 1}, TaskState{Version: 1}, "", "Ship the priority task.\n")

	priorityIndex := strings.Index(context, "# Priority Task Intent Override")
	specIndex := strings.Index(context, "# Current SPEC State")
	if priorityIndex < 0 || specIndex < 0 || priorityIndex > specIndex {
		t.Fatalf("priority task intent override should appear before normal context:\n%s", context)
	}
	for _, want := range []string{"Ship the priority task.", "highest-priority next-turn planning input", "Scope the first proposed runnable task to this intent", "Ignore task-proposal-mode"} {
		if !strings.Contains(context, want) {
			t.Fatalf("priority task context missing %q:\n%s", want, context)
		}
	}
}

func TestTaskProposalEvidenceIncludesPriorityTaskIntentOverride(t *testing.T) {
	evidence := buildTaskProposalEvidence(SpecState{Version: 1}, TaskState{Version: 1}, "", "Implement the priority override.")

	for _, want := range []string{"Priority task intent override from .jj/priority-task.md", "override task-proposal-mode", "Implement the priority override."} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("priority task evidence missing %q:\n%s", want, evidence)
		}
	}
}

func TestTaskProposalEvidenceIgnoresCompletedSecurityHistoryForAutoMode(t *testing.T) {
	evidence := buildTaskProposalEvidence(SpecState{Version: 1}, TaskState{
		Version: 1,
		Tasks: []TaskRecord{{
			ID:     "T-SEC-001",
			Title:  "Fix secret redaction security risk",
			Mode:   "security",
			Status: "done",
			Reason: "secret redaction security work",
		}},
	}, "", "")

	resolution := ResolveTaskProposalMode(TaskProposalModeAuto, evidence)
	if resolution.Resolved != TaskProposalModeFeature {
		t.Fatalf("old completed security history alone should not force security, got %#v with evidence:\n%s", resolution, evidence)
	}
}

func TestTaskProposalEvidenceFailedValidationResolvesBugfix(t *testing.T) {
	evidence := buildTaskProposalEvidence(SpecState{Version: 1}, TaskState{Version: 1}, "Previous validation failed: tests fail.", "")

	resolution := ResolveTaskProposalMode(TaskProposalModeAuto, evidence)
	if resolution.Resolved != TaskProposalModeBugfix {
		t.Fatalf("failed validation evidence should resolve bugfix, got %#v with evidence:\n%s", resolution, evidence)
	}
}

func TestTaskProposalEvidenceCurrentSpecSecurityRiskResolvesSecurity(t *testing.T) {
	evidence := buildTaskProposalEvidence(SpecState{
		Version:       1,
		Title:         "SPEC",
		Requirements:  []string{"Protect saved artifacts from secret exposure."},
		OpenQuestions: []string{"Is there a remaining security risk in dashboard access?"},
	}, TaskState{Version: 1}, "", "")

	resolution := ResolveTaskProposalMode(TaskProposalModeAuto, evidence)
	if resolution.Resolved != TaskProposalModeSecurity {
		t.Fatalf("current spec security risk should resolve security, got %#v with evidence:\n%s", resolution, evidence)
	}
}
