package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jungju/jj/internal/artifact"
	ai "github.com/jungju/jj/internal/openai"
	"github.com/jungju/jj/internal/security"
)

const (
	DefaultSpecStatePath    = ".jj/spec.json"
	DefaultTasksStatePath   = ".jj/tasks.json"
	DefaultPriorityTaskPath = ".jj/priority-task.md"
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
	var state SpecState
	_ = readWorkspaceJSON(cwd, DefaultSpecStatePath, &state)
	if state.Version == 0 {
		state.Version = 1
	}
	return state
}

func loadTaskState(cwd string) TaskState {
	var state TaskState
	_ = readWorkspaceJSON(cwd, DefaultTasksStatePath, &state)
	if state.Version == 0 {
		state.Version = 1
	}
	return state
}

func readWorkspaceJSON(cwd, rel string, target any) error {
	path, err := security.SafeJoinNoSymlinks(cwd, rel, security.PathPolicy{AllowHidden: true})
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeWorkspaceJSON(cwd, rel string, value any) error {
	path, err := security.SafeJoinNoSymlinks(cwd, rel, security.PathPolicy{AllowHidden: true})
	if err != nil {
		return err
	}
	redacted, _ := security.RedactJSONValueWithCount(value)
	data, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return err
	}
	data = append([]byte(redactSecrets(string(data))), '\n')
	if err := os.MkdirAll(filepath.Dir(path), artifact.PrivateDirMode); err != nil {
		return err
	}
	path, err = security.SafeJoinNoSymlinks(cwd, rel, security.PathPolicy{AllowHidden: true})
	if err != nil {
		return err
	}
	return artifact.AtomicWriteFile(path, data, artifact.PrivateFileMode)
}

func writeSnapshotJSON(store artifact.Store, rel string, value any) (string, error) {
	return writeRedactedJSON(store, rel, value)
}

func buildPlanningContext(plan string, spec SpecState, tasks TaskState, continuation string, priorityTask string) string {
	var b strings.Builder
	if strings.TrimSpace(priorityTask) != "" {
		b.WriteString("# Priority Task Intent Override\n\n")
		b.WriteString("The following local operator intent from .jj/priority-task.md is the highest-priority next-turn planning input. Scope the first proposed runnable task to this intent. Ignore task-proposal-mode, resolved mode, and auto/balanced detection when choosing what to plan; use mode only after the intent is satisfied as category metadata or fallback guidance.\n\n")
		b.WriteString(truncateString(redactSecrets(priorityTask), 16000))
		b.WriteString("\n\n")
	}
	if specHasContent(spec) {
		b.WriteString("# Current SPEC State (source of truth)\n\n")
		b.WriteString(mustCompactJSON(spec))
		b.WriteString("\n\n")
	} else {
		b.WriteString("# Current SPEC State\n\n")
		b.WriteString("No existing .jj/spec.json was found. Bootstrap the first SPEC from the plan.md seed.\n\n")
	}
	b.WriteString("# Current Task State Summary\n\n")
	b.WriteString(taskStateSummary(tasks, true))
	b.WriteString("\n\n")
	if strings.TrimSpace(continuation) != "" {
		b.WriteString("# Recent Run Evidence\n\n")
		b.WriteString(truncateString(redactSecrets(continuation), 16000))
		b.WriteString("\n\n")
	}
	if specHasContent(spec) {
		b.WriteString("# plan.md Seed (background product vision only)\n\n")
	} else {
		b.WriteString("# plan.md Seed (initial source of truth)\n\n")
	}
	b.WriteString(truncateString(redactSecrets(plan), 16000))
	b.WriteString("\n")
	return redactSecrets(b.String())
}

func buildTaskProposalEvidence(spec SpecState, tasks TaskState, continuation string, priorityTask string) string {
	var b strings.Builder
	if strings.TrimSpace(priorityTask) != "" {
		b.WriteString("Priority task intent override from .jj/priority-task.md:\n")
		b.WriteString("- This free-form intent is the highest-priority next task input and should override task-proposal-mode, resolved mode, and auto/balanced detection when choosing what to plan.\n")
		b.WriteString("- Scope the first proposed runnable task to this intent; use mode only afterward as category metadata or fallback guidance.\n")
		for _, line := range strings.Split(truncateString(redactSecrets(priorityTask), 4000), "\n") {
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
				b.WriteString(redactSecrets(trimmed))
				b.WriteByte('\n')
			}
		}
	} else {
		b.WriteString("No current SPEC exists; bootstrap from plan.md seed.\n")
	}
	b.WriteString("\nNon-terminal task state:\n")
	for _, task := range tasks.Tasks {
		if !taskRunnable(task.Status) {
			continue
		}
		fmt.Fprintf(&b, "- %s [%s/%s] %s\n", task.ID, task.Mode, task.Status, redactSecrets(task.Title))
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
	return redactSecrets(b.String())
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
		fmt.Fprintf(&b, "Active task id: %s\n", redactSecrets(*tasks.ActiveTaskID))
	}
	b.WriteString("\nRunnable tasks:\n")
	runnable := 0
	for _, task := range tasks.Tasks {
		if !taskRunnable(task.Status) {
			continue
		}
		runnable++
		fmt.Fprintf(&b, "- %s [%s/%s] %s\n", task.ID, task.Mode, task.Status, redactSecrets(task.Title))
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
				fmt.Fprintf(&b, "- %s [%s] %s\n", task.ID, task.Mode, redactSecrets(task.Title))
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
	text := strings.ToLower(continuation)
	if !containsAny(text, "validation failed", "status\":\"failed", `"status": "failed"`, "failed", "blocker", "panic", "fatal error", "regression") {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(continuation, "\n") {
		lower := strings.ToLower(line)
		if containsAny(lower, "validation", "status", "failed", "failure", "error", "blocker", "panic", "fatal", "regression") {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(redactSecrets(line)))
			b.WriteByte('\n')
		}
		if b.Len() > 4000 {
			break
		}
	}
	return b.String()
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
	data, _ := json.Marshal(state)
	var out SpecState
	_ = json.Unmarshal([]byte(redactSecrets(string(data))), &out)
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
	data, _ := json.Marshal(state)
	var out TaskState
	_ = json.Unmarshal([]byte(redactSecrets(string(data))), &out)
	return out
}

func mustCompactJSON(value any) string {
	data, err := json.MarshalIndent(security.RedactJSONValue(value), "", "  ")
	if err != nil {
		return "{}"
	}
	return redactSecrets(string(data))
}

func codexJSONPrompt(spec SpecState, task TaskRecord, proposal TaskProposalResolution) string {
	specSummary, _ := json.MarshalIndent(struct {
		Title              string   `json:"title"`
		Summary            string   `json:"summary"`
		Goals              []string `json:"goals,omitempty"`
		Requirements       []string `json:"requirements,omitempty"`
		AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	}{spec.Title, spec.Summary, spec.Goals, spec.Requirements, spec.AcceptanceCriteria}, "", "  ")
	taskJSON, _ := json.MarshalIndent(task, "", "  ")
	return redactSecrets(fmt.Sprintf(`You are running inside jj, a Go CLI that orchestrates planning, implementation, and validation.

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
- Do not rewrite .jj/spec.json, .jj/tasks.json, or .jj/runs; jj owns those state files.
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
		item = strings.TrimSpace(redactSecrets(item))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func nextTaskID(state TaskState, mode TaskProposalMode) string {
	prefix := strings.TrimSuffix(TaskProposalTaskID(mode), "-001")
	max := 0
	for _, task := range state.Tasks {
		if !strings.HasPrefix(task.ID, prefix+"-") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimPrefix(task.ID, prefix+"-"), "%d", &n); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("%s-%03d", prefix, max+1)
}

func changedFilesFromNameStatus(nameStatus string) []string {
	var out []string
	for _, line := range strings.Split(nameStatus, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		out = append(out, redactSecrets(fields[len(fields)-1]))
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
