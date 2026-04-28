package serve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jungju/jj/internal/security"
)

// StatusSummary is the sanitized dashboard-derived DTO used by `jj status`.
type StatusSummary struct {
	Task             StatusTaskSummary
	LatestRun        StatusLatestRunSummary
	NextAction       StatusNextActionSummary
	ActiveRuns       StatusActiveRunsSummary
	ValidationStatus StatusValidationStatusSummary
}

type StatusTaskSummary struct {
	State      string
	Message    string
	Available  bool
	Total      int
	Done       int
	InProgress int
	Pending    int
	Blocked    int
	Next       *StatusTaskItem
}

type StatusTaskItem struct {
	ID       string
	Category string
	Status   string
}

type StatusLatestRunSummary struct {
	State            string
	Message          string
	Available        bool
	RunID            string
	Status           string
	ProviderOrResult string
	EvaluationState  string
	TimestampLabel   string
}

type StatusNextActionSummary struct {
	State   string
	Label   string
	Message string
	RunID   string
	Task    *StatusTaskItem
}

type StatusActiveRunsSummary struct {
	State string
	Items []StatusActiveRunItem
}

type StatusActiveRunItem struct {
	RunID            string
	Status           string
	ProviderOrResult string
	EvaluationState  string
	TimestampLabel   string
}

type StatusValidationStatusSummary struct {
	State   string
	Message string
	Items   []StatusValidationStatusItem
}

type StatusValidationStatusItem struct {
	RunID           string
	ValidationState string
	CountsLabel     string
	TimestampLabel  string
}

// RecentRunsSummary is the sanitized dashboard Recent Runs DTO used by `jj runs`.
type RecentRunsSummary struct {
	State   string
	Message string
	Items   []RecentRunItem
}

type RecentRunItem struct {
	State            string
	RunID            string
	Status           string
	ProviderOrResult string
	EvaluationState  string
	ValidationState  string
	TimestampLabel   string
}

// LoadStatusSummary returns the same sanitized high-level state used by the
// dashboard root without starting an HTTP server.
func LoadStatusSummary(_ context.Context, cfg Config) (StatusSummary, error) {
	server, err := statusServerFromConfig(cfg)
	if err != nil {
		return StatusSummary{}, err
	}
	return server.StatusSummary(), nil
}

// LoadRecentRunsSummary returns the same sanitized Recent Runs data used by the
// dashboard root without starting an HTTP server.
func LoadRecentRunsSummary(_ context.Context, cfg Config) (RecentRunsSummary, error) {
	server, err := statusServerFromConfig(cfg)
	if err != nil {
		return RecentRunsSummary{}, err
	}
	return server.RecentRunsSummary(), nil
}

func statusServerFromConfig(cfg Config) (*Server, error) {
	resolved, err := ResolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	cwd := strings.TrimSpace(resolved.CWD)
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, errors.New("cwd is not allowed")
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("cwd does not exist")
		}
		return nil, errors.New("cwd is not readable")
	}
	if !info.IsDir() {
		return nil, errors.New("cwd is not a directory")
	}
	if resolvedAbs, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolvedAbs
	}
	return &Server{cwd: abs}, nil
}

// StatusSummary returns a CLI-safe summary derived from dashboard DTO helpers.
func (s *Server) StatusSummary() StatusSummary {
	taskQueue := s.taskQueueSummary()
	runs, err := s.discoverRuns()
	if err != nil {
		state := statusDiscoveryState(err)
		latest := latestRunSummary{
			State:            state,
			Message:          "Run metadata " + state + ".",
			Status:           state,
			ProviderOrResult: state,
			EvaluationState:  "unknown",
			TimestampLabel:   "unknown",
		}
		validation := validationStatusSummary{
			State:   state,
			Message: "Validation metadata " + state + ".",
		}
		return statusSummaryFromDashboardSummaries(
			taskQueue,
			latest,
			nextActionSummaryFromSummaries(taskQueue, latest),
			activeRunsSummary{State: state},
			validation,
		)
	}
	runs = s.sanitizeRunHistoryLinks(runs)
	latest := latestRunSummaryFromRuns(runs)
	return statusSummaryFromDashboardSummaries(
		taskQueue,
		latest,
		nextActionSummaryFromSummaries(taskQueue, latest),
		activeRunsSummaryFromRuns(runs),
		validationStatusSummaryFromRuns(runs),
	)
}

// RecentRunsSummary returns a CLI-safe recent-run list derived from the
// dashboard Recent Runs summary helper.
func (s *Server) RecentRunsSummary() RecentRunsSummary {
	runs, err := s.discoverRuns()
	if err != nil {
		state := statusDiscoveryState(err)
		return RecentRunsSummary{
			State:   state,
			Message: "Run metadata " + state + ".",
		}
	}
	runs = s.sanitizeRunHistoryLinks(runs)
	return recentRunsSummaryFromDashboard(recentRunsSummaryFromRuns(runs))
}

func statusDiscoveryState(err error) string {
	if errors.Is(err, security.ErrOutsideWorkspace) || errors.Is(err, security.ErrSymlinkOutside) || errors.Is(err, security.ErrSymlinkPath) {
		return "denied"
	}
	return "unavailable"
}

func statusSummaryFromDashboardSummaries(taskQueue taskQueueSummary, latest latestRunSummary, next nextActionSummary, active activeRunsSummary, validation validationStatusSummary) StatusSummary {
	return StatusSummary{
		Task:             statusTaskSummaryFromDashboard(taskQueue),
		LatestRun:        statusLatestRunSummaryFromDashboard(latest),
		NextAction:       statusNextActionSummaryFromDashboard(next),
		ActiveRuns:       statusActiveRunsSummaryFromDashboard(active),
		ValidationStatus: statusValidationStatusSummaryFromDashboard(validation),
	}
}

func statusTaskSummaryFromDashboard(summary taskQueueSummary) StatusTaskSummary {
	return StatusTaskSummary{
		State:      statusState(summary.State, "unknown"),
		Message:    statusSafeText(summary.Message, "TASK.md summary unavailable."),
		Available:  summary.Available,
		Total:      maxInt(summary.Counts.Total, 0),
		Done:       maxInt(summary.Counts.Done, 0),
		InProgress: maxInt(summary.Counts.InProgress, 0),
		Pending:    maxInt(summary.Counts.Pending, 0),
		Blocked:    maxInt(summary.Counts.Blocked, 0),
		Next:       statusTaskItemFromDashboard(summary.Next),
	}
}

func statusTaskItemFromDashboard(item *taskQueueItem) *StatusTaskItem {
	if item == nil {
		return nil
	}
	safe := sanitizeNextActionTask(*item)
	id := statusRunOrTaskID(safe.ID)
	status := sanitizeTaskStatus(safe.Status)
	if id == "" || status == "unknown" {
		return nil
	}
	return &StatusTaskItem{
		ID:       id,
		Category: statusToken(safe.Category, "unknown"),
		Status:   status,
	}
}

func statusLatestRunSummaryFromDashboard(summary latestRunSummary) StatusLatestRunSummary {
	state := statusState(summary.State, "unknown")
	if strings.Contains(dashboardCategory(summary.Message, ""), "denied") {
		state = "denied"
	}
	return StatusLatestRunSummary{
		State:            state,
		Message:          statusSafeText(summary.Message, ""),
		Available:        summary.Available,
		RunID:            statusRunOrTaskID(summary.RunID),
		Status:           statusToken(summary.Status, "unknown"),
		ProviderOrResult: statusSafeText(summary.ProviderOrResult, "unknown"),
		EvaluationState:  statusEvaluationState(summary.EvaluationState),
		TimestampLabel:   statusTimestampLabel(summary.TimestampLabel),
	}
}

func statusNextActionSummaryFromDashboard(summary nextActionSummary) StatusNextActionSummary {
	return StatusNextActionSummary{
		State:   statusToken(summary.State, "unknown"),
		Label:   statusSafeText(summary.Label, "Next Action Unknown"),
		Message: statusSafeText(summary.Message, "Next action state is unavailable."),
		RunID:   statusRunOrTaskID(summary.RunID),
		Task:    statusTaskItemFromDashboard(summary.Task),
	}
}

func statusActiveRunsSummaryFromDashboard(summary activeRunsSummary) StatusActiveRunsSummary {
	out := StatusActiveRunsSummary{State: statusState(summary.State, "none")}
	seen := map[string]bool{}
	for _, item := range summary.Items {
		runID := statusRunOrTaskID(item.RunID)
		if runID == "" || seen[runID] {
			continue
		}
		seen[runID] = true
		out.Items = append(out.Items, StatusActiveRunItem{
			RunID:            runID,
			Status:           statusToken(item.Status, "unknown"),
			ProviderOrResult: statusSafeText(item.ProviderOrResult, "unknown"),
			EvaluationState:  statusEvaluationState(item.EvaluationState),
			TimestampLabel:   statusTimestampLabel(item.TimestampLabel),
		})
	}
	if len(out.Items) > 0 {
		out.State = "available"
	}
	return out
}

func statusValidationStatusSummaryFromDashboard(summary validationStatusSummary) StatusValidationStatusSummary {
	out := StatusValidationStatusSummary{
		State:   statusState(summary.State, "none"),
		Message: statusSafeText(summary.Message, "Validation metadata unavailable."),
	}
	seen := map[string]bool{}
	for _, item := range summary.Items {
		runID := statusRunOrTaskID(item.RunID)
		if runID == "" || seen[runID] {
			continue
		}
		seen[runID] = true
		out.Items = append(out.Items, StatusValidationStatusItem{
			RunID:           runID,
			ValidationState: statusState(item.ValidationState, "unknown"),
			CountsLabel:     statusSafeText(item.CountsLabel, ""),
			TimestampLabel:  statusTimestampLabel(item.TimestampLabel),
		})
	}
	return out
}

func recentRunsSummaryFromDashboard(summary recentRunsSummary) RecentRunsSummary {
	out := RecentRunsSummary{
		State:   statusState(summary.State, "none"),
		Message: statusSafeText(summary.Message, ""),
	}
	seen := map[string]bool{}
	for _, item := range summary.Items {
		dto, ok := recentRunItemFromDashboard(item)
		if !ok || seen[dto.RunID] {
			continue
		}
		seen[dto.RunID] = true
		out.Items = append(out.Items, dto)
	}
	if len(out.Items) > 0 {
		out.State = "available"
	} else if out.State == "" {
		out.State = "none"
	}
	return out
}

func recentRunItemFromDashboard(item recentRunItem) (RecentRunItem, bool) {
	runID := latestRunIDLabel(item.RunID)
	if runID == "" {
		return RecentRunItem{}, false
	}
	dto := RecentRunItem{
		State:            statusState(item.State, "unknown"),
		RunID:            runID,
		Status:           statusToken(item.Status, "unknown"),
		ProviderOrResult: statusSafeText(item.ProviderOrResult, "unknown"),
		EvaluationState:  statusEvaluationState(item.EvaluationState),
		ValidationState:  statusState(item.ValidationState, "unknown"),
		TimestampLabel:   statusTimestampLabel(item.TimestampLabel),
	}
	return normalizeRecentRunDTO(dto), true
}

func normalizeRecentRunDTO(item RecentRunItem) RecentRunItem {
	switch item.State {
	case "denied":
		item.Status = "denied"
		item.ProviderOrResult = "denied"
		item.EvaluationState = "denied"
		item.ValidationState = "denied"
	case "unavailable":
		item.Status = "unavailable"
		item.ProviderOrResult = "unavailable"
		if item.EvaluationState == "" || item.EvaluationState == "unknown" {
			item.EvaluationState = "unavailable"
		}
		if item.ValidationState == "" || item.ValidationState == "unknown" {
			item.ValidationState = "unavailable"
		}
	case "unknown":
		item.Status = "unknown"
		item.ProviderOrResult = "unknown"
		item.EvaluationState = "unknown"
		item.ValidationState = "unknown"
	}
	switch item.Status {
	case "stale", "malformed", "partial":
		item.State = "unavailable"
		item.Status = "unavailable"
		item.ProviderOrResult = "unavailable"
		item.EvaluationState = "unavailable"
		item.ValidationState = "unavailable"
	case "inconsistent":
		item.State = "unknown"
		item.Status = "unknown"
		item.ProviderOrResult = "unknown"
		item.EvaluationState = "unknown"
		item.ValidationState = "unknown"
	}
	if item.State == "" {
		item.State = "unknown"
	}
	if item.Status == "" {
		item.Status = "unknown"
	}
	if item.ProviderOrResult == "" {
		item.ProviderOrResult = "unknown"
	}
	if item.EvaluationState == "" {
		item.EvaluationState = "unknown"
	}
	if item.ValidationState == "" {
		item.ValidationState = "unknown"
	}
	if item.TimestampLabel == "" {
		item.TimestampLabel = "unknown"
	}
	return item
}

func statusRunOrTaskID(value string) string {
	value = strings.TrimSpace(value)
	if id := latestRunIDLabel(value); id != "" {
		return id
	}
	if id := sanitizeTaskID(value); id != "" {
		return id
	}
	return ""
}

func statusState(value, fallback string) string {
	token := statusToken(value, fallback)
	switch token {
	case "available", "none", "unavailable", "unknown", "denied", "all_clear", "all-clear", "findings", "passed", "failed", "needs_work", "skipped":
		if token == "all_clear" {
			return "all-clear"
		}
		return token
	default:
		return fallback
	}
}

func statusToken(value, fallback string) string {
	token := dashboardCategory(value, "")
	if token == "" || statusUnsafeOutputText(token) {
		return fallback
	}
	return token
}

func statusEvaluationState(value string) string {
	if dashboardCategory(value, "") == "none" {
		return "none"
	}
	return evaluationStatusToken(value, "unknown")
}

func statusTimestampLabel(value string) string {
	value = statusSafeText(value, "")
	switch value {
	case "", "unknown":
		return "unknown"
	case "none":
		return "none"
	}
	if parsed, ok := parseLatestRunTimestamp(value); ok {
		return parsed.UTC().Format(time.RFC3339)
	}
	return "unknown"
}

func statusSafeText(value, fallback string) string {
	text := strings.TrimSpace(sanitizeRunDetailText(value))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" || statusUnsafeOutputText(text) {
		return fallback
	}
	return text
}

func statusUnsafeOutputText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if unsafeRunDetailText(text) {
		return true
	}
	redacted := security.RedactString(text)
	if redacted != text || strings.Contains(redacted, security.RedactionMarker) {
		return true
	}
	lower := strings.ToLower(text)
	for _, token := range []string{
		security.RedactionMarker,
		"sensitive value removed",
		"unsafe value removed",
		"[redacted]",
		"[omitted]",
		"[hidden]",
		"[removed]",
		"{redacted}",
		"{omitted}",
		"{hidden}",
		"{removed}",
		"<redacted>",
		"<omitted>",
		"<hidden>",
		"<removed>",
		"[path]",
	} {
		if strings.Contains(lower, strings.ToLower(token)) {
			return true
		}
	}
	return false
}
