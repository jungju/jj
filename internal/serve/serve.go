package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jungju/jj/internal/artifact"
	runpkg "github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/secrets"
	"github.com/jungju/jj/internal/security"
)

const DefaultAddr = DefaultHost + ":7331"

const displayWorkspace = "[workspace]"

var allowedProjectDocPaths = []string{
	"README.md",
	"plan.md",
	"docs/SPEC.md",
	"docs/TASK.md",
	runpkg.DefaultSpecStatePath,
	runpkg.DefaultTasksStatePath,
}

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
	ID                       string
	Status                   string
	StartedAt                string
	FinishedAt               string
	PlannerProvider          string
	Validation               string
	TaskProposalMode         string
	ResolvedTaskProposalMode string
	SelectedTaskID           string
	RepositoryURL            string
	BaseBranch               string
	WorkBranch               string
	PushEnabled              bool
	PushStatus               string
	PushedRef                string
	DryRun                   bool
	ErrorSummary             string
	RiskSummary              string
	Risks                    []string
	Failures                 []string
	NextActions              []string
	SecuritySummary          string
	SecurityDetails          []string
	Invalid                  bool
	ValidationArtifact       string
}

type dashboardManifest struct {
	RunID                    string `json:"run_id"`
	Status                   string `json:"status"`
	StartedAt                string `json:"started_at"`
	FinishedAt               string `json:"finished_at"`
	EndedAt                  string `json:"ended_at"`
	PlannerProvider          string `json:"planner_provider"`
	TaskProposalMode         string `json:"task_proposal_mode"`
	ResolvedTaskProposalMode string `json:"resolved_task_proposal_mode"`
	SelectedTaskID           string `json:"selected_task_id"`
	Repository               struct {
		Enabled          bool   `json:"enabled"`
		SanitizedRepoURL string `json:"sanitized_repo_url"`
		RepoURL          string `json:"repo_url"`
		BaseBranch       string `json:"base_branch"`
		WorkBranch       string `json:"work_branch"`
		PushEnabled      bool   `json:"push_enabled"`
		PushStatus       string `json:"push_status"`
		PushedRef        string `json:"pushed_ref"`
	} `json:"repository"`
	Planner struct {
		Provider string `json:"provider"`
	} `json:"planner"`
	DryRun     bool `json:"dry_run"`
	Validation struct {
		Status         string `json:"status"`
		EvidenceStatus string `json:"evidence_status"`
		Reason         string `json:"reason"`
		Summary        string `json:"summary"`
		SummaryPath    string `json:"summary_path"`
		ResultsPath    string `json:"results_path"`
	} `json:"validation"`
	Commit struct {
		Ran    bool   `json:"ran"`
		Status string `json:"status"`
	} `json:"commit"`
	Artifacts map[string]string       `json:"artifacts"`
	Errors    []string                `json:"errors"`
	Risks     []string                `json:"risks"`
	Security  runpkg.ManifestSecurity `json:"security"`
}

type dashboardManifestLoad struct {
	Manifest dashboardManifest
	Valid    bool
	Error    string
}

type artifactLink struct {
	Path string
}

type runFormData struct {
	PlanPath          string
	PlanPrompt        string
	CWD               string
	RunID             string
	DryRun            bool
	AutoContinue      bool
	MaxTurns          int
	PlanningAgents    int
	TaskProposalMode  string
	TaskProposalModes []string
	RepoURL           string
	RepoDir           string
	BaseBranch        string
	WorkBranch        string
	Push              bool
	PushMode          string
	GitHubTokenEnv    string
	AllowDirty        bool
	OpenAIModel       string
	CodexModel        string
	AllowNoGit        bool
	LocalOnly         bool
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
	if !server.localOnly {
		fmt.Fprintf(cfg.Stdout, "jj: warning: serving on non-local address %s; do this only on a trusted network\n", listener.Addr().String())
	}
	fmt.Fprintf(cfg.Stdout, "jj: serving dashboard at http://%s\n", listener.Addr().String())

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
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if strings.TrimSpace(cfg.RunID) != "" {
		if err := artifact.ValidateRunID(cfg.RunID); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = DefaultAddr
	}
	if !isLocalAddr(cfg.Addr) && !cfg.AddrExplicit && !cfg.HostExplicit {
		return nil, errors.New("external dashboard binding requires explicit --host or --addr")
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		if isUnsafeRequestPath(r.URL.Path, r.URL.EscapedPath()) {
			s.renderError(w, http.StatusForbidden, errors.New("request path is not allowed"))
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
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
		Title:       "jj dashboard",
		CWD:         displayWorkspace,
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
	s.render(w, pageData{
		Title: "start jj run",
		CWD:   displayWorkspace,
		RunForm: &runFormData{
			PlanPath:          defaultPlan,
			CWD:               "",
			DryRun:            true,
			MaxTurns:          10,
			PlanningAgents:    runpkg.DefaultPlanningAgents,
			TaskProposalMode:  string(runpkg.TaskProposalModeAuto),
			TaskProposalModes: runpkg.ValidTaskProposalModeValues(),
			PushMode:          runpkg.DefaultPushMode,
			GitHubTokenEnv:    runpkg.DefaultGitHubTokenEnv,
			LocalOnly:         s.localOnly,
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
	planPrompt := r.FormValue("plan_prompt")
	planPath := ""
	if strings.TrimSpace(planPrompt) == "" {
		if strings.TrimSpace(r.FormValue("plan_path")) == "" {
			s.renderError(w, http.StatusBadRequest, errors.New("plan path or prompt is required"))
			return
		}
		var err error
		planPath, err = s.validatePlanPath(r.FormValue("plan_path"))
		if err != nil {
			s.renderError(w, http.StatusBadRequest, err)
			return
		}
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
	taskProposalMode, err := runpkg.ParseTaskProposalMode(r.FormValue("task_proposal_mode"))
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	runID, runDir, err := s.resolveWebRunID(r.FormValue("run_id"))
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err)
		return
	}
	openAIModel := strings.TrimSpace(r.FormValue("openai_model"))
	codexModel := strings.TrimSpace(r.FormValue("codex_model"))
	repoURL := strings.TrimSpace(r.FormValue("repo"))
	repoDir := strings.TrimSpace(r.FormValue("repo_dir"))
	baseBranch := strings.TrimSpace(r.FormValue("base_branch"))
	workBranch := strings.TrimSpace(r.FormValue("work_branch"))
	pushMode := strings.TrimSpace(r.FormValue("push_mode"))
	githubTokenEnv := strings.TrimSpace(r.FormValue("github_token_env"))
	push := formBool(r, "push")
	allowDirty := formBool(r, "allow_dirty")
	cfg := runpkg.Config{
		PlanPath:                 planPath,
		PlanText:                 planPrompt,
		PlanInputName:            runpkg.DefaultWebPromptInput,
		CWD:                      s.cwd,
		RunID:                    runID,
		DryRun:                   dryRun,
		DryRunExplicit:           true,
		AllowNoGit:               allowNoGit,
		AllowNoGitExplicit:       true,
		PlanningAgents:           planningAgents,
		PlanningAgentsExplicit:   planningAgentsExplicit,
		TaskProposalMode:         taskProposalMode,
		TaskProposalModeExplicit: true,
		RepoURL:                  repoURL,
		RepoURLExplicit:          repoURL != "",
		RepoDir:                  repoDir,
		RepoDirExplicit:          repoDir != "",
		BaseBranch:               baseBranch,
		BaseBranchExplicit:       baseBranch != "",
		WorkBranch:               workBranch,
		WorkBranchExplicit:       workBranch != "",
		Push:                     push,
		PushExplicit:             true,
		PushMode:                 pushMode,
		PushModeExplicit:         pushMode != "",
		GitHubTokenEnv:           githubTokenEnv,
		GitHubTokenEnvExplicit:   githubTokenEnv != "",
		RepoAllowDirty:           allowDirty,
		RepoAllowDirtyExplicit:   true,
		OpenAIModel:              openAIModel,
		OpenAIModelExplicit:      openAIModel != "",
		CodexModel:               codexModel,
		CodexModelExplicit:       codexModel != "",
		ConfigSearchDir:          s.cwd,
		Stdout:                   io.Discard,
		Stderr:                   io.Discard,
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
		cfg.RunID = runpkg.TurnRunID(baseCfg.RunID, turn)
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
			runDir, dirErr := s.trustedRunDir(cfg.RunID, result.RunDir)
			if dirErr == nil {
				webRun.setCurrentTurnRunDir(runDir)
			} else {
				webRun.appendLog("jj web: ignored unsafe reported run directory")
			}
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
		if strings.EqualFold(outcome.ValidationStatus, "failed") || outcome.Status == runpkg.StatusFailed || outcome.Status == "cancelled" || strings.HasSuffix(outcome.Status, "_failed") {
			webRun.setLoopStatus("failed", "failed", outcome.Error, "turn failed")
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
	data, err := readRunFile(runDir, "manifest.json")
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
	ValidationStatus string
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
	data, err := readRunFile(runDir, "manifest.json")
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	var manifest struct {
		Status       string   `json:"status"`
		ErrorSummary string   `json:"error_summary"`
		Errors       []string `json:"errors"`
		Validation   struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"validation"`
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
	outcome.ValidationStatus = manifest.Validation.Status
	outcome.Error = manifest.ErrorSummary
	if outcome.Error == "" && len(manifest.Errors) > 0 {
		outcome.Error = manifest.Errors[0]
	}
	if outcome.Error == "" {
		outcome.Error = manifest.Validation.Error
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
		CWD:    displayWorkspace,
		WebRun: &view,
	})
}

func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("id")
	view, err := s.webRunView(runID)
	if err != nil {
		http.Error(w, sanitizeDashboardText(err.Error(), security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		http.Error(w, sanitizeDashboardText(err.Error(), security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}), http.StatusInternalServerError)
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
	if !isMarkdown(rel) && strings.ToLower(filepath.Ext(rel)) != ".json" {
		s.renderError(w, http.StatusBadRequest, errors.New("only allowlisted markdown and json state files are supported"))
		return
	}
	if !isAllowedDocPath(rel) {
		s.renderError(w, http.StatusForbidden, errors.New("state path is not allowed"))
		return
	}
	path, err := safeJoinProject(s.cwd, rel)
	if err != nil {
		s.renderError(w, http.StatusForbidden, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.renderError(w, http.StatusNotFound, errors.New("state file unavailable"))
		return
	}
	content, rendered := presentContent(rel, data, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace})
	s.render(w, pageData{
		Title:    rel,
		CWD:      displayWorkspace,
		Path:     filepath.ToSlash(rel),
		Content:  content,
		Rendered: rendered,
	})
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
		CWD:      displayWorkspace,
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
		CWD:       displayWorkspace,
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
	data, err := readRunFile(runDir, "manifest.json")
	if err != nil {
		s.renderError(w, http.StatusNotFound, errors.New("manifest unavailable"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"run_id": runID,
			"status": "unknown",
			"error":  "manifest is malformed",
		})
		return
	}
	sanitized := sanitizeDashboardValue(
		security.RedactJSONValue(decoded),
		security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace},
		security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + runID},
	)
	redacted, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, errors.New("manifest unavailable"))
		return
	}
	redacted = append(redacted, '\n')
	_, _ = w.Write(redacted)
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
		s.renderError(w, http.StatusForbidden, err)
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
		s.renderError(w, http.StatusForbidden, errors.New("artifact path is not listed in manifest"))
		return
	}
	path, err := safeJoin(runDir, rel)
	if err != nil {
		s.renderError(w, http.StatusForbidden, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.renderError(w, http.StatusNotFound, errors.New("artifact unavailable"))
		return
	}
	content, rendered := presentContent(
		rel,
		data,
		security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace},
		security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + runID},
	)
	s.render(w, pageData{
		Title:    runID + "/" + rel,
		CWD:      displayWorkspace,
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
	return security.SafeJoinNoSymlinks(s.cwd, filepath.ToSlash(filepath.Join(".jj", "runs", runID)), security.PathPolicy{AllowHidden: true})
}

func (s *Server) trustedRunDir(runID, reported string) (string, error) {
	expected, err := s.runDir(runID)
	if err != nil {
		return "", err
	}
	reported = strings.TrimSpace(reported)
	if reported == "" {
		return expected, nil
	}
	reportedAbs, err := filepath.Abs(reported)
	if err != nil {
		return "", err
	}
	expectedAbs, err := filepath.Abs(expected)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(reportedAbs); err == nil {
		reportedAbs = resolved
	}
	if resolved, err := filepath.EvalSymlinks(expectedAbs); err == nil {
		expectedAbs = resolved
	}
	if filepath.Clean(reportedAbs) != filepath.Clean(expectedAbs) {
		return "", errors.New("reported run directory is outside the expected run root")
	}
	return expected, nil
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
		return "", "", fmt.Errorf("run directory already exists for run id: %s", runID)
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
		return "", fmt.Errorf("run directory already exists for run id: %s", runID)
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

func (s *Server) workspaceReadiness() []readinessItem {
	items := []readinessItem{
		{Label: "Plan", Path: "plan.md"},
		{Label: "README", Path: "README.md"},
		{Label: "SPEC", Path: runpkg.DefaultSpecStatePath},
		{Label: "TASK", Path: runpkg.DefaultTasksStatePath},
	}
	for i := range items {
		path, err := safeJoinProject(s.cwd, items[i].Path)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		items[i].Ready = err == nil && !info.IsDir()
	}
	return items
}

func (s *Server) taskSummary() string {
	path, err := safeJoinProject(s.cwd, runpkg.DefaultTasksStatePath)
	if err != nil {
		return ".jj/tasks.json is missing."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ".jj/tasks.json is missing."
	}
	var state struct {
		ActiveTaskID *string `json:"active_task_id"`
		Tasks        []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Mode   string `json:"mode"`
			Status string `json:"status"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return ".jj/tasks.json is unreadable."
	}
	if len(state.Tasks) == 0 {
		return ".jj/tasks.json has no tasks."
	}
	counts := map[string]int{}
	for _, task := range state.Tasks {
		counts[task.Status]++
	}
	current := state.Tasks[0]
	if state.ActiveTaskID != nil {
		for _, task := range state.Tasks {
			if task.ID == *state.ActiveTaskID {
				current = task
				break
			}
		}
	}
	return fmt.Sprintf("Tasks: %d total, %d queued, %d in progress, %d done. Current: %s %s (%s).", len(state.Tasks), counts["queued"], counts["in_progress"]+counts["active"], counts["done"], sanitizeDashboardText(current.ID, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}), sanitizeDashboardText(current.Title, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}), sanitizeDashboardText(current.Mode, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}))
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
	return runpkg.BuildContinuationContext(s.cwd, previousRunID)
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

func (s *Server) appendContinuationRel(b *strings.Builder, title, root, rel string) {
	path, err := safeJoin(root, rel)
	if err != nil {
		return
	}
	s.appendContinuationFile(b, title, path)
}

func truncateDisplay(s string, max int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= max {
		return s
	}
	cut := 0
	for i := range s {
		if i > max {
			break
		}
		cut = i
	}
	return s[:cut] + "\n...[truncated]..."
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
		return "", errors.New("plan path is not allowed")
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
	docs := make([]docLink, 0, len(allowedProjectDocPaths))
	for _, rel := range allowedProjectDocPaths {
		path, err := safeJoinProject(s.cwd, rel)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		docs = append(docs, docLink{Path: rel})
	}
	return docs, nil
}

func (s *Server) discoverRuns() ([]runLink, error) {
	runsDir, err := security.SafeJoinNoSymlinks(s.cwd, ".jj/runs", security.PathPolicy{AllowHidden: true})
	if err != nil {
		return nil, err
	}
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
		runDir := filepath.Join(runsDir, entry.Name())
		loaded := loadDashboardManifest(entry.Name(), runDir)
		run := runLink{ID: entry.Name()}
		if !loaded.Valid {
			run.Invalid = true
			run.Status = "unavailable"
			run.ErrorSummary = unavailableRunError(loaded.Error)
			run.Failures = []string{run.ErrorSummary}
			runs = append(runs, run)
			continue
		}
		manifest := loaded.Manifest
		safeText := func(value string) string {
			return sanitizeDashboardText(value, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}, security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + entry.Name()})
		}
		run.Status = safeText(manifest.Status)
		run.StartedAt = safeText(manifest.StartedAt)
		run.FinishedAt = safeText(manifest.FinishedAt)
		if run.FinishedAt == "" {
			run.FinishedAt = safeText(manifest.EndedAt)
		}
		run.PlannerProvider = safeText(manifest.PlannerProvider)
		if run.PlannerProvider == "" {
			run.PlannerProvider = safeText(manifest.Planner.Provider)
		}
		run.TaskProposalMode = safeText(manifest.TaskProposalMode)
		run.ResolvedTaskProposalMode = safeText(manifest.ResolvedTaskProposalMode)
		run.SelectedTaskID = safeText(manifest.SelectedTaskID)
		if manifest.Repository.Enabled {
			run.RepositoryURL = safeText(firstNonEmpty(manifest.Repository.SanitizedRepoURL, manifest.Repository.RepoURL))
			run.BaseBranch = safeText(manifest.Repository.BaseBranch)
			run.WorkBranch = safeText(manifest.Repository.WorkBranch)
			run.PushEnabled = manifest.Repository.PushEnabled
			run.PushStatus = safeText(manifest.Repository.PushStatus)
			run.PushedRef = safeText(manifest.Repository.PushedRef)
		}
		run.DryRun = manifest.DryRun
		run.Validation = dashboardValidationStatus(manifest)
		run.ValidationArtifact = listedArtifactPath(manifest.Artifacts, "validation_summary", "validation_results")
		if run.ValidationArtifact == "" {
			run.ValidationArtifact = artifactPathByValue(manifest.Artifacts, manifest.Validation.SummaryPath)
		}
		if len(manifest.Errors) > 0 {
			run.ErrorSummary = safeText(manifest.Errors[0])
			run.Failures = sanitizeDashboardList(manifest.Errors, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}, security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + entry.Name()})
		}
		if len(manifest.Risks) > 0 {
			run.RiskSummary = safeText(manifest.Risks[0])
			run.Risks = sanitizeDashboardList(manifest.Risks, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}, security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + entry.Name()})
		}
		run.SecuritySummary, run.SecurityDetails = dashboardSecurityDiagnostics(manifest.Security)
		if isHistoricalCommitSuccess(manifest) {
			note := "Legacy commit-success metadata is historical; current jj runs do not auto-commit by default."
			run.Risks = appendUnique(run.Risks, note)
			if run.RiskSummary == "" {
				run.RiskSummary = note
			}
			run.NextActions = appendUnique(run.NextActions, "Review working tree changes; do not infer current auto-commit behavior from this legacy manifest.")
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

func unavailableRunError(reason string) string {
	reason = strings.TrimSpace(secrets.Redact(reason))
	if reason == "" {
		reason = "manifest unavailable"
	}
	return reason + "; artifact links unavailable because this run lacks a trusted top-level artifacts map or trusted manifest."
}

func isHistoricalCommitSuccess(manifest dashboardManifest) bool {
	return manifest.Commit.Ran && strings.EqualFold(strings.TrimSpace(manifest.Commit.Status), "success")
}

func dashboardValidationStatus(manifest dashboardManifest) string {
	status := strings.TrimSpace(secrets.Redact(manifest.Validation.Status))
	evidence := strings.TrimSpace(secrets.Redact(manifest.Validation.EvidenceStatus))
	reason := strings.TrimSpace(secrets.Redact(manifest.Validation.Reason))
	if status == "" {
		status = evidence
	}
	if status == "" {
		status = reason
	}
	if status == "" {
		return ""
	}
	if evidence != "" && !strings.EqualFold(status, evidence) {
		status += " (" + evidence + ")"
	}
	return status
}

func dashboardSecurityDiagnostics(securityMeta runpkg.ManifestSecurity) (string, []string) {
	diag := securityMeta.Diagnostics
	if strings.TrimSpace(diag.Version) == "" && !securityMeta.RedactionApplied && !securityMeta.WorkspaceGuardrailsApplied {
		return "", nil
	}
	commandStatus := dashboardCategory(diag.CommandSanitizationStatus, "unknown")
	if commandStatus == "unknown" && diag.CommandMetadataSanitized {
		commandStatus = "sanitized"
	}
	parityStatus := dashboardCategory(diag.DryRunParityStatus, "unknown")
	deniedCount := diag.DeniedPathCount
	if deniedCount < 0 {
		deniedCount = 0
	}
	redactionCount := securityMeta.RedactionCount
	if redactionCount < 0 {
		redactionCount = 0
	}
	summary := fmt.Sprintf(
		"security redactions %d · denied paths %d · command metadata %s · dry-run parity %s",
		redactionCount,
		deniedCount,
		commandStatus,
		parityStatus,
	)
	var details []string
	if roots := dashboardCategoryList(diag.RootLabels, "root"); len(roots) > 0 {
		details = append(details, "roots "+strings.Join(roots, ", "))
	}
	if categories := dashboardCategoryList(diag.DeniedPathCategories, "path_denied"); len(categories) > 0 {
		details = append(details, "denied categories "+strings.Join(categories, ", "))
	}
	if categories := dashboardCategoryList(diag.FailureCategories, "security_failure"); len(categories) > 0 {
		details = append(details, "failure categories "+strings.Join(categories, ", "))
	}
	return summary, details
}

func dashboardCategoryList(items []string, fallback string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		category := dashboardCategory(item, fallback)
		if category == "" || seen[category] {
			continue
		}
		seen[category] = true
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

func dashboardCategory(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(secrets.Redact(value)))
	if value == "" || strings.Contains(value, security.RedactionMarker) {
		return fallback
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if r == '-' || r == '_' {
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return fallback
	}
	return out
}

func loadDashboardManifest(runID, runDir string) dashboardManifestLoad {
	data, err := readRunFile(runDir, "manifest.json")
	if err != nil {
		return dashboardManifestLoad{Valid: false, Error: "manifest unavailable"}
	}
	var manifest dashboardManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return dashboardManifestLoad{Valid: false, Error: "manifest is malformed"}
	}
	if strings.TrimSpace(manifest.RunID) == "" {
		return dashboardManifestLoad{Valid: false, Error: "manifest is incomplete: missing run_id"}
	}
	if manifest.RunID != runID {
		return dashboardManifestLoad{Valid: false, Error: "manifest is incomplete: run_id mismatch"}
	}
	if strings.TrimSpace(manifest.Status) == "" {
		return dashboardManifestLoad{Valid: false, Error: "manifest is incomplete: missing status"}
	}
	if manifest.Artifacts == nil {
		return dashboardManifestLoad{Valid: false, Error: "manifest is incomplete: missing artifacts"}
	}
	return dashboardManifestLoad{Manifest: manifest, Valid: true}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func listedArtifactPath(artifacts map[string]string, keys ...string) string {
	for _, key := range keys {
		if clean, err := cleanAllowedArtifactPath(artifacts[key]); err == nil {
			return clean
		}
	}
	return ""
}

func artifactPathByValue(artifacts map[string]string, target string) string {
	for _, raw := range artifacts {
		clean, err := cleanAllowedArtifactPath(raw)
		if err == nil && clean == target {
			return clean
		}
	}
	return ""
}

func sanitizeDashboardList(items []string, roots ...security.CommandPathRoot) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(sanitizeDashboardText(item, roots...))
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
	case "snapshots/spec.after.json":
		return 0
	case "snapshots/tasks.after.json":
		return 1
	case "manifest.json":
		return 2
	case "validation/summary.md":
		return 3
	case "validation/results.json":
		return 4
	default:
		return 10
	}
}

func isAllowedDocPath(rel string) bool {
	clean, err := cleanAllowedProjectPath(rel)
	if err != nil {
		return false
	}
	return isProjectDocPath(clean)
}

func isAllowedArtifactPath(rel string) bool {
	_, err := cleanAllowedArtifactPath(rel)
	return err == nil
}

func cleanAllowedArtifactPath(rel string) (string, error) {
	clean, err := cleanAllowedRelativePath(rel)
	if err != nil {
		return "", errors.New("artifact path is not allowed")
	}
	return clean, nil
}

func cleanAllowedRelativePath(rel string) (string, error) {
	return security.CleanRelativePath(rel, security.PathPolicy{})
}

func cleanAllowedProjectPath(rel string) (string, error) {
	return security.CleanRelativePath(rel, security.PathPolicy{AllowHidden: strings.HasPrefix(rel, ".")})
}

func isProjectDocPath(rel string) bool {
	switch rel {
	case "README.md", "plan.md", "docs/SPEC.md", "docs/TASK.md", runpkg.DefaultSpecStatePath, runpkg.DefaultTasksStatePath:
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
	runID := filepath.Base(runDir)
	loaded := loadDashboardManifest(runID, runDir)
	if !loaded.Valid {
		return nil, errors.New(loaded.Error)
	}
	manifest := loaded.Manifest
	artifacts := map[string]struct{}{}
	add := func(raw string) {
		if clean, err := cleanAllowedArtifactPath(raw); err == nil {
			artifacts[clean] = struct{}{}
		}
	}
	for _, path := range manifest.Artifacts {
		add(path)
	}
	return artifacts, nil
}

func safeJoin(root, rel string) (string, error) {
	return security.SafeJoinNoSymlinks(root, rel, security.PathPolicy{})
}

func safeJoinProject(root, rel string) (string, error) {
	return security.SafeJoinNoSymlinks(root, rel, security.PathPolicy{AllowHidden: strings.HasPrefix(rel, ".")})
}

func readRunFile(runDir, rel string) ([]byte, error) {
	path, err := safeJoin(runDir, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
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

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; connect-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'")
}

func (s *Server) renderError(w http.ResponseWriter, status int, err error) {
	s.renderStatus(w, status, pageData{Title: "error", Error: sanitizeDashboardText(err.Error(), security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace})})
}

func (s *Server) renderErrorData(w http.ResponseWriter, status int, data pageData) {
	data.CWD = ""
	data.Error = sanitizeDashboardText(data.Error, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace})
	s.renderStatus(w, status, data)
}

func (s *Server) render(w http.ResponseWriter, data pageData) {
	s.renderStatus(w, http.StatusOK, data)
}

func (s *Server) renderStatus(w http.ResponseWriter, status int, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pageTemplate.Execute(w, data); err != nil {
		http.Error(w, sanitizeDashboardText(err.Error(), security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}), http.StatusInternalServerError)
	}
}

func presentContent(path string, data []byte, roots ...security.CommandPathRoot) (string, template.HTML) {
	redacted := security.RedactContent(path, data)
	if strings.EqualFold(filepath.Ext(path), ".json") {
		var decoded any
		if err := json.Unmarshal(redacted, &decoded); err == nil {
			sanitized := sanitizeDashboardValue(decoded, roots...)
			if encoded, err := json.MarshalIndent(sanitized, "", "  "); err == nil {
				redacted = append(encoded, '\n')
			}
		}
	}
	content := sanitizeDashboardText(string(redacted), roots...)
	if isMarkdown(path) {
		return "", renderMarkdown(content)
	}
	return content, ""
}

func sanitizeDashboardValue(value any, roots ...security.CommandPathRoot) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = sanitizeDashboardValue(child, roots...)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = sanitizeDashboardValue(child, roots...)
		}
		return out
	case string:
		return sanitizeDashboardText(v, roots...)
	default:
		return value
	}
}

func sanitizeDashboardText(text string, roots ...security.CommandPathRoot) string {
	text = security.SanitizeDisplayString(text, roots...)
	return redactDashboardLogPaths(text)
}

func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func isUnsafeRequestPath(rawPath, escapedPath string) bool {
	if strings.ContainsRune(rawPath, 0) || strings.Contains(rawPath, `\`) {
		return true
	}
	for _, part := range strings.Split(rawPath, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	lowerEscaped := strings.ToLower(escapedPath)
	for _, token := range []string{"%00", "%2e", "%2f", "%5c"} {
		if strings.Contains(lowerEscaped, token) {
			return true
		}
	}
	return false
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
    input, textarea, select { padding: 6px 8px; border: 1px solid color-mix(in srgb, CanvasText 22%, transparent); border-radius: 4px; background: Canvas; color: CanvasText; font: inherit; }
    input, select { min-height: 34px; }
    textarea { min-height: 150px; resize: vertical; line-height: 1.45; }
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
        <label>prompt
          <textarea name="plan_prompt" placeholder="Paste a one-off jj plan prompt here.">{{.RunForm.PlanPrompt}}</textarea>
        </label>
        <label>plan path
          <input name="plan_path" value="{{.RunForm.PlanPath}}" placeholder="plan.md">
        </label>
        <label>cwd
          <input name="cwd" value="{{.RunForm.CWD}}" placeholder="[workspace]">
        </label>
        <label>run id
          <input name="run_id" value="{{.RunForm.RunID}}" placeholder="auto">
        </label>
        <label>planning agents
          <input name="planning_agents" value="{{.RunForm.PlanningAgents}}">
        </label>
        <label>task proposal mode
          <select name="task_proposal_mode">
            {{range .RunForm.TaskProposalModes}}
              <option value="{{.}}" {{if eq . $.RunForm.TaskProposalMode}}selected{{end}}>{{.}}</option>
            {{end}}
          </select>
        </label>
        <label>repository URL
          <input name="repo" value="{{.RunForm.RepoURL}}" placeholder="https://github.com/org/repo.git">
        </label>
        <label>repository directory
          <input name="repo_dir" value="{{.RunForm.RepoDir}}" placeholder="auto">
        </label>
        <label>base branch
          <input name="base_branch" value="{{.RunForm.BaseBranch}}" placeholder="auto">
        </label>
        <label>work branch
          <input name="work_branch" value="{{.RunForm.WorkBranch}}" placeholder="jj/run-&lt;run-id&gt;">
        </label>
        <label>push mode
          <input name="push_mode" value="{{.RunForm.PushMode}}" placeholder="branch">
        </label>
        <label>GitHub token env
          <input name="github_token_env" value="{{.RunForm.GitHubTokenEnv}}" placeholder="JJ_GITHUB_TOKEN">
        </label>
        <label>OpenAI model
          <input name="openai_model" value="{{.RunForm.OpenAIModel}}" placeholder="configured default">
        </label>
        <label>Codex model
          <input name="codex_model" value="{{.RunForm.CodexModel}}" placeholder="Codex CLI default">
        </label>
	        <p class="muted">Full runs write .jj/spec.json and .jj/tasks.json. Dry-runs keep planned state snapshots under .jj/runs.</p>
        <label class="check"><input type="checkbox" name="dry_run" value="true" {{if .RunForm.DryRun}}checked{{end}}> dry-run</label>
        <label class="check"><input type="checkbox" name="auto_continue" value="true" {{if .RunForm.AutoContinue}}checked{{end}}> auto continue turns</label>
        <label>max turns
          <input name="max_turns" value="{{.RunForm.MaxTurns}}">
        </label>
        <label class="check"><input type="checkbox" name="allow_no_git" value="true" {{if .RunForm.AllowNoGit}}checked{{end}}> allow no git</label>
        <label class="check"><input type="checkbox" name="allow_dirty" value="true" {{if .RunForm.AllowDirty}}checked{{end}}> allow dirty repo</label>
        <label class="check"><input type="checkbox" name="push" value="true" {{if .RunForm.Push}}checked{{end}}> push repository branch</label>
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
        <li>{{if .Invalid}}<strong>{{.ID}}</strong>{{else}}<a href="/runs/{{q .ID}}">{{.ID}}</a>{{end}} <span class="muted">{{.Status}} {{.StartedAt}} {{.PlannerProvider}}{{if .Validation}} · validation {{.Validation}}{{end}}{{if .TaskProposalMode}} · mode {{.TaskProposalMode}}{{if .ResolvedTaskProposalMode}} → {{.ResolvedTaskProposalMode}}{{end}}{{end}}{{if .SecuritySummary}} · {{.SecuritySummary}}{{end}}</span>{{if .ErrorSummary}} <span class="error">{{.ErrorSummary}}</span>{{end}}</li>
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
	          {{if .Invalid}}
	            <p><strong>{{.ID}}</strong> <span class="muted">{{.Status}} {{.StartedAt}}</span></p>
	            <p class="error">{{.ErrorSummary}}</p>
	          {{else}}
	            <p><a href="/run?id={{q .ID}}">{{.ID}}</a> <span class="muted">{{.Status}} {{.StartedAt}}</span></p>
	            <p class="muted">provider {{.PlannerProvider}} · dry-run {{.DryRun}}{{if .Validation}} · validation {{.Validation}}{{end}}</p>
	            {{if .TaskProposalMode}}<p class="muted">Task Proposal Mode: {{.TaskProposalMode}}{{if .ResolvedTaskProposalMode}} · Resolved Mode: {{.ResolvedTaskProposalMode}}{{end}}{{if .SelectedTaskID}} · Recommended Next Task: {{.SelectedTaskID}}{{end}}</p>{{end}}
	            {{if .RepositoryURL}}<p class="muted">Repository: {{.RepositoryURL}} · Base Branch: {{.BaseBranch}} · Work Branch: {{.WorkBranch}} · Push Enabled: {{.PushEnabled}} · Push Status: {{.PushStatus}}{{if .PushedRef}} · Last Pushed Ref: {{.PushedRef}}{{end}}</p>{{end}}
	            {{if .SecuritySummary}}<p class="muted">{{.SecuritySummary}}{{range .SecurityDetails}} · {{.}}{{end}}</p>{{end}}
	            <p><a href="/runs/{{q .ID}}/manifest">Raw manifest</a>{{if .ValidationArtifact}} · <a href="/artifact?run={{q .ID}}&path={{q .ValidationArtifact}}">Validation artifact</a>{{end}}</p>
	          {{end}}
	          {{if and .ErrorSummary (not .Invalid)}}<p class="error">{{.ErrorSummary}}</p>{{end}}
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
	          <a class="button primary" href="/run/new">Start Web Run</a>
	          {{if .DefaultPlan}}<a class="button" href="/doc?path={{q .DefaultPlan}}">Open Plan</a>{{end}}
	          <a class="button" href="/doc?path=.jj/tasks.json">Open Tasks</a>
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
          <h2>State Files</h2>
          <ul>
          {{range .Docs}}
            <li><a href="/doc?path={{q .Path}}">{{.Path}}</a></li>
          {{else}}
            <li class="muted">No allowlisted state files found.</li>
          {{end}}
          </ul>
        </section>
	        <section>
	          <h2>Runs</h2>
	          <ul>
	          {{range .Runs}}
	            <li>{{if .Invalid}}<strong>{{.ID}}</strong>{{else}}<a href="/run?id={{q .ID}}">{{.ID}}</a>{{end}} <span class="muted">{{.Status}} {{.StartedAt}} {{.PlannerProvider}}{{if .Validation}} · validation {{.Validation}}{{end}}{{if .TaskProposalMode}} · mode {{.TaskProposalMode}}{{if .ResolvedTaskProposalMode}} → {{.ResolvedTaskProposalMode}}{{end}}{{end}}{{if .SecuritySummary}} · {{.SecuritySummary}}{{end}}</span>{{if not .Invalid}} <a href="/runs/{{q .ID}}/manifest">manifest</a>{{end}}{{if .ErrorSummary}} <span class="error">{{.ErrorSummary}}</span>{{else if .RiskSummary}} <span class="muted">{{.RiskSummary}}</span>{{end}}</li>
	          {{else}}
	            <li class="muted">No jj runs found. Try <code>jj run plan.md --dry-run</code>.</li>
	          {{end}}
	          </ul>
	        </section>
	        <section>
	          <h2>Next Actions</h2>
	          <ul>
	            <li><a href="/run/new">Start Web Run</a></li>
	            {{if .DefaultPlan}}<li><a href="/doc?path={{q .DefaultPlan}}">Review plan</a></li>{{end}}
	            {{if .Runs}}{{with index .Runs 0}}{{range .NextActions}}<li>{{.}}</li>{{end}}{{end}}{{else}}<li><code>jj run plan.md --dry-run</code></li>{{end}}
	            <li><a href="/doc?path=.jj/tasks.json">Open Tasks</a></li>
	            <li><a href="/doc?path=.jj/spec.json">Open SPEC</a></li>
	          </ul>
	        </section>
	      </div>
    {{end}}
  </main>
</body>
</html>`))
