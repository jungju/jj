package serve

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jungju/jj/internal/security"
)

const workspaceTaskMarkdownPath = "docs/TASK.md"

var taskMarkdownIDPattern = regexp.MustCompile(`\bTASK-\d{1,6}\b`)

type taskQueueSummary struct {
	State     string
	Message   string
	Available bool
	Counts    taskQueueCounts
	Next      *taskQueueItem
}

type taskQueueCounts struct {
	Done       int
	InProgress int
	Pending    int
	Blocked    int
	Total      int
}

type taskQueueItem struct {
	ID       string
	Category string
	Status   string
	Title    string
}

func (s *Server) taskQueueSummary() taskQueueSummary {
	path, err := safeJoinProject(s.cwd, workspaceTaskMarkdownPath)
	if err != nil {
		return unavailableTaskQueueSummary("denied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return unavailableTaskQueueSummary("unavailable")
	}
	return parseTaskQueueSummary(string(data), security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace})
}

func unavailableTaskQueueSummary(state string) taskQueueSummary {
	state = taskQueueState(state, "unavailable")
	return taskQueueSummary{
		State:   state,
		Message: "TASK.md unavailable.",
	}
}

func unknownTaskQueueSummary() taskQueueSummary {
	return taskQueueSummary{
		State:   "unknown",
		Message: "TASK.md task summary unknown.",
	}
}

func parseTaskQueueSummary(markdown string, roots ...security.CommandPathRoot) taskQueueSummary {
	if strings.TrimSpace(markdown) == "" {
		return unknownTaskQueueSummary()
	}
	tasks := parseTaskMarkdownItems(markdown, roots...)
	if len(tasks) == 0 {
		return unknownTaskQueueSummary()
	}
	summary := taskQueueSummary{
		State:     "available",
		Available: true,
	}
	for i := range tasks {
		task := tasks[i]
		summary.Counts.Total++
		switch task.Status {
		case "done":
			summary.Counts.Done++
		case "in-progress":
			summary.Counts.InProgress++
		case "pending":
			summary.Counts.Pending++
		case "blocked":
			summary.Counts.Blocked++
		}
		if summary.Next == nil && taskActionable(task.Status) {
			next := task
			summary.Next = &next
		}
	}
	summary.Message = fmt.Sprintf(
		"TASK.md: %d total, %d done, %d in progress, %d pending, %d blocked.",
		summary.Counts.Total,
		summary.Counts.Done,
		summary.Counts.InProgress,
		summary.Counts.Pending,
		summary.Counts.Blocked,
	)
	if summary.Next == nil {
		if summary.Counts.Total > 0 && summary.Counts.Done == summary.Counts.Total {
			summary.Message += " All TASK.md tasks are done. No runnable tasks."
		} else {
			summary.Message += " No pending or in-progress tasks."
		}
	}
	return summary
}

func parseTaskMarkdownItems(markdown string, roots ...security.CommandPathRoot) []taskQueueItem {
	var tasks []taskQueueItem
	current := -1
	currentLevel := 0
	sectionStatus := ""
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if level, heading, ok := markdownHeading(trimmed); ok {
			if task, ok := parseTaskMarkdownRecord(heading, sectionStatus, "", roots...); ok {
				tasks = append(tasks, task)
				current = len(tasks) - 1
				currentLevel = level
				continue
			}
			sectionStatus = taskStatusFromHeading(heading)
			if current >= 0 && level <= currentLevel {
				current = -1
				currentLevel = 0
			}
			continue
		}
		if current >= 0 {
			if field, value, ok := taskMarkdownField(trimmed); ok {
				handled := true
				switch strings.ToLower(field) {
				case "status", "state":
					if status := sanitizeTaskStatus(value); status != "unknown" {
						tasks[current].Status = status
					}
				case "mode", "category", "type":
					tasks[current].Category = sanitizeTaskCategory(value)
				case "title", "summary":
					tasks[current].Title = sanitizeTaskTitle(value, roots...)
				default:
					handled = false
				}
				if handled {
					continue
				}
			}
		}
		if item, checkboxStatus, ok := markdownTaskListItem(trimmed); ok {
			if task, ok := parseTaskMarkdownRecord(item, sectionStatus, checkboxStatus, roots...); ok {
				tasks = append(tasks, task)
				current = len(tasks) - 1
				currentLevel = 0
			}
		}
	}
	return tasks
}

func markdownHeading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	heading := strings.TrimSpace(line[level:])
	heading = strings.Trim(heading, "# ")
	if heading == "" {
		return 0, "", false
	}
	return level, heading, true
}

func markdownTaskListItem(line string) (string, string, bool) {
	item := ""
	for _, prefix := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, prefix) {
			item = strings.TrimSpace(strings.TrimPrefix(line, prefix))
			break
		}
	}
	if item == "" {
		for i, r := range line {
			if r < '0' || r > '9' {
				if i > 0 && (strings.HasPrefix(line[i:], ". ") || strings.HasPrefix(line[i:], ") ")) {
					item = strings.TrimSpace(line[i+2:])
				}
				break
			}
		}
	}
	if item == "" {
		return "", "", false
	}
	checkboxStatus := ""
	lower := strings.ToLower(item)
	switch {
	case strings.HasPrefix(lower, "[x] "):
		checkboxStatus = "done"
		item = strings.TrimSpace(item[3:])
	case strings.HasPrefix(lower, "[ ] "):
		checkboxStatus = "pending"
		item = strings.TrimSpace(item[3:])
	case strings.HasPrefix(lower, "[-] "), strings.HasPrefix(lower, "[!] "):
		checkboxStatus = "blocked"
		item = strings.TrimSpace(item[3:])
	case strings.HasPrefix(lower, "[~] "):
		checkboxStatus = "in-progress"
		item = strings.TrimSpace(item[3:])
	}
	if item == "" {
		return "", "", false
	}
	return item, checkboxStatus, true
}

func parseTaskMarkdownRecord(raw, sectionStatus, explicitStatus string, roots ...security.CommandPathRoot) (taskQueueItem, bool) {
	loc := taskMarkdownIDPattern.FindStringIndex(raw)
	if loc == nil || !safeTaskIDPrefix(raw[:loc[0]]) {
		return taskQueueItem{}, false
	}
	id := sanitizeTaskID(raw[loc[0]:loc[1]])
	if id == "" {
		return taskQueueItem{}, false
	}
	rest := strings.TrimSpace(raw[loc[1]:])
	category := ""
	status := sanitizeTaskStatus(explicitStatus)
	allowStatusOverride := status == "unknown"
	if status == "unknown" {
		status = sanitizeTaskStatus(sectionStatus)
	}
	rest, category, status = consumeTaskInlineMetadata(rest, category, status, allowStatusOverride)
	title := sanitizeTaskTitle(cleanTaskTitle(rest), roots...)
	if title == "" {
		title = "Untitled task"
	}
	if category == "" {
		category = "unknown"
	}
	return taskQueueItem{
		ID:       id,
		Category: category,
		Status:   status,
		Title:    title,
	}, true
}

func consumeTaskInlineMetadata(rest, category, status string, allowStatusOverride bool) (string, string, string) {
	rest = strings.TrimSpace(rest)
	for {
		if rest == "" {
			return rest, category, status
		}
		open, close := byte(0), byte(0)
		switch rest[0] {
		case '[':
			open, close = '[', ']'
		case '(':
			open, close = '(', ')'
		default:
			return rest, category, status
		}
		end := strings.IndexByte(rest, close)
		if end <= 0 {
			return rest, category, status
		}
		inside := rest[1:end]
		nextCategory, nextStatus := taskInlineMetadata(inside)
		if nextCategory == "" && nextStatus == "" {
			return rest, category, status
		}
		if category == "" {
			category = nextCategory
		}
		if (status == "unknown" || allowStatusOverride) && nextStatus != "" {
			status = nextStatus
		}
		rest = strings.TrimSpace(rest[end+1:])
		if open == '(' {
			rest = strings.TrimLeft(rest, ": -")
		}
	}
}

func taskInlineMetadata(raw string) (string, string) {
	raw = strings.NewReplacer("/", ",", "|", ",", ";", ",").Replace(raw)
	var category, status string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if field, value, ok := splitTaskMarkdownField(part); ok {
			switch strings.ToLower(field) {
			case "status", "state":
				if parsed := sanitizeTaskStatus(value); parsed != "unknown" {
					status = parsed
				}
			case "mode", "category", "type":
				category = sanitizeTaskCategory(value)
			}
			continue
		}
		if parsed := sanitizeTaskStatus(part); parsed != "unknown" {
			status = parsed
			continue
		}
		if category == "" {
			category = sanitizeTaskCategory(part)
		}
	}
	return category, status
}

func taskMarkdownField(line string) (string, string, bool) {
	if item, _, ok := markdownTaskListItem(line); ok {
		line = item
	}
	return splitTaskMarkdownField(line)
}

func splitTaskMarkdownField(line string) (string, string, bool) {
	before, after, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	field := strings.TrimSpace(strings.Trim(before, "*`"))
	value := strings.TrimSpace(after)
	if field == "" || value == "" {
		return "", "", false
	}
	return field, value, true
}

func cleanTaskTitle(title string) string {
	title = strings.TrimSpace(strings.TrimLeft(title, ": -"))
	title = strings.TrimSpace(strings.Trim(title, "*`"))
	return title
}

func safeTaskIDPrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return true
	}
	prefix = strings.Trim(prefix, "*`[]() ")
	return prefix == ""
}

func sanitizeTaskID(value string) string {
	return taskMarkdownIDPattern.FindString(sanitizeRunDetailText(value))
}

func sanitizeTaskTitle(value string, roots ...security.CommandPathRoot) string {
	value = strings.TrimSpace(sanitizeRunDetailText(value, roots...))
	if strings.Contains(value, security.RedactionMarker) {
		return "sensitive value removed"
	}
	return value
}

func sanitizeTaskCategory(value string) string {
	return dashboardCategory(value, "unknown")
}

func sanitizeTaskStatus(value string) string {
	status := dashboardCategory(value, "unknown")
	switch status {
	case "queued", "queue", "pending", "todo", "to_do", "open", "ready", "backlog", "planned", "proposed", "not_started":
		return "pending"
	case "active", "current", "started", "running", "in_progress", "inprogress", "progress", "doing":
		return "in-progress"
	case "done", "complete", "completed", "closed", "resolved", "passed":
		return "done"
	case "blocked", "blocker", "failed", "failure", "stalled":
		return "blocked"
	default:
		return "unknown"
	}
}

func taskStatusFromHeading(heading string) string {
	status := sanitizeTaskStatus(heading)
	if status != "unknown" {
		return status
	}
	normalized := dashboardCategory(heading, "")
	switch normalized {
	case "current_task", "current_tasks", "in_progress_tasks", "active_tasks", "now":
		return "in-progress"
	case "pending_tasks", "queued_tasks", "task_queue", "todo_tasks", "backlog_tasks", "runnable_tasks":
		return "pending"
	case "done_tasks", "completed_tasks", "closed_tasks":
		return "done"
	case "blocked_tasks":
		return "blocked"
	default:
		return ""
	}
}

func taskActionable(status string) bool {
	return status == "pending" || status == "in-progress"
}

func taskQueueState(value, fallback string) string {
	switch value {
	case "available", "unavailable", "unknown", "denied":
		return value
	default:
		return fallback
	}
}
