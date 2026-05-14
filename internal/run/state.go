package run

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jungju/jj/internal/artifact"
	ai "github.com/jungju/jj/internal/openai"
	"github.com/jungju/jj/internal/security"
)

const (
	DefaultSpecStatePath  = ".jj/spec.json"
	DefaultTasksStatePath = ".jj/tasks.json"
	DefaultNextIntentPath = ".jj/next-intent.md"
)

type SpecState struct {
	Version            int      `json:"version"`
	Title              string   `json:"title"`
	Summary            string   `json:"summary"`
	Goals              []string `json:"goals"`
	NonGoals           []string `json:"non_goals"`
	Requirements       []string `json:"requirements"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	OpenQuestions      []string `json:"open_questions"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

type TaskState struct {
	Version      int          `json:"version"`
	ActiveTaskID *string      `json:"active_task_id"`
	Tasks        []TaskRecord `json:"tasks"`
}

type TaskRecord struct {
	ID                       string   `json:"id"`
	Title                    string   `json:"title"`
	Mode                     string   `json:"mode"`
	SelectedTaskProposalMode string   `json:"selected_task_proposal_mode,omitempty"`
	ResolvedTaskProposalMode string   `json:"resolved_task_proposal_mode,omitempty"`
	Priority                 string   `json:"priority"`
	Status                   string   `json:"status"`
	Reason                   string   `json:"reason"`
	AcceptanceCriteria       []string `json:"acceptance_criteria"`
	ValidationCommand        string   `json:"validation_command,omitempty"`
	WorkBranch               string   `json:"work_branch,omitempty"`
	NextIntentHash           string   `json:"next_intent_hash,omitempty"`
	CreatedAt                string   `json:"created_at"`
	UpdatedAt                string   `json:"updated_at"`
	CreatedByRun             string   `json:"created_by_run"`
	CreatedByTurn            *string  `json:"created_by_turn"`
	CompletedByRun           *string  `json:"completed_by_run"`
	CompletedByTurn          *string  `json:"completed_by_turn"`
	Commit                   *string  `json:"commit"`
	Verdict                  *string  `json:"verdict"`
}

type stateSnapshot struct {
	SpecBefore  SpecState
	TasksBefore TaskState
}

func loadStateSnapshot(cwd string) stateSnapshot {
	return stateSnapshot{
		SpecBefore:  loadSpecState(cwd),
		TasksBefore: loadTaskState(cwd),
	}
}

func loadSpecState(cwd string) SpecState {
	state, _, _ := loadSpecStateFromStore(cwd)
	ensureSpecDefaults(&state)
	return state
}

func loadTaskState(cwd string) TaskState {
	state, _, _ := loadTaskStateFromStore(cwd)
	ensureTaskDefaults(&state)
	return state
}

func writeWorkspaceJSONAndStore(cwd, rel string, value any, store artifact.Store) error {
	data, err := writeWorkspaceStateDocument(cwd, rel, value)
	if err != nil {
		return err
	}
	return store.SaveDocument(rel, data)
}

func marshalWorkspaceJSON(value any) ([]byte, error) {
	redacted, _ := security.RedactJSONValueWithCount(value)
	data, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(redactSecrets(string(data))), '\n'), nil
}

func writeSnapshotJSON(store artifact.Store, rel string, value any) (string, error) {
	return writeRedactedJSON(store, rel, value)
}

func buildPlanningContext(plan string, spec SpecState, tasks TaskState, continuation string, nextIntent string) string {
	var b strings.Builder
	if strings.TrimSpace(nextIntent) != "" {
		b.WriteString("# Next Intent Override\n\n")
		b.WriteString("The following local operator intent from .jj/next-intent.md is the highest-priority next-turn planning input. Scope the first proposed runnable task to this intent. Ignore task-proposal-mode, resolved mode, and auto/balanced detection when choosing what to plan; use mode only after the intent is satisfied as category metadata or fallback guidance.\n\n")
		b.WriteString(truncateString(sanitizeHandoffText(nextIntent), 16000))
		b.WriteString("\n\n")
	}
	if specHasContent(spec) {
		b.WriteString("# Current SPEC State (source of truth)\n\n")
		b.WriteString(mustCompactJSON(spec))
		b.WriteString("\n\n")
	} else {
		b.WriteString("# Current SPEC State\n\n")
		b.WriteString("No existing SQLite workspace SPEC state was found. Bootstrap the first SPEC from the docs/PLAN.md seed.\n\n")
	}
	b.WriteString("# Current Task State Summary\n\n")
	b.WriteString(taskStateSummary(tasks, true))
	b.WriteString("\n\n")
	if strings.TrimSpace(continuation) != "" {
		b.WriteString("# Recent Run Evidence\n\n")
		b.WriteString(truncateString(sanitizeHandoffText(continuation), 16000))
		b.WriteString("\n\n")
	}
	if specHasContent(spec) {
		b.WriteString("# docs/PLAN.md Seed (background product vision only)\n\n")
	} else {
		b.WriteString("# docs/PLAN.md Seed (initial source of truth)\n\n")
	}
	b.WriteString(truncateString(sanitizeHandoffText(plan), 16000))
	b.WriteString("\n")
	return sanitizeHandoffText(b.String())
}

func buildTaskProposalEvidence(spec SpecState, tasks TaskState, continuation string, nextIntent string) string {
	var b strings.Builder
	if strings.TrimSpace(nextIntent) != "" {
		b.WriteString("Next intent override from .jj/next-intent.md:\n")
		b.WriteString("- This free-form intent is the highest-priority next task input and should override task-proposal-mode, resolved mode, and auto/balanced detection when choosing what to plan.\n")
		b.WriteString("- Scope the first proposed runnable task to this intent; use mode only afterward as category metadata or fallback guidance.\n")
		for _, line := range strings.Split(truncateString(sanitizeHandoffText(nextIntent), 4000), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				b.WriteString("- ")
				b.WriteString(trimmed)
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}
	if specHasContent(spec) {
		b.WriteString("Current SPEC requirements and open questions:\n")
		for _, item := range append(append([]string{}, spec.Requirements...), spec.OpenQuestions...) {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				b.WriteString("- ")
				b.WriteString(sanitizeHandoffText(trimmed))
				b.WriteByte('\n')
			}
		}
	} else {
		b.WriteString("No current SPEC exists; bootstrap from docs/PLAN.md seed.\n")
	}
	b.WriteString("\nNon-terminal task state:\n")
	for _, task := range tasks.Tasks {
		if !taskRunnable(task.Status) {
			continue
		}
		fmt.Fprintf(&b, "- %s [%s/%s] %s\n", task.ID, task.Mode, task.Status, sanitizeHandoffText(task.Title))
	}
	closedCount := 0
	for _, task := range tasks.Tasks {
		if !taskRunnable(task.Status) {
			closedCount++
		}
	}
	fmt.Fprintf(&b, "\nClosed task history count: %d\n", closedCount)
	if recent := recentFailureEvidence(continuation); recent != "" {
		b.WriteString("\nRecent failure evidence:\n")
		b.WriteString(recent)
	}
	if recent := recentSecurityEvidence(continuation); recent != "" {
		b.WriteString("\nRecent security evidence:\n")
		b.WriteString(recent)
	}
	return sanitizeHandoffText(b.String())
}

func specHasContent(spec SpecState) bool {
	return strings.TrimSpace(spec.Title) != "" ||
		strings.TrimSpace(spec.Summary) != "" ||
		len(nonEmptyPlanningItems(spec.Goals)) > 0 ||
		len(nonEmptyPlanningItems(spec.Requirements)) > 0 ||
		len(nonEmptyPlanningItems(spec.AcceptanceCriteria)) > 0 ||
		len(nonEmptyPlanningItems(spec.OpenQuestions)) > 0
}

func taskStateSummary(tasks TaskState, includeCompletedTitles bool) string {
	counts := map[string]int{}
	for _, task := range tasks.Tasks {
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if status == "" {
			status = "queued"
		}
		counts[status]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Total tasks: %d\n", len(tasks.Tasks))
	for _, status := range []string{"queued", "active", "in_progress", "done", "failed", "blocked", "skipped", "superseded"} {
		if counts[status] > 0 {
			fmt.Fprintf(&b, "- %s: %d\n", status, counts[status])
		}
	}
	if tasks.ActiveTaskID != nil {
		fmt.Fprintf(&b, "Active task id: %s\n", sanitizeHandoffText(*tasks.ActiveTaskID))
	}
	b.WriteString("\nRunnable tasks:\n")
	runnable := 0
	for _, task := range tasks.Tasks {
		if !taskRunnable(task.Status) {
			continue
		}
		runnable++
		fmt.Fprintf(&b, "- %s [%s/%s] %s\n", task.ID, task.Mode, task.Status, sanitizeHandoffText(task.Title))
	}
	if runnable == 0 {
		b.WriteString("- none\n")
	}
	if includeCompletedTitles {
		b.WriteString("\nRecent completed task titles:\n")
		wrote := 0
		for i := len(tasks.Tasks) - 1; i >= 0 && wrote < 10; i-- {
			task := tasks.Tasks[i]
			if strings.EqualFold(strings.TrimSpace(task.Status), "done") {
				fmt.Fprintf(&b, "- %s [%s] %s\n", task.ID, task.Mode, sanitizeHandoffText(task.Title))
				wrote++
			}
		}
		if wrote == 0 {
			b.WriteString("- none\n")
		}
	}
	return b.String()
}

func terminalTaskCountLines(tasks TaskState) []string {
	counts := map[string]int{}
	for _, task := range tasks.Tasks {
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if taskRunnable(status) {
			continue
		}
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	var out []string
	for _, status := range []string{"done", "failed", "blocked", "skipped", "superseded", "unknown"} {
		if counts[status] > 0 {
			out = append(out, fmt.Sprintf("%s: %d", status, counts[status]))
		}
	}
	if len(out) == 0 {
		out = append(out, "none")
	}
	return out
}

func recentFailureEvidence(continuation string) string {
	categories := positiveBugfixEvidenceCategories(continuationBugfixEvidenceScope(continuation))
	if len(categories) == 0 {
		return ""
	}
	var b strings.Builder
	for _, category := range categories {
		b.WriteString("- ")
		b.WriteString(category)
		b.WriteByte('\n')
	}
	return b.String()
}

func recentSecurityEvidence(continuation string) string {
	categories := positiveSecurityEvidenceCategories(continuationSecurityEvidenceScope(continuation))
	if len(categories) == 0 {
		return ""
	}
	var b strings.Builder
	for _, category := range categories {
		b.WriteString("- ")
		b.WriteString(category)
		b.WriteByte('\n')
	}
	return b.String()
}

func continuationBugfixEvidenceScope(continuation string) string {
	if !strings.Contains(continuation, "## ") {
		return continuation
	}
	sections := []string{
		sectionBetween(continuation, "## Previous Manifest", "## "),
		sectionBetween(continuation, "## Previous Validation Summary", "## "),
	}
	return strings.Join(nonEmptyPlanningItems(sections), "\n")
}

func continuationSecurityEvidenceScope(continuation string) string {
	if !strings.Contains(continuation, "## ") {
		return continuation
	}
	sections := []string{
		sectionBetween(continuation, "## Previous Manifest Summary", "## "),
		sectionBetween(continuation, "## Previous Manifest", "## "),
		sectionBetween(continuation, "## Previous Validation Summary", "## "),
		sectionBetween(continuation, "## Previous Git Diff Summary", "## "),
		sectionBetween(continuation, "## Previous Codex Summary", "## "),
	}
	return strings.Join(nonEmptyPlanningItems(sections), "\n")
}

func buildSpecState(plan string, merged ai.MergeResult, drafts []ai.PlanningDraft, proposal TaskProposalResolution, before SpecState, now time.Time) SpecState {
	if parsed, ok := parseSpecStateString(merged.Spec); ok {
		parsed.Version = 1
		if strings.TrimSpace(parsed.CreatedAt) == "" {
			parsed.CreatedAt = firstNonEmptyString(before.CreatedAt, now.Format(time.RFC3339))
		}
		parsed.UpdatedAt = now.Format(time.RFC3339)
		return redactSpecState(parsed)
	}
	createdAt := firstNonEmptyString(before.CreatedAt, now.Format(time.RFC3339))
	summary := firstNonEmptyString(firstMarkdownParagraph(merged.Spec), summarizePlain(plan, 320), "jj run state generated from the supplied plan.")
	state := SpecState{
		Version:            1,
		Title:              firstNonEmptyString(firstMarkdownHeading(merged.Spec), before.Title, "jj"),
		Summary:            summary,
		Goals:              uniqueStrings(nonEmptyPlanningItems(append(markdownSectionItems(merged.Spec, "Goals"), draftSummaries(drafts)...))...),
		NonGoals:           uniqueStrings(markdownSectionItems(merged.Spec, "Non-Goals")...),
		Requirements:       uniqueStrings(markdownSectionItems(merged.Spec, "Functional Requirements", "Requirements", "CLI Behavior", "Pipeline Behavior", "Artifact Layout")...),
		AcceptanceCriteria: uniqueStrings(append(mergedAcceptanceCriteria(drafts), markdownSectionItems(merged.Spec, "Acceptance Criteria")...)...),
		OpenQuestions:      uniqueStrings(markdownSectionItems(merged.Spec, "Open Questions", "Unknowns")...),
		CreatedAt:          createdAt,
		UpdatedAt:          now.Format(time.RFC3339),
	}
	if len(state.Goals) == 0 {
		state.Goals = []string{TaskProposalTaskTitle(proposal.Resolved)}
	}
	if len(state.Requirements) == 0 {
		state.Requirements = []string{summarizePlain(plan, 220)}
	}
	if len(state.AcceptanceCriteria) == 0 {
		state.AcceptanceCriteria = []string{"The requested jj workflow change is implemented and covered by deterministic validation."}
	}
	return redactSpecState(state)
}

func parseSpecStateString(raw string) (SpecState, bool) {
	var state SpecState
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &state) != nil {
		return SpecState{}, false
	}
	return state, state.Version != 0 || strings.TrimSpace(state.Summary) != ""
}

func buildReconciledSpecState(previous, planned SpecState, result ai.ReconcileSpecResult, now time.Time) (SpecState, error) {
	parsed, ok := parseSpecStateString(result.Spec)
	if !ok {
		return SpecState{}, fmt.Errorf("reconciled SPEC content is empty or invalid")
	}
	parsed.Version = 1
	parsed.CreatedAt = firstNonEmptyString(parsed.CreatedAt, previous.CreatedAt, planned.CreatedAt, now.Format(time.RFC3339))
	parsed.UpdatedAt = now.Format(time.RFC3339)
	return redactSpecState(parsed), nil
}

func redactSpecState(state SpecState) SpecState {
	data, _ := json.Marshal(security.SanitizeHandoffJSONValue(state))
	var out SpecState
	_ = json.Unmarshal([]byte(sanitizeHandoffText(string(data))), &out)
	return out
}

func buildTaskState(before TaskState, plan string, merged ai.MergeResult, drafts []ai.PlanningDraft, proposal TaskProposalResolution, runID string, inProgress bool, now time.Time) (TaskState, TaskRecord) {
	if parsed, ok := parseTaskStateString(merged.Task); ok && len(parsed.Tasks) > 0 {
		return appendPlannedTaskState(before, parsed.Tasks, proposal, runID, inProgress, now)
	}
	task := TaskRecord{
		Title:                    TaskProposalTaskTitle(proposal.Resolved),
		Mode:                     string(proposal.Resolved),
		SelectedTaskProposalMode: string(proposal.Selected),
		ResolvedTaskProposalMode: string(proposal.Resolved),
		Priority:                 "high",
		Status:                   "queued",
		Reason:                   firstNonEmptyString(proposal.Reason, summarizePlain(plan, 220)),
		AcceptanceCriteria:       taskAcceptanceCriteria(merged.Task, drafts),
		ValidationCommand:        defaultValidationCommand,
	}
	return appendPlannedTaskState(before, []TaskRecord{task}, proposal, runID, inProgress, now)
}

func parseTaskStateString(raw string) (TaskState, bool) {
	var state TaskState
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &state) != nil {
		return TaskState{}, false
	}
	return state, state.Version != 0 || len(state.Tasks) > 0
}

func appendPlannedTaskState(before TaskState, planned []TaskRecord, proposal TaskProposalResolution, runID string, inProgress bool, now time.Time) (TaskState, TaskRecord) {
	state := normalizeExistingTaskState(before)
	if inProgress {
		state = demoteExistingActiveTasks(state, now)
	}
	selectedID := ""
	for _, task := range planned {
		task = normalizePlannedTask(task, proposal, runID, now)
		task.ID = nextTaskID(state, taskMode(task, proposal))
		task.Status = "queued"
		task.CompletedByRun = nil
		task.CompletedByTurn = nil
		task.Commit = nil
		task.Verdict = nil
		task.CreatedAt = now.Format(time.RFC3339)
		task.UpdatedAt = now.Format(time.RFC3339)
		task.CreatedByRun = runID
		task.CreatedByTurn = nil
		state.Tasks = append(state.Tasks, task)
		if selectedID == "" {
			selectedID = task.ID
		}
	}
	if inProgress && selectedID != "" {
		idx := taskIndexByID(state, selectedID)
		if idx >= 0 {
			state.Tasks[idx].Status = "in_progress"
			state.Tasks[idx].UpdatedAt = now.Format(time.RFC3339)
			id := state.Tasks[idx].ID
			state.ActiveTaskID = &id
		}
	}
	redacted := redactTaskState(state)
	if selectedID != "" {
		return redacted, taskByID(redacted, selectedID, proposal)
	}
	return redacted, selectedTask(redacted, proposal)
}

func selectExistingRunnableTask(state TaskState) (TaskRecord, bool) {
	state = normalizeExistingTaskState(state)
	if state.ActiveTaskID != nil {
		idx := taskIndexByID(state, *state.ActiveTaskID)
		if idx >= 0 && taskRunnable(state.Tasks[idx].Status) {
			return state.Tasks[idx], true
		}
	}
	for _, task := range state.Tasks {
		switch strings.ToLower(strings.TrimSpace(task.Status)) {
		case "active", "in_progress":
			return task, true
		}
	}
	for _, task := range state.Tasks {
		if strings.EqualFold(strings.TrimSpace(task.Status), "queued") {
			return task, true
		}
	}
	return TaskRecord{}, false
}

func buildExistingRunnableTaskState(before TaskState, proposal TaskProposalResolution, inProgress bool, now time.Time) (TaskState, TaskRecord, bool) {
	state := normalizeExistingTaskState(before)
	selected, ok := selectExistingRunnableTask(state)
	if !ok {
		return redactTaskState(state), TaskRecord{}, false
	}
	idx := taskIndexByID(state, selected.ID)
	if idx < 0 {
		return redactTaskState(state), TaskRecord{}, false
	}
	if inProgress {
		state = demoteExistingActiveTasksExcept(state, selected.ID, now)
		idx = taskIndexByID(state, selected.ID)
		if idx >= 0 {
			state.Tasks[idx].Status = "in_progress"
			state.Tasks[idx].UpdatedAt = now.Format(time.RFC3339)
			if strings.TrimSpace(state.Tasks[idx].ValidationCommand) == "" {
				state.Tasks[idx].ValidationCommand = defaultValidationCommand
			}
			id := state.Tasks[idx].ID
			state.ActiveTaskID = &id
		}
	}
	redacted := redactTaskState(state)
	return redacted, taskByID(redacted, selected.ID, proposal), true
}

func normalizeExistingTaskState(state TaskState) TaskState {
	if state.Version == 0 {
		state.Version = 1
	}
	if state.ActiveTaskID != nil {
		idx := taskIndexByID(state, *state.ActiveTaskID)
		if idx < 0 || !taskRunnable(state.Tasks[idx].Status) {
			state.ActiveTaskID = nil
		}
	}
	for i := range state.Tasks {
		if strings.TrimSpace(state.Tasks[i].Status) == "" {
			state.Tasks[i].Status = "queued"
		}
	}
	return state
}

func demoteExistingActiveTasks(state TaskState, now time.Time) TaskState {
	for i := range state.Tasks {
		switch strings.ToLower(strings.TrimSpace(state.Tasks[i].Status)) {
		case "active", "in_progress":
			state.Tasks[i].Status = "queued"
			state.Tasks[i].UpdatedAt = now.Format(time.RFC3339)
		}
	}
	state.ActiveTaskID = nil
	return state
}

func demoteExistingActiveTasksExcept(state TaskState, keepID string, now time.Time) TaskState {
	for i := range state.Tasks {
		if state.Tasks[i].ID == keepID {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(state.Tasks[i].Status)) {
		case "active", "in_progress":
			state.Tasks[i].Status = "queued"
			state.Tasks[i].UpdatedAt = now.Format(time.RFC3339)
		}
	}
	state.ActiveTaskID = nil
	return state
}

func normalizePlannedTask(task TaskRecord, proposal TaskProposalResolution, runID string, now time.Time) TaskRecord {
	task.ID = strings.TrimSpace(task.ID)
	if strings.TrimSpace(task.Mode) == "" {
		task.Mode = string(concreteTaskProposalMode(proposal.Resolved))
	}
	if strings.TrimSpace(task.SelectedTaskProposalMode) == "" {
		task.SelectedTaskProposalMode = string(proposal.Selected)
	}
	if strings.TrimSpace(task.ResolvedTaskProposalMode) == "" {
		task.ResolvedTaskProposalMode = string(concreteTaskProposalMode(proposal.Resolved))
	}
	if strings.TrimSpace(task.Priority) == "" {
		task.Priority = "high"
	}
	if strings.TrimSpace(task.Status) == "" {
		task.Status = "queued"
	}
	if strings.TrimSpace(task.ValidationCommand) == "" {
		task.ValidationCommand = defaultValidationCommand
	}
	if strings.TrimSpace(task.CreatedAt) == "" {
		task.CreatedAt = now.Format(time.RFC3339)
	}
	task.UpdatedAt = now.Format(time.RFC3339)
	if strings.TrimSpace(task.CreatedByRun) == "" {
		task.CreatedByRun = runID
	}
	return task
}

func normalizeTaskState(state TaskState, proposal TaskProposalResolution, runID string, inProgress bool, now time.Time) TaskState {
	if state.Version == 0 {
		state.Version = 1
	}
	for i := range state.Tasks {
		if strings.TrimSpace(state.Tasks[i].ID) == "" {
			state.Tasks[i].ID = nextTaskID(state, proposal.Resolved)
		}
		if strings.TrimSpace(state.Tasks[i].Mode) == "" {
			state.Tasks[i].Mode = string(concreteTaskProposalMode(proposal.Resolved))
		}
		if strings.TrimSpace(state.Tasks[i].SelectedTaskProposalMode) == "" {
			state.Tasks[i].SelectedTaskProposalMode = string(proposal.Selected)
		}
		if strings.TrimSpace(state.Tasks[i].ResolvedTaskProposalMode) == "" {
			state.Tasks[i].ResolvedTaskProposalMode = string(concreteTaskProposalMode(proposal.Resolved))
		}
		if strings.TrimSpace(state.Tasks[i].Status) == "" {
			state.Tasks[i].Status = "queued"
		}
		if strings.TrimSpace(state.Tasks[i].Priority) == "" {
			state.Tasks[i].Priority = "high"
		}
		if strings.TrimSpace(state.Tasks[i].ValidationCommand) == "" {
			state.Tasks[i].ValidationCommand = defaultValidationCommand
		}
		if strings.TrimSpace(state.Tasks[i].CreatedAt) == "" {
			state.Tasks[i].CreatedAt = now.Format(time.RFC3339)
		}
		state.Tasks[i].UpdatedAt = now.Format(time.RFC3339)
		if strings.TrimSpace(state.Tasks[i].CreatedByRun) == "" {
			state.Tasks[i].CreatedByRun = runID
		}
	}
	if inProgress && state.ActiveTaskID == nil && len(state.Tasks) > 0 {
		state.Tasks[0].Status = "in_progress"
		id := state.Tasks[0].ID
		state.ActiveTaskID = &id
	}
	return redactTaskState(state)
}

func selectedTask(state TaskState, proposal TaskProposalResolution) TaskRecord {
	if state.ActiveTaskID != nil {
		for _, task := range state.Tasks {
			if task.ID == *state.ActiveTaskID && taskRunnable(task.Status) {
				return task
			}
		}
	}
	for _, task := range state.Tasks {
		if taskRunnable(task.Status) {
			return task
		}
	}
	return TaskRecord{
		ID:                TaskProposalTaskID(proposal.Resolved),
		Title:             TaskProposalTaskTitle(proposal.Resolved),
		Mode:              string(proposal.Resolved),
		Priority:          "high",
		Status:            "queued",
		ValidationCommand: defaultValidationCommand,
	}
}

func taskByID(state TaskState, id string, proposal TaskProposalResolution) TaskRecord {
	for _, task := range state.Tasks {
		if task.ID == id {
			return task
		}
	}
	return selectedTask(state, proposal)
}

func ensureActiveTask(state TaskState, proposal TaskProposalResolution, runID string, now time.Time) TaskState {
	if state.ActiveTaskID != nil {
		if idx := taskIndexByID(state, *state.ActiveTaskID); idx >= 0 && taskRunnable(state.Tasks[idx].Status) {
			state.Tasks[idx].Status = "in_progress"
			state.Tasks[idx].UpdatedAt = now.Format(time.RFC3339)
			return state
		}
		state.ActiveTaskID = nil
	}
	for i := range state.Tasks {
		if taskRunnable(state.Tasks[i].Status) {
			state.Tasks[i].Status = "in_progress"
			state.Tasks[i].UpdatedAt = now.Format(time.RFC3339)
			id := state.Tasks[i].ID
			state.ActiveTaskID = &id
			return state
		}
	}
	task := TaskRecord{
		ID:                       nextTaskID(state, proposal.Resolved),
		Title:                    TaskProposalTaskTitle(proposal.Resolved),
		Mode:                     string(proposal.Resolved),
		SelectedTaskProposalMode: string(proposal.Selected),
		ResolvedTaskProposalMode: string(proposal.Resolved),
		Priority:                 "high",
		Status:                   "in_progress",
		Reason:                   firstNonEmptyString(proposal.Reason, "No runnable task remained after merging planner output."),
		AcceptanceCriteria:       []string{"The selected task is implemented and deterministic validation passes."},
		ValidationCommand:        defaultValidationCommand,
		CreatedAt:                now.Format(time.RFC3339),
		UpdatedAt:                now.Format(time.RFC3339),
		CreatedByRun:             runID,
	}
	state.Tasks = append(state.Tasks, task)
	state.ActiveTaskID = &state.Tasks[len(state.Tasks)-1].ID
	return state
}

func mergeTaskRecord(existing, planned TaskRecord, now time.Time) TaskRecord {
	existing.Title = firstNonEmptyString(planned.Title, existing.Title)
	existing.Mode = firstNonEmptyString(planned.Mode, existing.Mode)
	existing.SelectedTaskProposalMode = firstNonEmptyString(planned.SelectedTaskProposalMode, existing.SelectedTaskProposalMode)
	existing.ResolvedTaskProposalMode = firstNonEmptyString(planned.ResolvedTaskProposalMode, existing.ResolvedTaskProposalMode)
	existing.Priority = firstNonEmptyString(planned.Priority, existing.Priority)
	existing.Reason = firstNonEmptyString(planned.Reason, existing.Reason)
	if len(planned.AcceptanceCriteria) > 0 {
		existing.AcceptanceCriteria = planned.AcceptanceCriteria
	}
	existing.ValidationCommand = firstNonEmptyString(planned.ValidationCommand, existing.ValidationCommand)
	existing.UpdatedAt = now.Format(time.RFC3339)
	return existing
}

func taskRunnable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "active", "in_progress":
		return true
	default:
		return false
	}
}

func taskIndexByID(state TaskState, id string) int {
	id = strings.TrimSpace(id)
	if id == "" {
		return -1
	}
	for i, task := range state.Tasks {
		if task.ID == id {
			return i
		}
	}
	return -1
}

func taskIndexByIntent(state TaskState, task TaskRecord) int {
	for i, existing := range state.Tasks {
		if sameTaskIntent(existing, task) {
			return i
		}
	}
	return -1
}

func sameTaskIntent(a, b TaskRecord) bool {
	return normalizeTaskText(a.Title) != "" &&
		normalizeTaskText(a.Title) == normalizeTaskText(b.Title) &&
		normalizeTaskText(a.Mode) == normalizeTaskText(b.Mode)
}

func normalizeTaskText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

func taskMode(task TaskRecord, proposal TaskProposalResolution) TaskProposalMode {
	if mode, err := ParseTaskProposalMode(task.Mode); err == nil && mode.Valid() {
		return concreteTaskProposalMode(mode)
	}
	return concreteTaskProposalMode(proposal.Resolved)
}

func updateTaskAfterValidation(state TaskState, taskID, runID string, validation ManifestValidation, commitSHA string, now time.Time) TaskState {
	verdict := validationTaskVerdict(validation)
	for i := range state.Tasks {
		if state.Tasks[i].ID != taskID {
			continue
		}
		switch verdict {
		case validationStatusPassed:
			state.Tasks[i].Status = "done"
		case validationStatusFailed:
			state.Tasks[i].Status = "failed"
		default:
			state.Tasks[i].Status = "blocked"
		}
		state.Tasks[i].UpdatedAt = now.Format(time.RFC3339)
		state.Tasks[i].CompletedByRun = stringPtr(runID)
		state.Tasks[i].Verdict = stringPtr(verdict)
		if strings.TrimSpace(commitSHA) != "" {
			state.Tasks[i].Commit = stringPtr(commitSHA)
		}
		break
	}
	state.ActiveTaskID = nil
	return redactTaskState(state)
}

func validationTaskVerdict(validation ManifestValidation) string {
	switch validation.Status {
	case validationStatusPassed:
		return validationStatusPassed
	case validationStatusFailed:
		return validationStatusFailed
	case validationStatusMissing:
		return validationStatusMissing
	case validationStatusSkipped:
		return validationStatusSkipped
	default:
		return "blocked"
	}
}

func redactTaskState(state TaskState) TaskState {
	data, _ := json.Marshal(security.SanitizeHandoffJSONValue(state))
	var out TaskState
	_ = json.Unmarshal([]byte(sanitizeHandoffText(string(data))), &out)
	return out
}

func mustCompactJSON(value any) string {
	data, err := json.MarshalIndent(security.SanitizeHandoffJSONValue(value), "", "  ")
	if err != nil {
		return "{}"
	}
	return sanitizeHandoffText(string(data))
}

func codexJSONPrompt(spec SpecState, task TaskRecord, proposal TaskProposalResolution) string {
	specSummary, _ := json.MarshalIndent(security.SanitizeHandoffJSONValue(struct {
		Title              string   `json:"title"`
		Summary            string   `json:"summary"`
		Goals              []string `json:"goals,omitempty"`
		Requirements       []string `json:"requirements,omitempty"`
		AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	}{spec.Title, spec.Summary, spec.Goals, spec.Requirements, spec.AcceptanceCriteria}), "", "  ")
	taskJSON, _ := json.MarshalIndent(security.SanitizeHandoffJSONValue(task), "", "  ")
	return sanitizeHandoffText(fmt.Sprintf(`You are running inside jj, a Go CLI that orchestrates planning, implementation, and validation.

Implement the selected task using this compact JSON context.

SPEC summary:
%s

Selected task:
%s

Task Proposal Mode:
- selected: %s
- resolved: %s

Requirements:
- Work in this repository only.
- Keep changes focused on the selected task and its acceptance criteria.
- Do not rewrite jj workspace state (data/documents.sqlite3 or legacy .jj/spec.json/.jj/tasks.json), or .jj/runs; jj owns those state files.
- Choose and run relevant tests yourself.
- In your final response, include changed files, tests run with results, and remaining risks.
`, specSummary, taskJSON, proposal.Selected, proposal.Resolved))
}

func taskAcceptanceCriteria(taskMarkdown string, drafts []ai.PlanningDraft) []string {
	items := markdownSectionItems(taskMarkdown, "Acceptance Criteria", "Done Criteria")
	items = append(items, mergedAcceptanceCriteria(drafts)...)
	items = uniqueStrings(items...)
	if len(items) == 0 {
		items = []string{
			"The selected task is implemented with a focused code or documentation change.",
			"Deterministic validation covers the changed behavior.",
		}
	}
	return items
}

func mergedAcceptanceCriteria(drafts []ai.PlanningDraft) []string {
	var items []string
	for _, draft := range drafts {
		items = append(items, draft.AcceptanceCriteria...)
	}
	return uniqueStrings(items...)
}

func draftSummaries(drafts []ai.PlanningDraft) []string {
	var items []string
	for _, draft := range drafts {
		if strings.TrimSpace(draft.Summary) != "" {
			items = append(items, draft.Summary)
		}
	}
	return items
}

func markdownSectionItems(markdown string, sectionNames ...string) []string {
	return extractMarkdownSectionItems(markdown, sectionNames...)
}

func firstMarkdownHeading(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func firstMarkdownParagraph(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "- ") {
			continue
		}
		return line
	}
	return ""
}

func summarizePlain(s string, max int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if s == "" {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "..."
}

func uniqueStrings(items ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(sanitizeHandoffText(item))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func nextTaskID(state TaskState, _ TaskProposalMode) string {
	max := len(state.Tasks)
	for _, task := range state.Tasks {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(task.ID), "TASK-%04d", &n); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("TASK-%04d", max+1)
}

func changedFilesFromNameStatus(nameStatus string) []string {
	var out []string
	for _, line := range strings.Split(nameStatus, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		out = append(out, sanitizeHandoffText(fields[len(fields)-1]))
	}
	return uniqueStrings(out...)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func stringPtr(s string) *string {
	return &s
}
