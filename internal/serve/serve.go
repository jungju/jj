package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jungju/jj/internal/artifact"
	runpkg "github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/secrets"
)

const DefaultAddr = DefaultHost + ":7331"

type RunExecutor func(context.Context, runpkg.Config) (*runpkg.Result, error)

type Config struct {
	CWD         string
	Addr        string
	Host        string
	Port        int
	RunID       string
	Stdout      io.Writer
	Context     context.Context
	RunExecutor RunExecutor

	ConfigSearchDir string
	ConfigFile      string
	CWDExplicit     bool
	AddrExplicit    bool
	HostExplicit    bool
	PortExplicit    bool
}

type Server struct {
	cwd         string
	runID       string
	addr        string
	localOnly   bool
	ctx         context.Context
	webRuns     *webRunRegistry
	runExecutor RunExecutor
	mux         *http.ServeMux
}

type docLink struct {
	Path string
}

type readinessItem struct {
	Label string
	Path  string
	Ready bool
}

type runLink struct {
	ID              string
	Status          string
	StartedAt       string
	FinishedAt      string
	PlannerProvider string
	Evaluation      string
	DryRun          bool
	ErrorSummary    string
	RiskSummary     string
	Risks           []string
	Failures        []string
	NextActions     []string
}

type artifactLink struct {
	Path string
}

type runFormData struct {
	PlanPath       string
	CWD            string
	RunID          string
	DryRun         bool
	AutoContinue   bool
	MaxTurns       int
	PlanningAgents int
	OpenAIModel    string
	CodexModel     string
	SpecDoc        string
	TaskDoc        string
	EvalDoc        string
	AllowNoGit     bool
	LocalOnly      bool
}

type runStartResult struct {
	RunID  string
	RunDir string
}

type pageData struct {
	Title       string
	CWD         string
	SelectedRun string
	TaskSummary string
	Docs        []docLink
	Runs        []runLink
	Readiness   []readinessItem
	DefaultPlan string
	ActiveRuns  []webRunView
	Artifacts   []artifactLink
	RunForm     *runFormData
	RunResult   *runStartResult
	WebRun      *webRunView
	RunsOnly    bool
	Path        string
	RunID       string
	Content     string
	Rendered    template.HTML
	Error       string
}

func Execute(ctx context.Context, cfg Config) error {
	var err error
	cfg, err = ResolveConfig(cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.CWD) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg.CWD = cwd
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = DefaultAddr
	}
	cfg.Context = ctx
	server, err := NewWithConfig(cfg)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	fmt.Fprintf(cfg.Stdout, "jj: serving docs at http://%s\n", listener.Addr().String())

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func New(cwd, runID string) (*Server, error) {
	return NewWithConfig(Config{CWD: cwd, RunID: runID, Addr: DefaultAddr})
}

func NewWithConfig(cfg Config) (*Server, error) {
	var err error
	cfg, err = ResolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	cwd := cfg.CWD
	if strings.TrimSpace(cwd) == "" {
		return nil, errors.New("cwd is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cwd does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cwd is not a directory: %s", abs)
	}
	if strings.TrimSpace(cfg.RunID) != "" {
		if err := artifact.ValidateRunID(cfg.RunID); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = DefaultAddr
	}
	runExecutor := cfg.RunExecutor
	if runExecutor == nil {
		runExecutor = runpkg.Execute
	}
	baseCtx := cfg.Context
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	s := &Server{
		cwd:         abs,
		runID:       cfg.RunID,
		addr:        cfg.Addr,
		localOnly:   isLocalAddr(cfg.Addr),
		ctx:         baseCtx,
		webRuns:     newWebRunRegistry(),
		runExecutor: runExecutor,
		mux:         http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/docs/", s.handleDocsPath)
	s.mux.HandleFunc("/runs", s.handleRunsIndex)
	s.mux.HandleFunc("/runs/", s.handleRunsPath)
	s.mux.HandleFunc("/doc", s.handleDoc)
	s.mux.HandleFunc("/run/new", s.handleRunNew)
	s.mux.HandleFunc("/run/start", s.handleRunStart)
	s.mux.HandleFunc("/run/progress", s.handleRunProgress)
	s.mux.HandleFunc("/run/status", s.handleRunStatus)
	s.mux.HandleFunc("/run/finish", s.handleRunFinish)
	s.mux.HandleFunc("/run", s.handleRun)
	s.mux.HandleFunc("/artifact", s.handleArtifact)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	docs, err := s.discoverDocs()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := s.discoverRuns()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
		return
	}
	readiness := s.workspaceReadiness()
	s.render(w, pageData{
		Title:       "jj docs",
		CWD:         s.cwd,
		SelectedRun: s.runID,
		TaskSummary: s.taskSummary(),
		Docs:        docs,
		Runs:        runs,
		Readiness:   readiness,
		DefaultPlan: firstReadyPath(readiness, "Plan"),
		ActiveRuns:  s.webRuns.activeViews(),
	})
}

func (s *Server) handleRunNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.renderError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	defaultPlan := firstReadyPath(s.workspaceReadiness(), "Plan")
	if defaultPlan == "" {
		defaultPlan = "plan.md"
	}
	s.render(w, pageData{
		Title: "start jj run",
		CWD:   s.cwd,
		RunForm: &runFormData{
			PlanPath:       defaultPlan,
			CWD:            s.cwd,
			DryRun:         true,
			MaxTurns:       10,
			PlanningAgents: runpkg.DefaultPlanningAgents,
			SpecDoc:        runpkg.DefaultSpecDoc,
			TaskDoc:        runpkg.DefaultTaskDoc,
			EvalDoc:        runpkg.DefaultEvalDoc,
			LocalOnly:      s.localOnly,
		},
	})
}

func (s *Server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.renderError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	planPath, err := s.validatePlanPath(r.FormValue("plan_path"))
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	formCWD := strings.TrimSpace(r.FormValue("cwd"))
	if formCWD == "" {
		formCWD = s.cwd
	}
	absFormCWD, err := filepath.Abs(formCWD)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	if absFormCWD != s.cwd {
		s.renderError(w, http.StatusBadRequest, errors.New("cwd must match the served workspace"))
		return
	}
	dryRun := formBool(r, "dry_run")
	autoContinue := formBool(r, "auto_continue")
	allowNoGit := formBool(r, "allow_no_git")
	if autoContinue && dryRun {
		s.renderError(w, http.StatusBadRequest, errors.New("auto continue turns requires full-run; disable dry-run"))
		return
	}
	if !dryRun && !formBool(r, "confirm_full_run") {
		s.renderError(w, http.StatusBadRequest, errors.New("full run requires explicit confirmation"))
		return
	}
	if !dryRun && !s.localOnly {
		s.renderError(w, http.StatusBadRequest, errors.New("full run is only allowed from a local-only server address"))
		return
	}
	maxTurns := 1
	if autoContinue {
		maxTurns = 10
		if raw := strings.TrimSpace(r.FormValue("max_turns")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				s.renderError(w, http.StatusBadRequest, fmt.Errorf("max turns must be a number: %w", err))
				return
			}
			maxTurns = parsed
		}
		if maxTurns < 1 || maxTurns > 50 {
			s.renderError(w, http.StatusBadRequest, errors.New("max turns must be between 1 and 50"))
			return
		}
		if err := s.validateAutoTurnWorkspace(r.Context(), allowNoGit); err != nil {
			s.renderError(w, http.StatusBadRequest, err)
			return
		}
	}
	planningAgents := runpkg.DefaultPlanningAgents
	planningAgentsExplicit := false
	if raw := strings.TrimSpace(r.FormValue("planning_agents")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			s.renderError(w, http.StatusBadRequest, fmt.Errorf("planning agents must be a number: %w", err))
			return
		}
		planningAgents = parsed
		planningAgentsExplicit = true
	}
	runID, runDir, err := s.resolveWebRunID(r.FormValue("run_id"))
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	openAIModel := strings.TrimSpace(r.FormValue("openai_model"))
	codexModel := strings.TrimSpace(r.FormValue("codex_model"))
	specDoc := strings.TrimSpace(r.FormValue("spec_doc"))
	taskDoc := strings.TrimSpace(r.FormValue("task_doc"))
	evalDoc := strings.TrimSpace(r.FormValue("eval_doc"))
	cfg := runpkg.Config{
		PlanPath:               planPath,
		CWD:                    s.cwd,
		RunID:                  runID,
		DryRun:                 dryRun,
		DryRunExplicit:         true,
		AllowNoGit:             allowNoGit,
		AllowNoGitExplicit:     true,
		PlanningAgents:         planningAgents,
		PlanningAgentsExplicit: planningAgentsExplicit,
		OpenAIModel:            openAIModel,
		OpenAIModelExplicit:    openAIModel != "",
		CodexModel:             codexModel,
		CodexModelExplicit:     codexModel != "",
		SpecDoc:                specDoc,
		SpecDocExplicit:        specDoc != "",
		SpecDocPathMode:        specDoc != "",
		TaskDoc:                taskDoc,
		TaskDocExplicit:        taskDoc != "",
		TaskDocPathMode:        taskDoc != "",
		EvalDoc:                evalDoc,
		EvalDocExplicit:        evalDoc != "",
		EvalDocPathMode:        evalDoc != "",
		ConfigSearchDir:        s.cwd,
		Stdout:                 io.Discard,
		Stderr:                 io.Discard,
	}
	cfg.RunID = runID
	webRun := s.webRuns.create(runID, autoContinue, maxTurns)
	webRun.appendLog("jj web: run queued")
	go s.executeWebRunLoop(webRun, cfg, runDir)
	http.Redirect(w, r, "/run/progress?id="+template.URLQueryEscaper(runID), http.StatusSeeOther)
}

func (s *Server) executeWebRunLoop(webRun *webRunState, baseCfg runpkg.Config, firstRunDir string) {
	webRun.setLoopStatus("running", "running", "", "")
	webRun.appendLog("jj web: run in progress")
	nextContext := strings.TrimSpace(baseCfg.AdditionalPlanContext)
	for turn := 1; turn <= webRun.maxTurns; turn++ {
		if turn > 1 && webRun.finishWasRequested() {
			webRun.setLoopStatus("success", "finished", "", "finish requested")
			return
		}
		cfg := baseCfg
		cfg.RunID = turnRunID(baseCfg.RunID, turn)
		cfg.AdditionalPlanContext = nextContext
		runDir := firstRunDir
		if turn > 1 {
			var err error
			runDir, err = s.reserveTurnRunDir(cfg.RunID)
			if err != nil {
				webRun.appendLog("jj web: run failed")
				webRun.setLoopStatus("failed", "failed", err.Error(), "run directory conflict")
				return
			}
		}
		webRun.beginTurn(turn, cfg.RunID, runDir)
		webRun.setCurrentTurnStatus("running", "running", "")
		writer := webRun.writer()
		cfg.Stdout = writer
		cfg.Stderr = writer
		result, err := s.runExecutor(s.ctx, cfg)
		if result != nil && strings.TrimSpace(result.RunDir) != "" {
			webRun.setCurrentTurnRunDir(result.RunDir)
		}
		if err != nil {
			status := "failed"
			if errors.Is(err, context.Canceled) {
				status = "cancelled"
			}
			webRun.appendLog("jj web: run failed")
			webRun.setCurrentTurnStatus(status, status, err.Error())
			webRun.setLoopStatus(status, status, err.Error(), status)
			return
		}
		outcome := s.runOutcomeForRun(cfg.RunID)
		webRun.appendLog("jj web: turn " + cfg.RunID + " completed with status " + outcome.Status)
		webRun.setCurrentTurnStatus(outcome.Status, "completed", outcome.Error)
		if !webRun.autoContinue {
			webRun.setLoopStatus(outcome.Status, "completed", outcome.Error, "single run complete")
			return
		}
		if outcome.CommitFailed {
			webRun.setLoopStatus("failed", "commit_failed", outcome.Error, "commit failed")
			return
		}
		if outcome.Status == runpkg.StatusFailed || outcome.Status == "cancelled" || (strings.HasSuffix(outcome.Status, "_failed") && outcome.Status != runpkg.StatusPartialFailed) {
			webRun.setLoopStatus("failed", "failed", outcome.Error, "turn failed")
			return
		}
		if strings.EqualFold(outcome.EvaluationResult, "PASS") || outcome.Status == runpkg.StatusSuccess || outcome.Status == "success" {
			webRun.setLoopStatus("success", "completed", "", "evaluation PASS")
			return
		}
		if webRun.finishWasRequested() {
			webRun.setLoopStatus(outcome.Status, "finished", "", "finish requested")
			return
		}
		if turn == webRun.maxTurns {
			webRun.setLoopStatus(outcome.Status, "max_turns", "", "max turns reached")
			return
		}
		contextText, err := s.buildContinuationContext(cfg.RunID)
		if err != nil {
			webRun.setLoopStatus("failed", "context", err.Error(), "continuation context failed")
			return
		}
		nextContext = contextText
		webRun.appendLog("jj web: continuing to next turn")
	}
}

func (s *Server) finalStatusForRun(runID string) string {
	runDir, err := s.runDir(runID)
	if err != nil {
		return "success"
	}
	data, err := os.ReadFile(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		return "success"
	}
	var manifest struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || strings.TrimSpace(manifest.Status) == "" {
		return "success"
	}
	return manifest.Status
}

type runOutcome struct {
	Status           string
	EvaluationResult string
	Error            string
	CommitFailed     bool
}

func (s *Server) runOutcomeForRun(runID string) runOutcome {
	outcome := runOutcome{Status: s.finalStatusForRun(runID)}
	runDir, err := s.runDir(runID)
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	data, err := os.ReadFile(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	var manifest struct {
		Status       string   `json:"status"`
		ErrorSummary string   `json:"error_summary"`
		Errors       []string `json:"errors"`
		Evaluation   struct {
			Result string `json:"result"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"evaluation"`
		Commit struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	if strings.TrimSpace(manifest.Status) != "" {
		outcome.Status = manifest.Status
	}
	outcome.EvaluationResult = manifest.Evaluation.Result
	if outcome.EvaluationResult == "" {
		outcome.EvaluationResult = manifest.Evaluation.Status
	}
	outcome.Error = manifest.ErrorSummary
	if outcome.Error == "" && len(manifest.Errors) > 0 {
		outcome.Error = manifest.Errors[0]
	}
	if outcome.Error == "" {
		outcome.Error = manifest.Evaluation.Error
	}
	if manifest.Commit.Status == "failed" {
		outcome.CommitFailed = true
		if manifest.Commit.Error != "" {
			outcome.Error = manifest.Commit.Error
		}
	}
	outcome.Error = secrets.Redact(outcome.Error)
	return outcome
}

func (s *Server) handleRunProgress(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("id")
	view, err := s.webRunView(runID)
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	s.render(w, pageData{
		Title:  "run progress",
		CWD:    s.cwd,
		WebRun: &view,
	})
}

func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("id")
	view, err := s.webRunView(runID)
	if err != nil {
		http.Error(w, secrets.Redact(err.Error()), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleRunFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.renderError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	runID := r.FormValue("id")
	if strings.TrimSpace(runID) == "" {
		runID = r.URL.Query().Get("id")
	}
	if err := artifact.ValidateRunID(runID); err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	webRun := s.webRuns.get(runID)
	if webRun == nil {
		s.renderError(w, http.StatusNotFound, fmt.Errorf("web run not found: %s", runID))
		return
	}
	webRun.requestFinish()
	http.Redirect(w, r, "/run/progress?id="+template.URLQueryEscaper(runID), http.StatusSeeOther)
}

func (s *Server) webRunView(runID string) (webRunView, error) {
	if err := artifact.ValidateRunID(runID); err != nil {
		return webRunView{}, err
	}
	webRun := s.webRuns.get(runID)
	if webRun == nil {
		return webRunView{}, fmt.Errorf("web run not found: %s", runID)
	}
	return webRun.view(), nil
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(rel) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	if strings.ToLower(filepath.Ext(rel)) != ".md" {
		s.renderError(w, http.StatusBadRequest, errors.New("only markdown documents are supported"))
		return
	}
	if !isAllowedDocPath(rel) {
		s.renderError(w, http.StatusBadRequest, fmt.Errorf("document path is not allowed: %s", rel))
		return
	}
	path, err := safeJoin(s.cwd, rel)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	content, rendered := presentContent(rel, data)
	s.render(w, pageData{
		Title:    rel,
		CWD:      s.cwd,
		Path:     filepath.ToSlash(rel),
		Content:  content,
		Rendered: rendered,
	})
}

func (s *Server) handleDocsPath(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/docs/")
	if strings.TrimSpace(rel) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	r.URL.RawQuery = "path=" + url.QueryEscape("docs/"+rel)
	s.handleDoc(w, r)
}

func (s *Server) handleRunsIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/runs" {
		http.NotFound(w, r)
		return
	}
	runs, err := s.discoverRuns()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err)
		return
	}
	s.render(w, pageData{
		Title:    "runs",
		CWD:      s.cwd,
		Runs:     runs,
		RunsOnly: true,
	})
}

func (s *Server) handleRunsPath(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/runs/"), "/")
	if rest == "" {
		r.URL.Path = "/runs"
		s.handleRunsIndex(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	runID := parts[0]
	if len(parts) == 1 {
		s.renderRunArtifacts(w, runID)
		return
	}
	switch parts[1] {
	case "manifest":
		s.handleRunManifest(w, runID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("id")
	if strings.TrimSpace(runID) == "" {
		runID = s.runID
	}
	if strings.TrimSpace(runID) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}
	s.renderRunArtifacts(w, runID)
}

func (s *Server) renderRunArtifacts(w http.ResponseWriter, runID string) {
	if strings.TrimSpace(runID) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	artifacts, err := discoverArtifacts(runDir)
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	s.render(w, pageData{
		Title:     "run " + runID,
		CWD:       s.cwd,
		RunID:     runID,
		Artifacts: artifacts,
	})
}

func (s *Server) handleRunManifest(w http.ResponseWriter, runID string) {
	runDir, err := s.runDir(runID)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	data, err := os.ReadFile(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	redacted := secrets.Redact(string(data))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var decoded any
	if err := json.Unmarshal([]byte(redacted), &decoded); err != nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"run_id": runID,
			"status": "unknown",
			"error":  "manifest is corrupt",
		})
		return
	}
	_, _ = io.WriteString(w, redacted)
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run")
	rawRel := r.URL.Query().Get("path")
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(rawRel) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("run and path are required"))
		return
	}
	rel, err := cleanAllowedArtifactPath(rawRel)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	if ok, err := isManifestListedArtifact(runDir, rel); err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	} else if !ok {
		s.renderError(w, http.StatusBadRequest, fmt.Errorf("artifact path is not listed in manifest: %s", rel))
		return
	}
	path, err := safeJoin(runDir, rel)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.renderError(w, http.StatusNotFound, err)
		return
	}
	content, rendered := presentContent(rel, data)
	s.render(w, pageData{
		Title:    runID + "/" + rel,
		CWD:      s.cwd,
		RunID:    runID,
		Path:     filepath.ToSlash(rel),
		Content:  content,
		Rendered: rendered,
	})
}

func (s *Server) runDir(runID string) (string, error) {
	if err := artifact.ValidateRunID(runID); err != nil {
		return "", err
	}
	return safeJoin(filepath.Join(s.cwd, ".jj", "runs"), runID)
}

func (s *Server) resolveWebRunID(raw string) (string, string, error) {
	runID := strings.TrimSpace(raw)
	if runID == "" {
		runID = artifact.NewRunID(time.Now())
	}
	if err := artifact.ValidateRunID(runID); err != nil {
		return "", "", err
	}
	if s.webRuns.isActive(runID) {
		return "", "", fmt.Errorf("web run is already active: %s", runID)
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		return "", "", err
	}
	if info, err := os.Stat(runDir); err == nil && info.IsDir() {
		return "", "", fmt.Errorf("run directory already exists: %s", runDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	return runID, runDir, nil
}

func (s *Server) reserveTurnRunDir(runID string) (string, error) {
	if err := artifact.ValidateRunID(runID); err != nil {
		return "", err
	}
	if s.webRuns.isActive(runID) {
		return "", fmt.Errorf("web run is already active: %s", runID)
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(runDir); err == nil && info.IsDir() {
		return "", fmt.Errorf("run directory already exists: %s", runDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return runDir, nil
}

func (s *Server) validateAutoTurnWorkspace(ctx context.Context, allowNoGit bool) error {
	gitState, err := runpkg.InspectGit(ctx, s.cwd)
	if err != nil {
		return fmt.Errorf("inspect git state: %w", err)
	}
	if !gitState.Available && !allowNoGit {
		return errors.New("auto continue turns requires a git repository")
	}
	return nil
}

func turnRunID(loopID string, turn int) string {
	if turn <= 1 {
		return loopID
	}
	return fmt.Sprintf("%s-t%02d", loopID, turn)
}

func (s *Server) workspaceReadiness() []readinessItem {
	items := []readinessItem{
		{Label: "Plan", Path: "plan.md"},
		{Label: "README", Path: "README.md"},
		{Label: "SPEC", Path: "docs/SPEC.md"},
		{Label: "TASK", Path: "docs/TASK.md"},
	}
	for i := range items {
		path, err := safeJoin(s.cwd, items[i].Path)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		items[i].Ready = err == nil && !info.IsDir()
	}
	return items
}

func (s *Server) taskSummary() string {
	path, err := safeJoin(s.cwd, "docs/TASK.md")
	if err != nil {
		return "docs/TASK.md is missing."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "docs/TASK.md is missing."
	}
	text := secrets.Redact(string(data))
	progress := taskChecklistProgress(text)
	lines := strings.Fields(strings.TrimSpace(text))
	if len(lines) == 0 {
		return "docs/TASK.md is empty."
	}
	if len(lines) > 40 {
		lines = lines[:40]
	}
	summary := strings.Join(lines, " ")
	if progress != "" {
		return progress + " " + summary
	}
	return summary
}

func taskChecklistProgress(markdown string) string {
	total := 0
	done := 0
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "- [ ]") || strings.HasPrefix(lower, "* [ ]") {
			total++
			continue
		}
		if strings.HasPrefix(lower, "- [x]") || strings.HasPrefix(lower, "* [x]") {
			total++
			done++
		}
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("TASK checklist: %d/%d complete.", done, total)
}

func (s *Server) buildContinuationContext(previousRunID string) (string, error) {
	runDir, err := s.runDir(previousRunID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("This is an automatic continuation turn for jj.\n")
	b.WriteString("Use the original plan as the source of truth, then use this evidence to decide the next smallest useful change.\n\n")
	b.WriteString("Previous run id: ")
	b.WriteString(previousRunID)
	b.WriteString("\n\n")
	s.appendContinuationFile(&b, "Workspace SPEC", filepath.Join(s.cwd, "docs", "SPEC.md"))
	s.appendContinuationFile(&b, "Workspace TASK", filepath.Join(s.cwd, "docs", "TASK.md"))
	s.appendContinuationFile(&b, "Workspace EVAL", filepath.Join(s.cwd, "docs", "EVAL.md"))
	s.appendContinuationFile(&b, "Previous Manifest", filepath.Join(runDir, "manifest.json"))
	s.appendContinuationFile(&b, "Previous Git Diff Summary", filepath.Join(runDir, "git", "diff-summary.txt"))
	s.appendContinuationFile(&b, "Previous Codex Summary", filepath.Join(runDir, "codex", "summary.md"))
	return truncateDisplay(secrets.Redact(b.String()), 60000), nil
}

func (s *Server) appendContinuationFile(b *strings.Builder, title, path string) {
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return
	}
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(truncateDisplay(secrets.Redact(string(data)), 12000))
	b.WriteString("\n\n")
}

func truncateDisplay(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]..."
}

func firstReadyPath(items []readinessItem, label string) string {
	for _, item := range items {
		if item.Label == label && item.Ready {
			return item.Path
		}
	}
	return ""
}

func (s *Server) validatePlanPath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("plan path is required")
	}
	if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	if !isMarkdown(rel) {
		return "", errors.New("plan path must be a markdown file")
	}
	path, err := safeJoin(s.cwd, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("plan path is not readable: %w", err)
	}
	if info.IsDir() {
		return "", errors.New("plan path must be a file")
	}
	normalized, err := filepath.Rel(s.cwd, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(normalized), nil
}

func (s *Server) discoverDocs() ([]docLink, error) {
	var docs []docLink
	err := filepath.WalkDir(s.cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".jj", "node_modules", "vendor":
				return filepath.SkipDir
			}
			if path != s.cwd && !isAllowedDocDir(s.cwd, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(name)) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(s.cwd, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isRootDoc(rel) || strings.HasPrefix(rel, "docs/") || strings.HasPrefix(rel, "playground/") {
			docs = append(docs, docLink{Path: rel})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

func (s *Server) discoverRuns() ([]runLink, error) {
	runsDir := filepath.Join(s.cwd, ".jj", "runs")
	entries, err := os.ReadDir(runsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	runs := make([]runLink, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := artifact.ValidateRunID(entry.Name()); err != nil {
			continue
		}
		run := runLink{ID: entry.Name()}
		manifestPath := filepath.Join(runsDir, entry.Name(), "manifest.json")
		if data, err := os.ReadFile(manifestPath); err == nil {
			var manifest struct {
				Status          string `json:"status"`
				StartedAt       string `json:"started_at"`
				FinishedAt      string `json:"finished_at"`
				EndedAt         string `json:"ended_at"`
				PlannerProvider string `json:"planner_provider"`
				Planner         struct {
					Provider string `json:"provider"`
				} `json:"planner"`
				DryRun     bool `json:"dry_run"`
				Evaluation struct {
					Result string `json:"result"`
					Status string `json:"status"`
					Error  string `json:"error"`
				} `json:"evaluation"`
				Errors []string `json:"errors"`
				Risks  []string `json:"risks"`
			}
			_ = json.Unmarshal(data, &manifest)
			run.Status = manifest.Status
			run.StartedAt = manifest.StartedAt
			run.FinishedAt = manifest.FinishedAt
			if run.FinishedAt == "" {
				run.FinishedAt = manifest.EndedAt
			}
			run.PlannerProvider = manifest.PlannerProvider
			if run.PlannerProvider == "" {
				run.PlannerProvider = manifest.Planner.Provider
			}
			run.DryRun = manifest.DryRun
			run.Evaluation = manifest.Evaluation.Result
			if run.Evaluation == "" {
				run.Evaluation = manifest.Evaluation.Status
			}
			if run.Evaluation == "" {
				run.Evaluation = manifest.Evaluation.Error
			}
			run.Evaluation = secrets.Redact(run.Evaluation)
			if len(manifest.Errors) > 0 {
				run.ErrorSummary = secrets.Redact(manifest.Errors[0])
				run.Failures = redactList(manifest.Errors)
			}
			if len(manifest.Risks) > 0 {
				run.RiskSummary = secrets.Redact(manifest.Risks[0])
				run.Risks = redactList(manifest.Risks)
			}
		}
		evalPath := filepath.Join(runsDir, entry.Name(), "docs", "EVAL.md")
		if data, err := os.ReadFile(evalPath); err == nil {
			evalText := secrets.Redact(string(data))
			if run.Evaluation == "" {
				run.Evaluation = firstMarkdownValue(evalText, "Verdict", "Result")
			}
			if len(run.Risks) == 0 {
				run.Risks = markdownListSection(evalText, "Risks")
				if len(run.Risks) > 0 {
					run.RiskSummary = run.Risks[0]
				}
			}
			run.Failures = appendUnique(run.Failures, markdownListSection(evalText, "Regressions")...)
			run.Failures = appendUnique(run.Failures, markdownListSection(evalText, "Failures")...)
			run.NextActions = appendUnique(run.NextActions, markdownListSection(evalText, "Next Actions")...)
			run.NextActions = appendUnique(run.NextActions, markdownListSection(evalText, "Recommended Follow-ups")...)
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].ID == s.runID {
			return true
		}
		if runs[j].ID == s.runID {
			return false
		}
		return runs[i].ID > runs[j].ID
	})
	return runs, nil
}

func redactList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(secrets.Redact(item))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstMarkdownValue(markdown string, names ...string) string {
	for _, name := range names {
		items := markdownListSection(markdown, name)
		if len(items) > 0 {
			return items[0]
		}
		value := markdownParagraphSection(markdown, name)
		if value != "" {
			return value
		}
	}
	return ""
}

func markdownParagraphSection(markdown, name string) string {
	section := markdownSection(markdown, name)
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line != "" {
			return secrets.Redact(line)
		}
	}
	return ""
}

func markdownListSection(markdown, name string) []string {
	section := markdownSection(markdown, name)
	var out []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if item != "" && item != "(none)" {
				out = append(out, secrets.Redact(item))
			}
		}
	}
	return out
}

func markdownSection(markdown, name string) string {
	target := "## " + name
	lines := strings.Split(markdown, "\n")
	var b strings.Builder
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break
			}
			inSection = strings.EqualFold(trimmed, target)
			continue
		}
		if inSection {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func appendUnique(items []string, add ...string) []string {
	for _, item := range add {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		seen := false
		for _, existing := range items {
			if existing == item {
				seen = true
				break
			}
		}
		if !seen {
			items = append(items, item)
		}
	}
	return items
}

func discoverArtifacts(runDir string) ([]artifactLink, error) {
	var artifacts []artifactLink
	allowed, err := manifestArtifactPaths(runDir)
	if err != nil {
		return nil, err
	}
	for rel := range allowed {
		path, err := safeJoin(runDir, rel)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		artifacts = append(artifacts, artifactLink{Path: rel})
	}
	sort.SliceStable(artifacts, func(i, j int) bool {
		return artifactRank(artifacts[i].Path) < artifactRank(artifacts[j].Path) ||
			(artifactRank(artifacts[i].Path) == artifactRank(artifacts[j].Path) && artifacts[i].Path < artifacts[j].Path)
	})
	return artifacts, nil
}

func artifactRank(path string) int {
	switch path {
	case "docs/SPEC.md":
		return 0
	case "docs/TASK.md":
		return 1
	case "docs/EVAL.md":
		return 2
	case "SPEC.md":
		return 3
	case "TASK.md":
		return 4
	case "EVAL.md":
		return 5
	case "manifest.json":
		return 6
	default:
		return 10
	}
}

func isAllowedDocDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "docs" || strings.HasPrefix(rel, "docs/") || rel == "playground" || strings.HasPrefix(rel, "playground/")
}

func isAllowedDocPath(rel string) bool {
	clean, err := cleanAllowedRelativePath(rel)
	if err != nil {
		return false
	}
	return isRootDoc(clean) || strings.HasPrefix(clean, "docs/") || clean == "playground/plan.md" || strings.HasPrefix(clean, "playground/")
}

func isAllowedArtifactPath(rel string) bool {
	_, err := cleanAllowedArtifactPath(rel)
	return err == nil
}

func cleanAllowedArtifactPath(rel string) (string, error) {
	clean, err := cleanAllowedRelativePath(rel)
	if err != nil {
		return "", fmt.Errorf("artifact path is not allowed: %s", rel)
	}
	return clean, nil
}

func cleanAllowedRelativePath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("path is required")
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("path contains NUL byte: %s", rel)
	}
	if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("backslashes are not allowed in paths: %s", rel)
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	if isWindowsDrivePath(rel) || strings.HasPrefix(rel, "//") {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("path segment is not allowed: %s", rel)
		}
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean != rel || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path must be clean and stay inside the root: %s", rel)
	}
	return clean, nil
}

func isWindowsDrivePath(rel string) bool {
	if len(rel) < 2 || rel[1] != ':' {
		return false
	}
	c := rel[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isRootDoc(rel string) bool {
	switch rel {
	case "README.md", "plan.md", "SPEC.md", "TASK.md", "EVAL.md":
		return true
	default:
		return false
	}
}

func isManifestListedArtifact(runDir, rel string) (bool, error) {
	artifacts, err := manifestArtifactPaths(runDir)
	if err != nil {
		return false, err
	}
	_, ok := artifacts[rel]
	return ok, nil
}

func manifestArtifactPaths(runDir string) (map[string]struct{}, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var manifest struct {
		Artifacts map[string]string `json:"artifacts"`
		Planner   struct {
			Artifacts map[string]string `json:"artifacts"`
		} `json:"planner"`
		Git struct {
			BaselinePath     string `json:"baseline_path"`
			BaselineTextPath string `json:"baseline_text_path"`
			StatusBeforePath string `json:"status_before_path"`
			StatusAfterPath  string `json:"status_after_path"`
			StatusPath       string `json:"status_path"`
			DiffPath         string `json:"diff_path"`
			DiffStatPath     string `json:"diff_stat_path"`
			DiffSummaryPath  string `json:"diff_summary_path"`
		} `json:"git"`
		Codex struct {
			EventsPath  string `json:"events_path"`
			SummaryPath string `json:"summary_path"`
			ExitPath    string `json:"exit_path"`
		} `json:"codex"`
		Evaluation struct {
			EvalPath string `json:"eval_path"`
		} `json:"evaluation"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	artifacts := map[string]struct{}{}
	add := func(raw string) {
		if clean, err := cleanAllowedArtifactPath(raw); err == nil {
			artifacts[clean] = struct{}{}
		}
	}
	for _, path := range manifest.Artifacts {
		add(path)
	}
	for _, path := range manifest.Planner.Artifacts {
		add(path)
	}
	add(manifest.Git.BaselinePath)
	add(manifest.Git.BaselineTextPath)
	add(manifest.Git.StatusBeforePath)
	add(manifest.Git.StatusAfterPath)
	add(manifest.Git.StatusPath)
	add(manifest.Git.DiffPath)
	add(manifest.Git.DiffStatPath)
	add(manifest.Git.DiffSummaryPath)
	add(manifest.Codex.EventsPath)
	add(manifest.Codex.SummaryPath)
	add(manifest.Codex.ExitPath)
	add(manifest.Evaluation.EvalPath)
	return artifacts, nil
}

func safeJoin(root, rel string) (string, error) {
	if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	path := filepath.Join(root, clean)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relToRoot, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root: %s", rel)
	}
	if evalRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		if evalPath, err := filepath.EvalSymlinks(absPath); err == nil {
			relToEvalRoot, err := filepath.Rel(evalRoot, evalPath)
			if err != nil {
				return "", err
			}
			if relToEvalRoot == ".." || strings.HasPrefix(relToEvalRoot, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("path escapes root: %s", rel)
			}
		}
	}
	return absPath, nil
}

func formBool(r *http.Request, name string) bool {
	switch strings.ToLower(strings.TrimSpace(r.FormValue(name))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func isLocalAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	switch strings.ToLower(host) {
	case "localhost":
		return true
	case "":
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) renderError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	s.render(w, pageData{Title: "error", CWD: s.cwd, Error: secrets.Redact(err.Error())})
}

func (s *Server) renderErrorData(w http.ResponseWriter, status int, data pageData) {
	w.WriteHeader(status)
	data.Error = secrets.Redact(data.Error)
	s.render(w, data)
}

func (s *Server) render(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func presentContent(path string, data []byte) (string, template.HTML) {
	redacted := secrets.Redact(string(data))
	if isMarkdown(path) {
		return "", renderMarkdown(redacted)
	}
	return redacted, ""
}

func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func renderMarkdown(content string) template.HTML {
	var b strings.Builder
	inList := false
	closeList := func() {
		if inList {
			b.WriteString("</ul>\n")
			inList = false
		}
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			closeList()
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "# "):
			closeList()
			b.WriteString("<h1>")
			b.WriteString(template.HTMLEscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))))
			b.WriteString("</h1>\n")
		case strings.HasPrefix(trimmed, "## "):
			closeList()
			b.WriteString("<h2>")
			b.WriteString(template.HTMLEscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))))
			b.WriteString("</h2>\n")
		case strings.HasPrefix(trimmed, "### "):
			closeList()
			b.WriteString("<h3>")
			b.WriteString(template.HTMLEscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))))
			b.WriteString("</h3>\n")
		case strings.HasPrefix(trimmed, "- "):
			if !inList {
				b.WriteString("<ul>\n")
				inList = true
			}
			b.WriteString("<li>")
			b.WriteString(template.HTMLEscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			b.WriteString("</li>\n")
		default:
			closeList()
			b.WriteString("<p>")
			b.WriteString(template.HTMLEscapeString(trimmed))
			b.WriteString("</p>\n")
		}
	}
	closeList()
	return template.HTML(b.String())
}

var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"q": func(s string) string { return template.URLQueryEscaper(s) },
}).Parse(`<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: Canvas; color: CanvasText; }
    header { padding: 18px 24px; border-bottom: 1px solid color-mix(in srgb, CanvasText 18%, transparent); }
    main { max-width: 1120px; margin: 0 auto; padding: 24px; }
    h1 { margin: 0 0 6px; font-size: 22px; }
    h2 { margin-top: 28px; font-size: 16px; }
    .muted { color: color-mix(in srgb, CanvasText 62%, transparent); font-size: 13px; }
    .grid { display: grid; gap: 18px; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); }
    .ready-grid { display: grid; gap: 10px; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); margin: 12px 0 16px; }
    .ready-item { border: 1px solid color-mix(in srgb, CanvasText 16%, transparent); border-radius: 6px; padding: 10px; }
    .status { display: inline-block; margin-top: 6px; font-size: 12px; font-weight: 700; }
    .status.ready { color: #067647; }
    .status.missing { color: #b42318; }
    .status.running { color: #175cd3; }
    .actions { display: flex; gap: 10px; flex-wrap: wrap; margin: 14px 0 22px; }
    .button { display: inline-flex; align-items: center; justify-content: center; min-height: 36px; padding: 0 12px; border: 1px solid color-mix(in srgb, CanvasText 18%, transparent); border-radius: 6px; background: color-mix(in srgb, CanvasText 5%, Canvas); color: CanvasText; text-decoration: none; font: inherit; cursor: pointer; }
    .button.primary { background: LinkText; color: Canvas; border-color: LinkText; }
    form { max-width: 760px; display: grid; gap: 14px; }
    label { display: grid; gap: 5px; font-size: 13px; font-weight: 600; }
    input { min-height: 34px; padding: 6px 8px; border: 1px solid color-mix(in srgb, CanvasText 22%, transparent); border-radius: 4px; background: Canvas; color: CanvasText; font: inherit; }
    input[type="checkbox"] { min-height: 0; width: 16px; height: 16px; }
    .check { display: flex; gap: 8px; align-items: center; font-weight: 500; }
    ul { list-style: none; padding: 0; margin: 10px 0 0; }
    li { padding: 7px 0; border-bottom: 1px solid color-mix(in srgb, CanvasText 10%, transparent); }
    a { color: LinkText; text-decoration: none; }
    a:hover { text-decoration: underline; }
    pre { overflow: auto; padding: 18px; border: 1px solid color-mix(in srgb, CanvasText 16%, transparent); border-radius: 6px; line-height: 1.45; white-space: pre-wrap; word-break: break-word; }
    pre.logs { max-height: 360px; background: color-mix(in srgb, CanvasText 4%, Canvas); }
    .turns li { display: grid; gap: 3px; }
    article { overflow-wrap: anywhere; line-height: 1.55; }
    article h1 { margin-top: 18px; }
    article h2 { margin-top: 24px; }
    article ul { list-style: disc; padding-left: 22px; }
    article li { border: 0; padding: 3px 0; }
    .error { color: #b42318; }
  </style>
</head>
<body>
  <header>
    <h1>{{.Title}}</h1>
    <div class="muted">{{.CWD}}</div>
  </header>
  <main>
    {{if .Error}}
      <p class="error">{{.Error}}</p>
      {{if .RunResult}}
      <p><a href="/run?id={{q .RunResult.RunID}}">Open run {{.RunResult.RunID}}</a></p>
      <div class="muted">{{.RunResult.RunDir}}</div>
      {{end}}
    {{else if .WebRun}}
      <p><a href="/">← dashboard</a></p>
      <section id="run-progress" data-status-url="/run/status?id={{q .WebRun.RunID}}">
        <h2 id="run-status-line">{{if .WebRun.Done}}Run {{.WebRun.Status}}{{else}}Run in progress{{end}}</h2>
        <p>
          <strong>{{.WebRun.RunID}}</strong>
          <span class="status running" id="run-status">{{.WebRun.Status}}</span>
          <span class="muted" id="run-phase">{{.WebRun.Phase}}</span>
        </p>
        <p class="muted">auto continue {{.WebRun.AutoContinue}} · max turns {{.WebRun.MaxTurns}} · finish requested <span id="finish-requested">{{.WebRun.FinishRequested}}</span></p>
        <p class="muted" id="run-stop-reason">{{.WebRun.StopReason}}</p>
        <div class="muted" id="run-dir">{{.WebRun.RunDir}}</div>
        {{if not .WebRun.Done}}
        <form method="post" action="/run/finish" style="margin: 12px 0; max-width: 240px;">
          <input type="hidden" name="id" value="{{.WebRun.RunID}}">
          <button class="button" type="submit">Finish Turn</button>
        </form>
        {{end}}
        <p id="run-artifact-link" {{if not .WebRun.Done}}style="display:none"{{end}}><a href="{{.WebRun.ArtifactURL}}">Open run artifacts</a></p>
        <p class="error" id="run-error">{{.WebRun.Error}}</p>
        <h2>Turns</h2>
        <ul class="turns" id="run-turns">
        {{range .WebRun.Turns}}
          <li><a href="{{.ArtifactURL}}">Turn {{.Number}} · {{.RunID}}</a><span class="muted">{{.Status}} {{.Phase}}</span></li>
        {{else}}
          <li class="muted">No turns started yet.</li>
        {{end}}
        </ul>
        <h2>Logs</h2>
        <pre class="logs" id="run-logs">{{range .WebRun.Logs}}{{.}}
{{end}}</pre>
      </section>
      <script>
      (function () {
        const root = document.getElementById("run-progress");
        if (!root) return;
        const statusURL = root.dataset.statusUrl;
        const statusLine = document.getElementById("run-status-line");
        const statusEl = document.getElementById("run-status");
        const phaseEl = document.getElementById("run-phase");
        const dirEl = document.getElementById("run-dir");
        const logsEl = document.getElementById("run-logs");
        const errorEl = document.getElementById("run-error");
        const artifactEl = document.getElementById("run-artifact-link");
        const turnsEl = document.getElementById("run-turns");
        const finishEl = document.getElementById("finish-requested");
        const stopEl = document.getElementById("run-stop-reason");
        let timer = null;
        async function refresh() {
          const response = await fetch(statusURL, {cache: "no-store"});
          if (!response.ok) return;
          const data = await response.json();
          statusEl.textContent = data.status || "";
          phaseEl.textContent = data.phase || "";
          dirEl.textContent = data.run_dir || "";
          errorEl.textContent = data.error || "";
          logsEl.textContent = (data.logs || []).join("\n");
          finishEl.textContent = String(!!data.finish_requested);
          stopEl.textContent = data.stop_reason || "";
          turnsEl.innerHTML = "";
          const turns = data.turns || [];
          if (turns.length === 0) {
            const item = document.createElement("li");
            item.className = "muted";
            item.textContent = "No turns started yet.";
            turnsEl.appendChild(item);
          } else {
            for (const turn of turns) {
              const item = document.createElement("li");
              const link = document.createElement("a");
              link.href = turn.artifact_url || ("/run?id=" + encodeURIComponent(turn.run_id || ""));
              link.textContent = "Turn " + turn.number + " · " + turn.run_id;
              const meta = document.createElement("span");
              meta.className = "muted";
              meta.textContent = (turn.status || "") + " " + (turn.phase || "");
              item.appendChild(link);
              item.appendChild(meta);
              turnsEl.appendChild(item);
            }
          }
          if (data.done) {
            statusLine.textContent = "Run " + data.status;
            artifactEl.style.display = "";
            if (timer) window.clearInterval(timer);
          } else {
            statusLine.textContent = "Run in progress";
          }
        }
        timer = window.setInterval(refresh, 1500);
        refresh();
      }());
      </script>
    {{else if .RunResult}}
      <p>Run completed.</p>
      <p><a href="/run?id={{q .RunResult.RunID}}">Open run {{.RunResult.RunID}}</a></p>
      <div class="muted">{{.RunResult.RunDir}}</div>
      <p><a href="/">← dashboard</a></p>
    {{else if .RunForm}}
      <p><a href="/">← dashboard</a></p>
      <form method="post" action="/run/start">
        <label>plan path
          <input name="plan_path" value="{{.RunForm.PlanPath}}" required>
        </label>
        <label>cwd
          <input name="cwd" value="{{.RunForm.CWD}}" required>
        </label>
        <label>run id
          <input name="run_id" value="{{.RunForm.RunID}}" placeholder="auto">
        </label>
        <label>planning agents
          <input name="planning_agents" value="{{.RunForm.PlanningAgents}}">
        </label>
        <label>OpenAI model
          <input name="openai_model" value="{{.RunForm.OpenAIModel}}" placeholder="configured default">
        </label>
        <label>Codex model
          <input name="codex_model" value="{{.RunForm.CodexModel}}" placeholder="Codex CLI default">
        </label>
	        <label>SPEC path
	          <input name="spec_doc" value="{{.RunForm.SpecDoc}}">
	        </label>
	        <label>TASK path
	          <input name="task_doc" value="{{.RunForm.TaskDoc}}">
	        </label>
	        <label>EVAL path
	          <input name="eval_doc" value="{{.RunForm.EvalDoc}}">
	        </label>
        <label class="check"><input type="checkbox" name="dry_run" value="true" {{if .RunForm.DryRun}}checked{{end}}> dry-run</label>
        <label class="check"><input type="checkbox" name="auto_continue" value="true" {{if .RunForm.AutoContinue}}checked{{end}}> auto continue turns</label>
        <label>max turns
          <input name="max_turns" value="{{.RunForm.MaxTurns}}">
        </label>
        <label class="check"><input type="checkbox" name="allow_no_git" value="true" {{if .RunForm.AllowNoGit}}checked{{end}}> allow no git</label>
        <label class="check"><input type="checkbox" name="confirm_full_run" value="true"> confirm full-run workspace mutation</label>
        {{if not .RunForm.LocalOnly}}
          <p class="muted">This server is not bound to a local-only address. Full-run requests will be rejected.</p>
        {{end}}
        <button class="button primary" type="submit">Start Run</button>
      </form>
    {{else if .RunsOnly}}
      <p><a href="/">← dashboard</a></p>
      <h2>Runs</h2>
      <ul>
      {{range .Runs}}
        <li><a href="/runs/{{q .ID}}">{{.ID}}</a> <span class="muted">{{.Status}} {{.StartedAt}} {{.PlannerProvider}} {{.Evaluation}}</span>{{if .ErrorSummary}} <span class="error">{{.ErrorSummary}}</span>{{end}}</li>
      {{else}}
        <li class="muted">No jj runs found.</li>
      {{end}}
      </ul>
    {{else if or .Content .Rendered}}
      <p><a href="/">← index</a>{{if .RunID}} · <a href="/run?id={{q .RunID}}">run {{.RunID}}</a>{{end}}</p>
      <div class="muted">{{.Path}}</div>
      {{if .Rendered}}
      <article>{{.Rendered}}</article>
      {{else}}
      <pre>{{.Content}}</pre>
      {{end}}
    {{else if .Artifacts}}
      <p><a href="/">← index</a></p>
      <h2>Artifacts</h2>
      <ul>
      {{range .Artifacts}}
        <li><a href="/artifact?run={{q $.RunID}}&path={{q .Path}}">{{.Path}}</a></li>
      {{else}}
        <li class="muted">No artifacts found.</li>
      {{end}}
      </ul>
	    {{else}}
	      <section>
	        <h2>Current TASK</h2>
	        <p>{{.TaskSummary}}</p>
	      </section>
	      <section>
	        <h2>Latest Run</h2>
	        {{if .Runs}}{{with index .Runs 0}}
	          <p><a href="/run?id={{q .ID}}">{{.ID}}</a> <span class="muted">{{.Status}} {{.StartedAt}}</span></p>
	          <p class="muted">provider {{.PlannerProvider}} · dry-run {{.DryRun}} · evaluation {{.Evaluation}}</p>
	          <p><a href="/runs/{{q .ID}}/manifest">Raw manifest</a> · <a href="/artifact?run={{q .ID}}&path=docs/EVAL.md">Evaluation artifact</a></p>
	          {{if .Evaluation}}<p><strong>Evaluation Verdict</strong> {{.Evaluation}}</p>{{end}}
	          {{if .ErrorSummary}}<p class="error">{{.ErrorSummary}}</p>{{end}}
	          {{if .RiskSummary}}<p class="muted">{{.RiskSummary}}</p>{{end}}
	        {{end}}{{else}}
	          <p class="muted">No jj runs found. First suggested command: <code>jj run plan.md --dry-run</code></p>
	        {{end}}
	      </section>
	      <section>
	        <h2>Risks And Failures</h2>
	        {{if .Runs}}{{with index .Runs 0}}
	          {{if .ErrorSummary}}<p class="error">{{.ErrorSummary}}</p>{{else if .RiskSummary}}<p>{{.RiskSummary}}</p>{{else}}<p class="muted">No recorded failures or risks in the latest run.</p>{{end}}
	        {{end}}{{else}}
	          <p class="muted">No runs available for risk review.</p>
	        {{end}}
	      </section>
	      <section>
	        <h2>Workspace Readiness</h2>
        <div class="ready-grid">
        {{range .Readiness}}
          <div class="ready-item">
            <strong>{{.Label}}</strong>
            {{if .Ready}}
              <div><a href="/doc?path={{q .Path}}">{{.Path}}</a></div>
              <span class="status ready">{{.Label}} Ready</span>
            {{else}}
              <div class="muted">{{.Path}}</div>
              <span class="status missing">{{.Label}} Missing</span>
            {{end}}
          </div>
        {{end}}
        </div>
	        <div class="actions">
	          {{if .DefaultPlan}}<a class="button primary" href="/run/new">Start Web Run</a><a class="button" href="/doc?path={{q .DefaultPlan}}">Open Plan</a>{{end}}
	          <a class="button" href="/doc?path=docs/TASK.md">Open TASK</a>
	          <a class="button" href="/runs">Open Runs</a>
	        </div>
      </section>
      {{if .ActiveRuns}}
      <section>
        <h2>Active Web Runs</h2>
        <ul>
        {{range .ActiveRuns}}
          <li><a href="/run/progress?id={{q .RunID}}">{{.RunID}}</a> <span class="muted">{{.Status}} {{.Phase}} turn {{.CurrentTurn.Number}} {{.StopReason}}</span></li>
        {{end}}
        </ul>
      </section>
      {{end}}
      <div class="grid">
        <section>
          <h2>Docs</h2>
          <ul>
          {{range .Docs}}
            <li><a href="/doc?path={{q .Path}}">{{.Path}}</a></li>
          {{else}}
            <li class="muted">No markdown docs found.</li>
          {{end}}
          </ul>
        </section>
	        <section>
	          <h2>Runs</h2>
	          <ul>
	          {{range .Runs}}
	            <li><a href="/run?id={{q .ID}}">{{.ID}}</a> <span class="muted">{{.Status}} {{.StartedAt}} {{.PlannerProvider}} {{.Evaluation}}</span> <a href="/runs/{{q .ID}}/manifest">manifest</a>{{if .ErrorSummary}} <span class="error">{{.ErrorSummary}}</span>{{else if .RiskSummary}} <span class="muted">{{.RiskSummary}}</span>{{end}}</li>
	          {{else}}
	            <li class="muted">No jj runs found. Try <code>jj run plan.md --dry-run</code>.</li>
	          {{end}}
	          </ul>
	        </section>
	        <section>
	          <h2>Next Actions</h2>
	          <ul>
	            {{if .DefaultPlan}}<li><a href="/run/new">Start Web Run</a></li><li><a href="/doc?path={{q .DefaultPlan}}">Review plan</a></li>{{end}}
	            {{if .Runs}}{{with index .Runs 0}}{{range .NextActions}}<li>{{.}}</li>{{end}}{{end}}{{else}}<li><code>jj run plan.md --dry-run</code></li>{{end}}
	            <li><a href="/doc?path=docs/TASK.md">Open TASK</a></li>
	            <li><a href="/doc?path=docs/SPEC.md">Open SPEC</a></li>
	          </ul>
	        </section>
	      </div>
    {{end}}
  </main>
</body>
</html>`))
