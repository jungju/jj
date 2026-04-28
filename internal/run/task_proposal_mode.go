package run

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jungju/jj/internal/security"
)

// TaskProposalMode controls the direction jj asks planning to prefer when
// proposing the next implementation task.
type TaskProposalMode string

const (
	TaskProposalModeAuto      TaskProposalMode = "auto"
	TaskProposalModeBalanced  TaskProposalMode = "balanced"
	TaskProposalModeFeature   TaskProposalMode = "feature"
	TaskProposalModeSecurity  TaskProposalMode = "security"
	TaskProposalModeHardening TaskProposalMode = "hardening"
	TaskProposalModeQuality   TaskProposalMode = "quality"
	TaskProposalModeBugfix    TaskProposalMode = "bugfix"
	TaskProposalModeDocs      TaskProposalMode = "docs"
)

var validTaskProposalModes = []TaskProposalMode{
	TaskProposalModeAuto,
	TaskProposalModeBalanced,
	TaskProposalModeFeature,
	TaskProposalModeSecurity,
	TaskProposalModeHardening,
	TaskProposalModeQuality,
	TaskProposalModeBugfix,
	TaskProposalModeDocs,
}

// TaskProposalResolution records the user-selected mode and the concrete mode
// used for this run after applying automatic or blocker-based resolution.
type TaskProposalResolution struct {
	Selected       TaskProposalMode
	Resolved       TaskProposalMode
	Reason         string
	SelectedTaskID string
}

// ValidTaskProposalModes returns the supported modes in CLI display order.
func ValidTaskProposalModes() []TaskProposalMode {
	modes := make([]TaskProposalMode, len(validTaskProposalModes))
	copy(modes, validTaskProposalModes)
	return modes
}

// ValidTaskProposalModeValues returns the supported modes as strings.
func ValidTaskProposalModeValues() []string {
	modes := ValidTaskProposalModes()
	values := make([]string, 0, len(modes))
	for _, mode := range modes {
		values = append(values, string(mode))
	}
	return values
}

// ValidTaskProposalModesString returns the supported modes for error messages.
func ValidTaskProposalModesString() string {
	return strings.Join(ValidTaskProposalModeValues(), ", ")
}

// ParseTaskProposalMode parses and validates a mode string.
func ParseTaskProposalMode(raw string) (TaskProposalMode, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return TaskProposalModeAuto, nil
	}
	mode := TaskProposalMode(trimmed)
	if mode.Valid() {
		return mode, nil
	}
	return "", fmt.Errorf("invalid task proposal mode: %q\nvalid modes: %s", raw, ValidTaskProposalModesString())
}

// Valid reports whether mode is supported.
func (m TaskProposalMode) Valid() bool {
	for _, valid := range validTaskProposalModes {
		if m == valid {
			return true
		}
	}
	return false
}

// Description returns a short user-facing description of the mode.
func (m TaskProposalMode) Description() string {
	switch m {
	case TaskProposalModeAuto:
		return "jj chooses the best direction from current evidence."
	case TaskProposalModeBalanced:
		return "jj balances product progress, security, quality, hardening, and documentation."
	case TaskProposalModeFeature:
		return "Propose user-facing product capabilities."
	case TaskProposalModeSecurity:
		return "Propose security and privacy improvements."
	case TaskProposalModeHardening:
		return "Propose reliability, recoverability, state, or architecture improvements."
	case TaskProposalModeQuality:
		return "Propose validation, test, or regression-detection improvements."
	case TaskProposalModeBugfix:
		return "Propose fixes for failures, regressions, and blockers."
	case TaskProposalModeDocs:
		return "Propose documentation and specification alignment work."
	default:
		return ""
	}
}

// PromptInstruction returns mode-specific instructions for planning prompts.
func (m TaskProposalMode) PromptInstruction() string {
	switch m {
	case TaskProposalModeAuto:
		return "Choose the best concrete next-task category from the available evidence and explain why. Prioritize bugfix when tests, validation, or blockers fail; security when secret, file access, command execution, artifact, or dashboard exposure risk exists; hardening when run, turn, manifest, provider, artifact, or recovery structure is weak; quality when validation, tests, or regression detection is weak; docs when README, .jj/spec.json, .jj/tasks.json, or behavior are inconsistent; otherwise choose feature."
	case TaskProposalModeBalanced:
		return "Keep product progress, security, quality, hardening, and documentation balanced. Avoid repeatedly choosing one direction when security, quality, hardening, or documentation debt is visible. Consider recent turn history when available; if history is unavailable and blockers are clear, prefer useful product progress."
	case TaskProposalModeFeature:
		return "Propose the next task that adds the most useful user-facing capability. Avoid pure refactors unless they are required to deliver the feature. The task must be small enough for one implementation turn."
	case TaskProposalModeSecurity:
		return "When recommending the next task, prioritize reducing security or privacy risk. Consider secret redaction, workspace boundaries, symlink escape prevention, command execution safety, artifact safety, prompt/log redaction, and dashboard exposure. Do not recommend unrelated user-facing features unless they are necessary to mitigate the risk."
	case TaskProposalModeHardening:
		return "Propose the next task that improves reliability, recoverability, state consistency, or architecture. Prioritize provider separation, run/turn state, manifest schema, event logging, atomic artifacts, crash recovery, resume support, git evidence collection, and deterministic provider behavior. Avoid broad new user-facing features."
	case TaskProposalModeQuality:
		return "Propose the next task that improves validation, tests, or regression detection. Prioritize deterministic tests, injected or fake providers, validation reliability, provider fallback tests, git evidence tests, and redaction tests. Default validation must not require live OpenAI API access, real Codex CLI execution, or network access."
	case TaskProposalModeBugfix:
		return "Propose the next task that fixes the most important known failure, regression, broken test, or blocker. Use evidence from validation results, test logs, git evidence, manifest, event log, dashboard errors, and provider failures. Do not propose new features until the blocker is resolved."
	case TaskProposalModeDocs:
		return "Propose the next task that improves alignment between documentation, JSON state, and actual behavior. Prioritize README alignment, .jj/spec.json alignment, .jj/tasks.json cleanup, canonical JSON templates, provider model documentation, configuration documentation, and acceptance criteria updates."
	default:
		return ""
	}
}

// ResolveTaskProposalMode resolves automatic or balanced modes into a concrete
// mode and applies critical-blocker overrides for concrete selections.
func ResolveTaskProposalMode(selected TaskProposalMode, evidence string) TaskProposalResolution {
	if !selected.Valid() {
		selected = TaskProposalModeAuto
	}
	detected, detectedReason, critical := detectTaskProposalMode(evidence)
	resolved := selected
	reason := ""

	switch selected {
	case TaskProposalModeAuto:
		resolved = detected
		reason = "auto selected " + detectedReason
	case TaskProposalModeBalanced:
		resolved = detected
		if detected == TaskProposalModeFeature {
			reason = "balanced found no blocker or high debt signal, so it can continue product progress while watching security, quality, hardening, and documentation."
		} else {
			reason = "balanced selected " + detectedReason
		}
	default:
		if critical && detected == TaskProposalModeBugfix {
			resolved = TaskProposalModeBugfix
			reason = fmt.Sprintf("%s was overridden because %s", selected, detectedReason)
		} else {
			resolved = selected
			reason = fmt.Sprintf("selected concrete mode %s remains active because no validation, test, provider, blocker, panic, fatal error, or regression evidence was detected.", selected)
		}
	}

	return TaskProposalResolution{
		Selected:       selected,
		Resolved:       resolved,
		Reason:         reason,
		SelectedTaskID: TaskProposalTaskID(resolved),
	}
}

// TaskProposalTaskID returns the default global task id for generated task
// scaffolding and events. Category metadata lives in TaskRecord.Mode.
func TaskProposalTaskID(mode TaskProposalMode) string {
	return "TASK-0001"
}

// TaskProposalTaskTitle returns a concise title for generated task scaffolding.
func TaskProposalTaskTitle(mode TaskProposalMode) string {
	switch concreteTaskProposalMode(mode) {
	case TaskProposalModeSecurity:
		return "Reduce the highest security or privacy risk"
	case TaskProposalModeHardening:
		return "Improve run reliability and state consistency"
	case TaskProposalModeQuality:
		return "Improve validation quality"
	case TaskProposalModeBugfix:
		return "Fix the highest-priority failure or blocker"
	case TaskProposalModeDocs:
		return "Align documentation with current behavior"
	default:
		return "Add the next useful user-facing capability"
	}
}

// TaskProposalPromptContext formats the mode context passed to providers.
func TaskProposalPromptContext(resolution TaskProposalResolution, nextIntent ...string) string {
	selected := resolution.Selected
	resolved := resolution.Resolved
	if !selected.Valid() {
		selected = TaskProposalModeAuto
	}
	if !resolved.Valid() {
		resolved = concreteTaskProposalMode(selected)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Task Proposal Mode: %s\n", selected)
	fmt.Fprintf(&b, "Resolved Mode: %s\n", resolved)
	if strings.TrimSpace(resolution.Reason) != "" {
		fmt.Fprintf(&b, "Resolution Reason: %s\n", security.SanitizeHandoffString(resolution.Reason))
	}
	fmt.Fprintf(&b, "Recommended Task ID Format: %s\n", TaskProposalTaskID(resolved))
	fmt.Fprintf(&b, "Instruction: %s", selected.PromptInstruction())
	if selected != resolved {
		fmt.Fprintf(&b, "\nConcrete Direction: %s", resolved.PromptInstruction())
	}
	if len(nextIntent) > 0 && strings.TrimSpace(nextIntent[0]) != "" {
		fmt.Fprintf(&b, "\nNext Intent Override: .jj/next-intent.md is active. The first proposed runnable task must be scoped to that free-form intent. Ignore task-proposal-mode, resolved mode, and auto/balanced detection when choosing the task. Use mode only after satisfying the intent as inferred category metadata or fallback guidance.")
	}
	return b.String()
}

func concreteTaskProposalMode(mode TaskProposalMode) TaskProposalMode {
	switch mode {
	case TaskProposalModeSecurity, TaskProposalModeHardening, TaskProposalModeQuality, TaskProposalModeBugfix, TaskProposalModeDocs, TaskProposalModeFeature:
		return mode
	default:
		return TaskProposalModeFeature
	}
}

func detectTaskProposalMode(evidence string) (TaskProposalMode, string, bool) {
	text := strings.ToLower(evidence)
	if hasPositiveBugfixEvidence(taskProposalBugfixEvidenceScope(evidence)) {
		return TaskProposalModeBugfix, "bugfix is required because failing validation, tests, regressions, or blockers are present.", true
	}
	if containsAny(text, "secret", "api key", "bearer token", "private key", "password", "credential", "connection string", "workspace boundary", "path traversal", "symlink escape", "command execution", "artifact exposure", "dashboard exposure", "security risk", "privacy risk", "redaction") {
		return TaskProposalModeSecurity, "security is required because secret, workspace, command, artifact, dashboard, or privacy risk is present.", true
	}
	if containsAny(text, "manifest", "event log", "state machine", "crash recovery", "resume", "atomic artifact", "artifact writer", "provider interface", "turn state", "run state") {
		return TaskProposalModeHardening, "hardening is appropriate because run state, provider, manifest, artifact, or recovery structure needs work.", false
	}
	if containsAny(text, "coverage", "validation", "deterministic test", "fake provider", "injected provider", "regression detection", "test coverage") {
		return TaskProposalModeQuality, "quality is appropriate because validation, tests, or regression detection needs work.", false
	}
	if containsAny(text, "readme", "documentation", "docs alignment", "document alignment", "spec alignment", "task state cleanup", "task queue cleanup", "canonical json", "canonical document", "acceptance criteria update") {
		return TaskProposalModeDocs, "docs is appropriate because documentation or canonical project documents need alignment.", false
	}
	return TaskProposalModeFeature, "feature is appropriate because no blocker, security, hardening, quality, or documentation debt signal was detected.", false
}

var failedCountEvidencePattern = regexp.MustCompile(`(?i)"?failed[_ -]?count"?\s*[:=]\s*"?([0-9]+)"?`)

func taskProposalBugfixEvidenceScope(evidence string) string {
	if !strings.Contains(evidence, "Current SPEC requirements and open questions:") &&
		!strings.Contains(evidence, "Non-terminal task state:") {
		return evidence
	}
	sections := []string{
		sectionBetween(evidence, "Non-terminal task state:", "Closed task history count:", "Recent failure evidence:"),
		sectionBetween(evidence, "Recent failure evidence:"),
	}
	return strings.Join(nonEmptyPlanningItems(sections), "\n")
}

func hasPositiveBugfixEvidence(evidence string) bool {
	return len(positiveBugfixEvidenceCategories(evidence)) > 0
}

func positiveBugfixEvidenceCategories(evidence string) []string {
	text := strings.ToLower(evidence)
	found := map[string]bool{}
	for _, match := range failedCountEvidencePattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		count, err := strconv.Atoi(match[1])
		if err == nil && count > 0 {
			found["failed_count_positive"] = true
		}
	}
	for _, line := range evidenceLines(text) {
		for _, category := range positiveBugfixLineCategories(line) {
			found[category] = true
		}
	}
	order := []string{
		"validation_failed",
		"tests_failed",
		"failed_count_positive",
		"failed_status",
		"blocked_task",
		"provider_failure",
		"panic",
		"fatal_error",
		"regression",
	}
	var categories []string
	for _, category := range order {
		if found[category] {
			categories = append(categories, category)
		}
	}
	return categories
}

func positiveBugfixLineCategories(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	found := map[string]bool{}
	if lineHasFailedOrBlockedStatus(line) {
		if containsAny(line, "blocked") {
			found["blocked_task"] = true
		} else {
			found["failed_status"] = true
		}
	}
	if lineDeclaresNoBugfixEvidence(line) {
		return orderedBugfixCategories(found)
	}
	if containsAny(line, "validation_failed") ||
		containsPositivePhrase(line, "validation failed", "validation failure", "failed validation") {
		found["validation_failed"] = true
	}
	if containsAny(line, "tests_failed") ||
		containsPositivePhrase(line, "tests failed", "test failed", "tests fail", "test fail", "failing test", "failed tests", "failed test") {
		found["tests_failed"] = true
	}
	if containsAny(line, "failed_count_positive") {
		found["failed_count_positive"] = true
	}
	if containsAny(line, "provider_failure") ||
		containsPositivePhrase(line, "provider failure", "provider failed", "provider error", "planner failed", "openai failed", "codex failed") {
		found["provider_failure"] = true
	}
	if containsPositivePhrase(line, "panic") {
		found["panic"] = true
	}
	if containsAny(line, "fatal_error") || containsPositivePhrase(line, "fatal error", "fatal failure") {
		found["fatal_error"] = true
	}
	if hasPositiveRegressionEvidence(line) {
		found["regression"] = true
	}
	if containsAny(line, "blocked_task") ||
		containsPositivePhrase(line,
			"current blocker",
			"active blocker",
			"known blocker",
			"open blocker",
			"blocker prevents",
			"blocker present",
			"blocking progress",
			"blocks progress",
			"blocks feature work",
			"blocked runnable task",
			"runnable task blocked",
			"task is blocked",
			"blocked by",
		) {
		found["blocked_task"] = true
	}
	return orderedBugfixCategories(found)
}

func orderedBugfixCategories(found map[string]bool) []string {
	order := []string{"validation_failed", "tests_failed", "failed_count_positive", "failed_status", "blocked_task", "provider_failure", "panic", "fatal_error", "regression"}
	var categories []string
	for _, category := range order {
		if found[category] {
			categories = append(categories, category)
		}
	}
	return categories
}

func lineDeclaresNoBugfixEvidence(line string) bool {
	if !strings.Contains(line, "no ") || !strings.Contains(line, "evidence") {
		return false
	}
	if containsAny(line, "evidence was detected", "evidence detected", "evidence exists", "evidence present") &&
		containsAny(line, "validation", "test", "provider", "blocker", "blocked", "panic", "fatal", "regression") {
		return true
	}
	return false
}

func lineHasFailedOrBlockedStatus(line string) bool {
	for _, status := range []string{"failed", "blocked", "partial_failed", "hard_failed"} {
		for _, token := range []string{
			`"status":"` + status + `"`,
			`"status": "` + status + `"`,
			`status:` + status,
			`status: ` + status,
			`status=` + status,
		} {
			if strings.Contains(line, token) && !strings.Contains(line, `\"status\":\"`+status+`\"`) {
				return true
			}
		}
	}
	return false
}

func hasPositiveRegressionEvidence(line string) bool {
	if containsPositivePhrase(line,
		"regression found",
		"regression detected",
		"regression introduced",
		"regression occurred",
		"regression failure",
		"regression failed",
		"regressed",
		"regresses",
	) {
		return true
	}
	if containsAny(line, "regression detection", "regression guard", "regression test", "regression coverage", "regression suite", "regression check") {
		return false
	}
	return containsPositivePhrase(line, "regression")
}

func containsPositivePhrase(line string, phrases ...string) bool {
	for _, phrase := range phrases {
		start := 0
		for {
			idx := strings.Index(line[start:], phrase)
			if idx < 0 {
				break
			}
			pos := start + idx
			if !hasRecentNegation(line, pos) {
				return true
			}
			start = pos + len(phrase)
		}
	}
	return false
}

func hasRecentNegation(line string, pos int) bool {
	start := pos - 80
	if start < 0 {
		start = 0
	}
	words := strings.FieldsFunc(line[start:pos], func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_'
	})
	if len(words) == 0 {
		return false
	}
	first := len(words) - 8
	if first < 0 {
		first = 0
	}
	for _, word := range words[first:] {
		switch word {
		case "no", "not", "without", "never", "none", "zero":
			return true
		}
	}
	return false
}

func evidenceLines(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '\n', '\r', '\t', ';':
			return true
		default:
			return false
		}
	})
	var lines []string
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func sectionBetween(text, startMarker string, endMarkers ...string) string {
	lower := strings.ToLower(text)
	startMarker = strings.ToLower(startMarker)
	start := strings.Index(lower, startMarker)
	if start < 0 {
		return ""
	}
	contentStart := start + len(startMarker)
	end := len(text)
	for _, marker := range endMarkers {
		marker = strings.ToLower(marker)
		if marker == "" {
			continue
		}
		if idx := strings.Index(lower[contentStart:], marker); idx >= 0 && contentStart+idx < end {
			end = contentStart + idx
		}
	}
	return text[contentStart:end]
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
