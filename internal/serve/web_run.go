package serve

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/secrets"
)

const (
	maxWebRunLogLines = 400
	maxWebRunLogBytes = 64 * 1024
)

var dashboardAbsolutePathPattern = regexp.MustCompile(`(^|[\s="'(])(/[^\s"'<>),}]+)`)

type webRunRegistry struct {
	mu   sync.Mutex
	runs map[string]*webRunState
}

type webRunState struct {
	mu              sync.Mutex
	loopID          string
	status          string
	phase           string
	startedAt       string
	finishedAt      string
	err             string
	logs            []string
	logBytes        int
	turns           []webRunTurnState
	currentTurn     int
	autoContinue    bool
	maxTurns        int
	finishRequested bool
	stopReason      string
}

type webRunTurnState struct {
	Number     int
	RunID      string
	Status     string
	Phase      string
	StartedAt  string
	FinishedAt string
	RunDir     string
	Error      string
}

type webRunTurnView struct {
	Number      int    `json:"number"`
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	Phase       string `json:"phase"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at,omitempty"`
	RunDir      string `json:"run_dir"`
	Error       string `json:"error,omitempty"`
	ArtifactURL string `json:"artifact_url"`
	Done        bool   `json:"done"`
}

type webRunView struct {
	RunID           string           `json:"run_id"`
	Status          string           `json:"status"`
	Phase           string           `json:"phase"`
	StartedAt       string           `json:"started_at"`
	FinishedAt      string           `json:"finished_at,omitempty"`
	RunDir          string           `json:"run_dir"`
	Logs            []string         `json:"logs"`
	Error           string           `json:"error,omitempty"`
	ArtifactURL     string           `json:"artifact_url"`
	Done            bool             `json:"done"`
	Turns           []webRunTurnView `json:"turns"`
	CurrentTurn     webRunTurnView   `json:"current_turn"`
	AutoContinue    bool             `json:"auto_continue"`
	MaxTurns        int              `json:"max_turns"`
	FinishRequested bool             `json:"finish_requested"`
	StopReason      string           `json:"stop_reason,omitempty"`
}

type webRunWriter struct {
	run *webRunState
}

func newWebRunRegistry() *webRunRegistry {
	return &webRunRegistry{runs: map[string]*webRunState{}}
}

func (r *webRunRegistry) create(loopID string, autoContinue bool, maxTurns int) *webRunState {
	run := &webRunState{
		loopID:       loopID,
		status:       "queued",
		phase:        "queued",
		startedAt:    time.Now().UTC().Format(time.RFC3339),
		autoContinue: autoContinue,
		maxTurns:     maxTurns,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[loopID] = run
	return run
}

func (r *webRunRegistry) get(loopID string) *webRunState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs[loopID]
}

func (r *webRunRegistry) isActive(loopID string) bool {
	run := r.get(loopID)
	if run == nil {
		return false
	}
	return !run.view().Done
}

func (r *webRunRegistry) activeViews() []webRunView {
	r.mu.Lock()
	runs := make([]*webRunState, 0, len(r.runs))
	for _, run := range r.runs {
		runs = append(runs, run)
	}
	r.mu.Unlock()

	views := make([]webRunView, 0, len(runs))
	for _, run := range runs {
		view := run.view()
		if !view.Done {
			views = append(views, view)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].StartedAt > views[j].StartedAt })
	return views
}

func (r *webRunState) writer() io.Writer {
	return webRunWriter{run: r}
}

func (w webRunWriter) Write(p []byte) (int, error) {
	w.run.appendLog(string(p))
	return len(p), nil
}

func (r *webRunState) beginTurn(number int, runID, runDir string) {
	now := time.Now().UTC().Format(time.RFC3339)
	r.mu.Lock()
	r.turns = append(r.turns, webRunTurnState{
		Number:    number,
		RunID:     runID,
		RunDir:    runDir,
		Status:    "queued",
		Phase:     "queued",
		StartedAt: now,
	})
	r.currentTurn = len(r.turns) - 1
	r.status = "running"
	r.phase = "turn_queued"
	r.mu.Unlock()
	r.appendLog("jj web: queued turn " + runID)
}

func (r *webRunState) setCurrentTurnRunDir(runDir string) {
	if strings.TrimSpace(runDir) == "" {
		return
	}
	r.mu.Lock()
	if r.currentTurn >= 0 && r.currentTurn < len(r.turns) {
		r.turns[r.currentTurn].RunDir = runDir
	}
	r.mu.Unlock()
	r.persistLog()
}

func (r *webRunState) setCurrentTurnStatus(status, phase, errText string) {
	now := time.Now().UTC().Format(time.RFC3339)
	r.mu.Lock()
	if r.currentTurn >= 0 && r.currentTurn < len(r.turns) {
		turn := &r.turns[r.currentTurn]
		turn.Status = status
		if strings.TrimSpace(phase) != "" {
			turn.Phase = phase
		}
		if errText != "" {
			turn.Error = redactDashboardLogPaths(secrets.Redact(errText))
		}
		if webRunDone(status) {
			turn.FinishedAt = now
		}
	}
	if !webRunDone(status) {
		r.status = status
	}
	if strings.TrimSpace(phase) != "" {
		r.phase = phase
	}
	if errText != "" {
		r.err = redactDashboardLogPaths(secrets.Redact(errText))
	}
	r.mu.Unlock()
	r.persistLog()
}

func (r *webRunState) setLoopStatus(status, phase, errText, stopReason string) {
	if webRunDone(status) {
		r.persistLog()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	r.mu.Lock()
	r.status = status
	if strings.TrimSpace(phase) != "" {
		r.phase = phase
	}
	if errText != "" {
		r.err = redactDashboardLogPaths(secrets.Redact(errText))
	}
	if stopReason != "" {
		r.stopReason = redactDashboardLogPaths(secrets.Redact(stopReason))
	}
	if webRunDone(status) {
		r.finishedAt = now
	}
	r.mu.Unlock()
	if !webRunDone(status) {
		r.persistLog()
	}
}

func (r *webRunState) requestFinish() {
	r.mu.Lock()
	r.finishRequested = true
	r.stopReason = "finish requested"
	r.mu.Unlock()
	r.appendLog("jj web: finish requested; current turn will complete before stopping")
}

func (r *webRunState) finishWasRequested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finishRequested
}

func (r *webRunState) appendLog(text string) {
	text = secrets.Redact(text)
	text = redactDashboardLogPaths(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	r.mu.Lock()
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		r.logs = append(r.logs, line)
		r.logBytes += len(line) + 1
		if phase := phaseFromLog(line); phase != "" && !webRunDone(r.status) {
			r.phase = phase
			if r.currentTurn >= 0 && r.currentTurn < len(r.turns) {
				r.turns[r.currentTurn].Phase = phase
			}
		}
		for len(r.logs) > maxWebRunLogLines || (r.logBytes > maxWebRunLogBytes && len(r.logs) > 1) {
			r.logBytes -= len(r.logs[0]) + 1
			r.logs = r.logs[1:]
		}
	}
	r.mu.Unlock()
	r.persistLog()
}

func (r *webRunState) view() webRunView {
	r.mu.Lock()
	defer r.mu.Unlock()
	logs := append([]string(nil), r.logs...)
	turns := make([]webRunTurnView, 0, len(r.turns))
	var current webRunTurnView
	for i, turn := range r.turns {
		view := turn.view()
		turns = append(turns, view)
		if i == r.currentTurn {
			current = view
		}
	}
	runDir := current.RunDir
	artifactURL := current.ArtifactURL
	if artifactURL == "" {
		artifactURL = "/run?id=" + r.loopID
	}
	return webRunView{
		RunID:           r.loopID,
		Status:          r.status,
		Phase:           r.phase,
		StartedAt:       r.startedAt,
		FinishedAt:      r.finishedAt,
		RunDir:          displayRunDir(runDir, current.RunID),
		Logs:            logs,
		Error:           r.err,
		ArtifactURL:     artifactURL,
		Done:            webRunDone(r.status),
		Turns:           turns,
		CurrentTurn:     current,
		AutoContinue:    r.autoContinue,
		MaxTurns:        r.maxTurns,
		FinishRequested: r.finishRequested,
		StopReason:      r.stopReason,
	}
}

func (t webRunTurnState) view() webRunTurnView {
	return webRunTurnView{
		Number:      t.Number,
		RunID:       t.RunID,
		Status:      t.Status,
		Phase:       t.Phase,
		StartedAt:   t.StartedAt,
		FinishedAt:  t.FinishedAt,
		RunDir:      displayRunDir(t.RunDir, t.RunID),
		Error:       t.Error,
		ArtifactURL: "/run?id=" + t.RunID,
		Done:        webRunDone(t.Status),
	}
}

func (r *webRunState) persistLog() {
	r.mu.Lock()
	logs := append([]string(nil), r.logs...)
	runDir := ""
	if r.currentTurn >= 0 && r.currentTurn < len(r.turns) {
		runDir = r.turns[r.currentTurn].RunDir
	}
	r.mu.Unlock()
	if strings.TrimSpace(runDir) == "" || len(logs) == 0 {
		return
	}
	info, err := os.Stat(runDir)
	if err != nil || !info.IsDir() {
		return
	}
	data := strings.Join(logs, "\n") + "\n"
	path, err := safeJoin(runDir, "web-run.log")
	if err != nil {
		return
	}
	redacted := []byte(secrets.Redact(data))
	if err := artifact.AtomicWriteFile(path, redacted, artifact.PrivateFileMode); err != nil {
		return
	}
	runID := filepath.Base(runDir)
	cwd := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))
	store, err := artifact.NewStore(cwd, runID)
	if err == nil {
		_ = store.SaveDocument("web-run.log", redacted)
	}
}

func displayRunDir(runDir, runID string) string {
	if strings.TrimSpace(runID) != "" {
		return ".jj/runs/" + runID
	}
	if strings.TrimSpace(runDir) == "" {
		return ""
	}
	return "[run]"
}

func redactDashboardLogPaths(text string) string {
	return dashboardAbsolutePathPattern.ReplaceAllString(text, "${1}[path]")
}

func webRunDone(status string) bool {
	switch status {
	case "dry_run_complete", "complete", "partial_failed", "planned", "completed", "succeeded", "planning_failed", "implementation_failed", "partial", "success", "needs_work", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func phaseFromLog(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "jj: reading"):
		return "read_plan"
	case strings.Contains(lower, "checking git"):
		return "git_baseline"
	case strings.Contains(lower, "creating run directory"):
		return "artifact_init"
	case strings.Contains(lower, "running") && strings.Contains(lower, "planning agents"):
		return "planning"
	case strings.Contains(lower, "merging planning outputs"):
		return "merge"
	case strings.Contains(lower, "dry run complete"):
		return "dry_run_complete"
	case strings.Contains(lower, "wrote sqlite workspace") || strings.Contains(lower, "wrote .jj/spec.json") || strings.Contains(lower, "wrote .jj/tasks.json"):
		return "write_outputs"
	case strings.Contains(lower, "running codex exec"):
		return "codex"
	case strings.Contains(lower, "capturing git diff"):
		return "git_capture"
	case strings.Contains(lower, "running validation"):
		return "validation"
	case strings.Contains(lower, "jj: done"):
		return "completed"
	case strings.Contains(lower, "jj: failed") || strings.Contains(lower, "jj web: run failed"):
		return "failed"
	default:
		return ""
	}
}
