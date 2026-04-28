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
	CompareURL               string
}

type runHistoryFilters struct {
	Status          string
	DryRun          string
	PlannerProvider string
	Evaluation      string
	Query           string
	Notice          string
	HasActive       bool
}

type runHistoryFilterOptions struct {
	Statuses         []string
	DryRunModes      []runHistoryFilterOption
	PlannerProviders []string
	Evaluations      []string
}

type runHistoryFilterOption struct {
	Value string
	Label string
}

type dashboardManifest struct {
	RunID                    string `json:"run_id"`
	Status                   string `json:"status"`
	StartedAt                string `json:"started_at"`
	FinishedAt               string `json:"finished_at"`
	EndedAt                  string `json:"ended_at"`
	DurationMS               int64  `json:"duration_ms"`
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
		Provider  string            `json:"provider"`
		Model     string            `json:"model"`
		Artifacts map[string]string `json:"artifacts"`
	} `json:"planner"`
	DryRun     bool                      `json:"dry_run"`
	Workspace  runpkg.ManifestWorkspace  `json:"workspace"`
	Codex      runpkg.ManifestCodex      `json:"codex"`
	Validation runpkg.ManifestValidation `json:"validation"`
	Commit     struct {
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

type runInspection struct {
	ID              string
	rawID           string
	RunDir          string
	Roots           []security.CommandPathRoot
	ValidID         bool
	State           string
	HTTPStatus      int
	ManifestState   string
	Error           string
	TrustedManifest bool
	ManifestLoaded  bool
	manifest        dashboardManifest
	Detail          runDetail
	History         runLink
	AuditSecurity   runAuditSecurity
}

type artifactLink struct {
	Path string
}

type runDetail struct {
	ID                       string
	Status                   string
	StartedAt                string
	FinishedAt               string
	Duration                 string
	DryRun                   bool
	PlannerProvider          string
	PlannerModel             string
	TaskProposalMode         string
	ResolvedTaskProposalMode string
	SelectedTaskID           string
	RepositorySummary        string
	ManifestState            string
	Error                    string
	Docs                     []runDetailLink
	Artifacts                []runArtifactStatus
	ArtifactNote             string
	Validation               runValidationDetail
	Codex                    runCodexDetail
	Commands                 []runCommandDetail
	SecuritySummary          string
	SecurityDetails          []string
	NextActions              []string
}

type runAuditExport struct {
	SchemaVersion string             `json:"schema_version"`
	State         string             `json:"state"`
	RunID         string             `json:"run_id,omitempty"`
	ManifestState string             `json:"manifest_state"`
	Error         string             `json:"error,omitempty"`
	Status        string             `json:"status"`
	StartedAt     string             `json:"started_at,omitempty"`
	FinishedAt    string             `json:"finished_at,omitempty"`
	Duration      string             `json:"duration,omitempty"`
	DryRun        bool               `json:"dry_run"`
	Planner       runAuditPlanner    `json:"planner"`
	GeneratedDocs []runAuditLink     `json:"generated_docs"`
	Artifacts     []runAuditLink     `json:"artifacts"`
	Evaluation    runAuditEvaluation `json:"evaluation"`
	Codex         runAuditCodex      `json:"codex"`
	Commands      []runAuditCommand  `json:"commands"`
	Security      runAuditSecurity   `json:"security"`
	NextActions   []string           `json:"next_actions,omitempty"`
}

type runAuditPlanner struct {
	Provider                 string `json:"provider,omitempty"`
	Model                    string `json:"model,omitempty"`
	TaskProposalMode         string `json:"task_proposal_mode,omitempty"`
	ResolvedTaskProposalMode string `json:"resolved_task_proposal_mode,omitempty"`
	SelectedTaskID           string `json:"selected_task_id,omitempty"`
}

type runAuditLink struct {
	Label     string `json:"label,omitempty"`
	Path      string `json:"path,omitempty"`
	URL       string `json:"url,omitempty"`
	Available bool   `json:"available"`
	Status    string `json:"status"`
}

type runAuditEvaluation struct {
	Status          string        `json:"status"`
	EvidenceStatus  string        `json:"evidence_status"`
	Reason          string        `json:"reason,omitempty"`
	Summary         string        `json:"summary,omitempty"`
	Results         *runAuditLink `json:"results,omitempty"`
	SummaryArtifact *runAuditLink `json:"summary_artifact,omitempty"`
	CommandCount    int           `json:"command_count"`
	PassedCount     int           `json:"passed_count"`
	FailedCount     int           `json:"failed_count"`
}

type runAuditCodex struct {
	Ran             bool          `json:"ran"`
	Skipped         bool          `json:"skipped"`
	Status          string        `json:"status"`
	Model           string        `json:"model,omitempty"`
	ExitCode        int           `json:"exit_code"`
	Duration        string        `json:"duration,omitempty"`
	Events          *runAuditLink `json:"events,omitempty"`
	SummaryArtifact *runAuditLink `json:"summary_artifact,omitempty"`
	Exit            *runAuditLink `json:"exit,omitempty"`
	Error           string        `json:"error,omitempty"`
}

type runAuditCommand struct {
	Source   string        `json:"source,omitempty"`
	Label    string        `json:"label,omitempty"`
	Name     string        `json:"name,omitempty"`
	Provider string        `json:"provider,omitempty"`
	Model    string        `json:"model,omitempty"`
	CWD      string        `json:"cwd,omitempty"`
	RunID    string        `json:"run_id,omitempty"`
	Argv     []string      `json:"argv,omitempty"`
	Status   string        `json:"status"`
	ExitCode int           `json:"exit_code"`
	Duration string        `json:"duration,omitempty"`
	Stdout   *runAuditLink `json:"stdout,omitempty"`
	Stderr   *runAuditLink `json:"stderr,omitempty"`
	Error    string        `json:"error,omitempty"`
	Note     string        `json:"note,omitempty"`
}

type runAuditSecurity struct {
	Available                  bool                   `json:"available"`
	Summary                    string                 `json:"summary"`
	Details                    []string               `json:"details,omitempty"`
	RedactionApplied           bool                   `json:"redaction_applied"`
	WorkspaceGuardrailsApplied bool                   `json:"workspace_guardrails_applied"`
	RedactionCount             int64                  `json:"redaction_count"`
	SecretMaterialPresent      bool                   `json:"secret_material_present"`
	RootLabels                 []string               `json:"root_labels,omitempty"`
	GuardedRoots               []runAuditSecurityRoot `json:"guarded_roots,omitempty"`
	DeniedPathCount            int                    `json:"denied_path_count"`
	DeniedPathCategories       []string               `json:"denied_path_categories,omitempty"`
	DeniedPathCategoryCounts   map[string]int         `json:"denied_path_category_counts,omitempty"`
	FailureCategories          []string               `json:"failure_categories,omitempty"`
	FailureCategoryCounts      map[string]int         `json:"failure_category_counts,omitempty"`
	CommandRecordCount         int                    `json:"command_record_count"`
	CommandMetadataSanitized   bool                   `json:"command_metadata_sanitized"`
	CommandArgvSanitized       bool                   `json:"command_argv_sanitized"`
	CommandCWDLabel            string                 `json:"command_cwd_label,omitempty"`
	CommandSanitizationStatus  string                 `json:"command_sanitization_status,omitempty"`
	RawCommandTextPersisted    bool                   `json:"raw_command_text_persisted"`
	RawEnvironmentPersisted    bool                   `json:"raw_environment_persisted"`
	DryRunParityApplied        bool                   `json:"dry_run_parity_applied"`
	DryRunParityStatus         string                 `json:"dry_run_parity_status,omitempty"`
}

type runAuditSecurityRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type runCompare struct {
	Sides  []runCompareSide
	Notice string
}

type runCompareSide struct {
	Label                    string
	ID                       string
	State                    string
	ManifestState            string
	Error                    string
	Status                   string
	StartedAt                string
	FinishedAt               string
	Duration                 string
	DryRun                   bool
	PlannerProvider          string
	PlannerModel             string
	TaskProposalMode         string
	ResolvedTaskProposalMode string
	SelectedTaskID           string
	Docs                     []runDetailLink
	Artifacts                []runArtifactStatus
	Validation               runValidationDetail
	Codex                    runCodexDetail
	Commands                 []runCommandDetail
	SecuritySummary          string
	SecurityDetails          []string
	validID                  bool
}

type runDetailLink struct {
	Label     string
	Path      string
	URL       string
	Available bool
	Status    string
}

type runArtifactStatus struct {
	Path      string
	URL       string
	Available bool
	Status    string
}

type runValidationDetail struct {
	Status         string
	EvidenceStatus string
	Reason         string
	Summary        string
	ResultsPath    string
	ResultsURL     string
	SummaryPath    string
	SummaryURL     string
	CommandCount   int
	PassedCount    int
	FailedCount    int
}

type runCodexDetail struct {
	Ran         bool
	Skipped     bool
	Status      string
	Model       string
	ExitCode    int
	Duration    string
	EventsPath  string
	EventsURL   string
	SummaryPath string
	SummaryURL  string
	ExitPath    string
	ExitURL     string
	Error       string
}

type runCommandDetail struct {
	Source     string
	Label      string
	Name       string
	Provider   string
	Model      string
	CWD        string
	RunID      string
	Argv       []string
	Status     string
	ExitCode   int
	Duration   string
	StdoutPath string
	StdoutURL  string
	StderrPath string
	StderrURL  string
	Error      string
	Note       string
}

type commandRecord struct {
	Provider   string   `json:"provider"`
	Name       string   `json:"name"`
	Model      string   `json:"model"`
	CWD        string   `json:"cwd"`
	RunID      string   `json:"run_id"`
	Argv       []string `json:"argv"`
	Status     string   `json:"status"`
	ExitCode   int      `json:"exit_code"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error"`
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
	Title            string
	CWD              string
	SelectedRun      string
	TaskSummary      string
	Docs             []docLink
	Runs             []runLink
	Readiness        []readinessItem
	DefaultPlan      string
	ActiveRuns       []webRunView
	Artifacts        []artifactLink
	RunForm          *runFormData
	RunResult        *runStartResult
	WebRun           *webRunView
	RunDetail        *runDetail
	RunCompare       *runCompare
	RunFilters       *runHistoryFilters
	RunFilterOptions runHistoryFilterOptions
	RunsOnly         bool
	Path             string
	RunID            string
	Content          string
	Rendered         template.HTML
	Error            string
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
			if isRunAuditRequestPath(r.URL.Path, r.URL.EscapedPath()) {
				s.writeRunAuditExport(w, http.StatusForbidden, deniedRunAuditExport("", "run id denied", "run id is not allowed"))
				return
			}
			s.renderError(w, http.StatusForbidden, errors.New("request path is not allowed"))
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/runs", s.handleRunsIndex)
	s.mux.HandleFunc("/runs/audit", s.handleRunAudit)
	s.mux.HandleFunc("/runs/compare", s.handleRunCompare)
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
	data, status, err := s.loadDocPage(rel)
	if err != nil {
		s.renderError(w, status, err)
		return
	}
	s.render(w, data)
}

func (s *Server) loadDocPage(rel string) (pageData, int, error) {
	if strings.TrimSpace(rel) == "" {
		return pageData{}, http.StatusBadRequest, errors.New("path is required")
	}
	if !isMarkdown(rel) && strings.ToLower(filepath.Ext(rel)) != ".json" {
		return pageData{}, http.StatusBadRequest, errors.New("only allowlisted markdown and json state files are supported")
	}
	if !isAllowedDocPath(rel) {
		return pageData{}, http.StatusForbidden, errors.New("state path is not allowed")
	}
	path, err := safeJoinProject(s.cwd, rel)
	if err != nil {
		return pageData{}, http.StatusForbidden, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pageData{}, http.StatusNotFound, errors.New("state file unavailable")
	}
	content, rendered := presentContent(rel, data, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace})
	return pageData{
		Title:    rel,
		CWD:      displayWorkspace,
		Path:     filepath.ToSlash(rel),
		Content:  content,
		Rendered: rendered,
	}, http.StatusOK, nil
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
	options := runHistoryFilterOptionsFor(runs)
	filters := runHistoryFiltersFromQuery(r.URL.Query(), options)
	runs = applyRunHistoryFilters(runs, filters)
	runs = s.sanitizeRunHistoryLinks(runs)
	runs = addRunCompareLinks(runs, runs)
	s.render(w, pageData{
		Title:            "run history",
		CWD:              displayWorkspace,
		Runs:             runs,
		RunFilters:       &filters,
		RunFilterOptions: options,
		RunsOnly:         true,
	})
}

func (s *Server) handleRunCompare(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/runs/compare" {
		http.NotFound(w, r)
		return
	}
	compare := s.loadRunCompare(r.URL.Query())
	s.render(w, pageData{
		Title:      "compare runs",
		CWD:        displayWorkspace,
		RunCompare: &compare,
	})
}

func (s *Server) handleRunAudit(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/runs/audit" {
		http.NotFound(w, r)
		return
	}
	runID, ok, export := auditQueryRunID(r.URL.Query())
	if !ok {
		status := http.StatusBadRequest
		if export.State == "denied" {
			status = http.StatusForbidden
		}
		s.writeRunAuditExport(w, status, export)
		return
	}
	export, status := s.loadRunAuditExport(strings.TrimSpace(runID))
	s.writeRunAuditExport(w, status, export)
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
		s.renderRunDetail(w, runID)
		return
	}
	switch parts[1] {
	case "manifest":
		s.handleRunManifest(w, runID)
	case "audit", "audit.json":
		export, status := s.loadRunAuditExport(runID)
		s.writeRunAuditExport(w, status, export)
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
	s.renderRunDetail(w, runID)
}

func (s *Server) renderRunDetail(w http.ResponseWriter, runID string) {
	if strings.TrimSpace(runID) == "" {
		s.renderError(w, http.StatusBadRequest, errors.New("run id is required"))
		return
	}
	inspection := s.loadRunInspection(runID)
	if inspection.HTTPStatus != http.StatusOK {
		s.renderError(w, inspection.HTTPStatus, errors.New(firstNonEmpty(inspection.Error, "run unavailable")))
		return
	}
	detail := inspection.Detail
	s.render(w, pageData{
		Title:     "run " + detail.ID,
		CWD:       displayWorkspace,
		RunID:     detail.ID,
		RunDetail: &detail,
	})
}

func auditQueryRunID(query url.Values) (string, bool, runAuditExport) {
	var values []string
	for _, name := range []string{"run", "id"} {
		if found, ok := query[name]; ok {
			values = append(values, found...)
		}
	}
	if len(values) == 0 || (len(values) == 1 && strings.TrimSpace(values[0]) == "") {
		return "", false, unavailableRunAuditExport("", "run id unavailable", "run id is required")
	}
	if len(values) != 1 {
		return "", false, deniedRunAuditExport("", "run id denied", "exactly one run id is required")
	}
	return values[0], true, runAuditExport{}
}

func (s *Server) loadRunAuditExport(runID string) (runAuditExport, int) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return unavailableRunAuditExport("", "run id unavailable", "run id is required"), http.StatusBadRequest
	}
	inspection := s.loadRunInspection(runID)
	if inspection.State == "denied" {
		return deniedRunAuditExport("", firstNonEmpty(inspection.ManifestState, "run id denied"), firstNonEmpty(inspection.Error, "run id is not allowed")), http.StatusForbidden
	}
	if inspection.HTTPStatus == http.StatusBadRequest {
		return unavailableRunAuditExport("", firstNonEmpty(inspection.ManifestState, "run id unavailable"), firstNonEmpty(inspection.Error, "run id is required")), http.StatusBadRequest
	}
	if inspection.HTTPStatus == http.StatusNotFound {
		return unavailableRunAuditExport(inspection.ID, firstNonEmpty(inspection.ManifestState, "run unavailable"), firstNonEmpty(inspection.Error, "run unavailable")), http.StatusNotFound
	}

	detail := inspection.Detail
	export := runAuditExportFromDetail(detail)
	if detail.ManifestState == "manifest available" {
		export.State = "available"
	} else {
		export.State = "unavailable"
		if export.Error == "" {
			export.Error = detail.ManifestState
		}
	}
	export.Security = inspection.AuditSecurity
	if export.Security.Summary == "" {
		export.Security = runAuditSecurityFromDetail(detail)
	}
	return sanitizeRunAuditExport(export, inspection.Roots...), http.StatusOK
}

func unavailableRunAuditExport(runID, manifestState, message string) runAuditExport {
	return runAuditExport{
		SchemaVersion: "jj.audit.v1",
		State:         "unavailable",
		RunID:         sanitizeRunAuditString(runID),
		ManifestState: sanitizeRunAuditString(manifestState),
		Error:         sanitizeRunAuditString(message),
		Status:        "unknown",
		Evaluation: runAuditEvaluation{
			Status:         "unknown",
			EvidenceStatus: "unknown",
		},
		Codex:    runAuditCodex{Status: "unknown"},
		Security: unavailableRunAuditSecurity(),
	}
}

func deniedRunAuditExport(runID, manifestState, message string) runAuditExport {
	export := unavailableRunAuditExport(runID, manifestState, message)
	export.State = "denied"
	return export
}

func runAuditExportFromDetail(detail runDetail) runAuditExport {
	export := runAuditExport{
		SchemaVersion: "jj.audit.v1",
		State:         "available",
		RunID:         detail.ID,
		ManifestState: detail.ManifestState,
		Error:         detail.Error,
		Status:        firstNonEmpty(detail.Status, "unknown"),
		StartedAt:     detail.StartedAt,
		FinishedAt:    detail.FinishedAt,
		Duration:      detail.Duration,
		DryRun:        detail.DryRun,
		Planner: runAuditPlanner{
			Provider:                 detail.PlannerProvider,
			Model:                    detail.PlannerModel,
			TaskProposalMode:         detail.TaskProposalMode,
			ResolvedTaskProposalMode: detail.ResolvedTaskProposalMode,
			SelectedTaskID:           detail.SelectedTaskID,
		},
		GeneratedDocs: runAuditLinksFromDetailLinks(detail.Docs),
		Artifacts:     runAuditLinksFromArtifactStatuses(detail.Artifacts),
		Evaluation:    runAuditEvaluationFromDetail(detail.Validation),
		Codex:         runAuditCodexFromDetail(detail.Codex),
		Commands:      runAuditCommandsFromDetails(detail.Commands),
		Security:      runAuditSecurityFromDetail(detail),
		NextActions:   append([]string(nil), detail.NextActions...),
	}
	return export
}

func runAuditLinksFromDetailLinks(links []runDetailLink) []runAuditLink {
	out := make([]runAuditLink, 0, len(links))
	for _, link := range links {
		out = append(out, runAuditLink{
			Label:     link.Label,
			Path:      link.Path,
			URL:       link.URL,
			Available: link.Available,
			Status:    firstNonEmpty(link.Status, "unknown"),
		})
	}
	return out
}

func runAuditLinksFromArtifactStatuses(statuses []runArtifactStatus) []runAuditLink {
	out := make([]runAuditLink, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, runAuditLink{
			Path:      status.Path,
			URL:       status.URL,
			Available: status.Available,
			Status:    firstNonEmpty(status.Status, "unknown"),
		})
	}
	return out
}

func runAuditEvaluationFromDetail(detail runValidationDetail) runAuditEvaluation {
	out := runAuditEvaluation{
		Status:         firstNonEmpty(detail.Status, "unknown"),
		EvidenceStatus: firstNonEmpty(detail.EvidenceStatus, "unknown"),
		Reason:         detail.Reason,
		Summary:        detail.Summary,
		CommandCount:   detail.CommandCount,
		PassedCount:    detail.PassedCount,
		FailedCount:    detail.FailedCount,
	}
	if link := runAuditOptionalLink("Validation results", detail.ResultsPath, detail.ResultsURL); link != nil {
		out.Results = link
	}
	if link := runAuditOptionalLink("Validation summary", detail.SummaryPath, detail.SummaryURL); link != nil {
		out.SummaryArtifact = link
	}
	return out
}

func runAuditCodexFromDetail(detail runCodexDetail) runAuditCodex {
	out := runAuditCodex{
		Ran:      detail.Ran,
		Skipped:  detail.Skipped,
		Status:   firstNonEmpty(detail.Status, "unknown"),
		Model:    detail.Model,
		ExitCode: detail.ExitCode,
		Duration: detail.Duration,
		Error:    detail.Error,
	}
	if link := runAuditOptionalLink("Codex events", detail.EventsPath, detail.EventsURL); link != nil {
		out.Events = link
	}
	if link := runAuditOptionalLink("Codex summary", detail.SummaryPath, detail.SummaryURL); link != nil {
		out.SummaryArtifact = link
	}
	if link := runAuditOptionalLink("Codex command metadata", detail.ExitPath, detail.ExitURL); link != nil {
		out.Exit = link
	}
	return out
}

func runAuditCommandsFromDetails(commands []runCommandDetail) []runAuditCommand {
	out := make([]runAuditCommand, 0, len(commands))
	for _, command := range commands {
		item := runAuditCommand{
			Source:   command.Source,
			Label:    command.Label,
			Name:     command.Name,
			Provider: command.Provider,
			Model:    command.Model,
			CWD:      command.CWD,
			RunID:    command.RunID,
			Argv:     append([]string(nil), command.Argv...),
			Status:   firstNonEmpty(command.Status, "unknown"),
			ExitCode: command.ExitCode,
			Duration: command.Duration,
			Error:    command.Error,
			Note:     command.Note,
		}
		if link := runAuditOptionalLink("stdout", command.StdoutPath, command.StdoutURL); link != nil {
			item.Stdout = link
		}
		if link := runAuditOptionalLink("stderr", command.StderrPath, command.StderrURL); link != nil {
			item.Stderr = link
		}
		out = append(out, item)
	}
	return out
}

func runAuditOptionalLink(label, path, url string) *runAuditLink {
	if strings.TrimSpace(path) == "" && strings.TrimSpace(url) == "" {
		return nil
	}
	status := "available"
	if strings.TrimSpace(url) == "" {
		status = "unavailable"
	}
	return &runAuditLink{Label: label, Path: path, URL: url, Available: url != "", Status: status}
}

func runAuditSecurityFromDetail(detail runDetail) runAuditSecurity {
	if detail.SecuritySummary == "" || detail.SecuritySummary == "security diagnostics unavailable" {
		return unavailableRunAuditSecurity()
	}
	return runAuditSecurity{
		Available: true,
		Summary:   detail.SecuritySummary,
		Details:   append([]string(nil), detail.SecurityDetails...),
	}
}

func unavailableRunAuditSecurity() runAuditSecurity {
	return runAuditSecurity{
		Available: false,
		Summary:   "security diagnostics unavailable",
		Details:   []string{"diagnostics unknown"},
	}
}

func runAuditSecurityFromManifest(securityMeta runpkg.ManifestSecurity) runAuditSecurity {
	summary, details := runDetailSecurityDiagnostics(securityMeta)
	if summary == "" {
		return unavailableRunAuditSecurity()
	}
	diag := securityMeta.Diagnostics
	redactionCount := securityMeta.RedactionCount
	if redactionCount < 0 {
		redactionCount = 0
	}
	deniedPathCount := diag.DeniedPathCount
	if deniedPathCount < 0 {
		deniedPathCount = 0
	}
	return runAuditSecurity{
		Available:                  true,
		Summary:                    summary,
		Details:                    details,
		RedactionApplied:           securityMeta.RedactionApplied,
		WorkspaceGuardrailsApplied: securityMeta.WorkspaceGuardrailsApplied,
		RedactionCount:             redactionCount,
		SecretMaterialPresent:      diag.SecretMaterialPresent,
		RootLabels:                 dashboardCategoryList(diag.RootLabels, "root"),
		GuardedRoots:               runAuditSecurityRoots(diag.GuardedRoots),
		DeniedPathCount:            deniedPathCount,
		DeniedPathCategories:       dashboardCategoryList(diag.DeniedPathCategories, "path_denied"),
		DeniedPathCategoryCounts:   runAuditCategoryCounts(diag.DeniedPathCategoryCounts, "path_denied"),
		FailureCategories:          dashboardCategoryList(diag.FailureCategories, "security_failure"),
		FailureCategoryCounts:      runAuditCategoryCounts(diag.FailureCategoryCounts, "security_failure"),
		CommandRecordCount:         maxInt(diag.CommandRecordCount, 0),
		CommandMetadataSanitized:   diag.CommandMetadataSanitized,
		CommandArgvSanitized:       diag.CommandArgvSanitized,
		CommandCWDLabel:            runAuditSecurityLabel(diag.CommandCWDLabel, "[workspace]"),
		CommandSanitizationStatus:  dashboardCategory(diag.CommandSanitizationStatus, "unknown"),
		RawCommandTextPersisted:    diag.RawCommandTextPersisted,
		RawEnvironmentPersisted:    diag.RawEnvironmentPersisted,
		DryRunParityApplied:        diag.DryRunParityApplied,
		DryRunParityStatus:         dashboardCategory(diag.DryRunParityStatus, "unknown"),
	}
}

func runAuditSecurityRoots(roots []runpkg.ManifestSecurityRoot) []runAuditSecurityRoot {
	out := make([]runAuditSecurityRoot, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		label := dashboardCategory(root.Label, "")
		path := runAuditSecurityRootPath(root.Path)
		if label == "" || path == "" {
			continue
		}
		key := label + "\x00" + path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, runAuditSecurityRoot{Label: label, Path: path})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label == out[j].Label {
			return out[i].Path < out[j].Path
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func runAuditSecurityRootPath(value string) string {
	value = strings.TrimSpace(secrets.Redact(value))
	switch value {
	case "[workspace]", "[run]", ".jj/runs":
		return value
	default:
		return ""
	}
}

func runAuditCategoryCounts(counts map[string]int, fallback string) map[string]int {
	out := map[string]int{}
	for key, count := range counts {
		if count <= 0 {
			continue
		}
		category := dashboardCategory(key, fallback)
		if category == "" {
			category = fallback
		}
		out[category] += count
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runAuditSecurityLabel(value, fallback string) string {
	value = strings.TrimSpace(secrets.Redact(value))
	if value == "[workspace]" || value == "[run]" {
		return value
	}
	return dashboardCategory(value, fallback)
}

func sanitizeRunAuditExport(export runAuditExport, roots ...security.CommandPathRoot) runAuditExport {
	export.SchemaVersion = sanitizeRunAuditString(firstNonEmpty(export.SchemaVersion, "jj.audit.v1"), roots...)
	export.State = sanitizeRunAuditString(firstNonEmpty(export.State, "unavailable"), roots...)
	export.RunID = sanitizeRunAuditString(export.RunID, roots...)
	export.ManifestState = sanitizeRunAuditString(export.ManifestState, roots...)
	export.Error = sanitizeRunAuditString(export.Error, roots...)
	export.Status = sanitizeRunAuditString(firstNonEmpty(export.Status, "unknown"), roots...)
	export.StartedAt = sanitizeRunAuditString(export.StartedAt, roots...)
	export.FinishedAt = sanitizeRunAuditString(export.FinishedAt, roots...)
	export.Duration = sanitizeRunAuditString(export.Duration, roots...)
	export.Planner.Provider = sanitizeRunAuditString(export.Planner.Provider, roots...)
	export.Planner.Model = sanitizeRunAuditString(export.Planner.Model, roots...)
	export.Planner.TaskProposalMode = sanitizeRunAuditString(export.Planner.TaskProposalMode, roots...)
	export.Planner.ResolvedTaskProposalMode = sanitizeRunAuditString(export.Planner.ResolvedTaskProposalMode, roots...)
	export.Planner.SelectedTaskID = sanitizeRunAuditString(export.Planner.SelectedTaskID, roots...)
	export.GeneratedDocs = sanitizeRunAuditLinks(export.GeneratedDocs, roots...)
	export.Artifacts = sanitizeRunAuditLinks(export.Artifacts, roots...)
	export.Evaluation = sanitizeRunAuditEvaluation(export.Evaluation, roots...)
	export.Codex = sanitizeRunAuditCodex(export.Codex, roots...)
	export.Commands = sanitizeRunAuditCommands(export.Commands, roots...)
	export.Security = sanitizeRunAuditSecurity(export.Security, roots...)
	export.NextActions = sanitizeRunAuditList(export.NextActions, roots...)
	return export
}

func sanitizeRunAuditEvaluation(evaluation runAuditEvaluation, roots ...security.CommandPathRoot) runAuditEvaluation {
	evaluation.Status = sanitizeRunAuditString(firstNonEmpty(evaluation.Status, "unknown"), roots...)
	evaluation.EvidenceStatus = sanitizeRunAuditString(firstNonEmpty(evaluation.EvidenceStatus, "unknown"), roots...)
	evaluation.Reason = sanitizeRunAuditString(evaluation.Reason, roots...)
	evaluation.Summary = sanitizeRunAuditString(evaluation.Summary, roots...)
	if evaluation.Results != nil {
		*evaluation.Results = sanitizeRunAuditLink(*evaluation.Results, roots...)
	}
	if evaluation.SummaryArtifact != nil {
		*evaluation.SummaryArtifact = sanitizeRunAuditLink(*evaluation.SummaryArtifact, roots...)
	}
	return evaluation
}

func sanitizeRunAuditCodex(codex runAuditCodex, roots ...security.CommandPathRoot) runAuditCodex {
	codex.Status = sanitizeRunAuditString(firstNonEmpty(codex.Status, "unknown"), roots...)
	codex.Model = sanitizeRunAuditString(codex.Model, roots...)
	codex.Duration = sanitizeRunAuditString(codex.Duration, roots...)
	codex.Error = sanitizeRunAuditString(codex.Error, roots...)
	if codex.Events != nil {
		*codex.Events = sanitizeRunAuditLink(*codex.Events, roots...)
	}
	if codex.SummaryArtifact != nil {
		*codex.SummaryArtifact = sanitizeRunAuditLink(*codex.SummaryArtifact, roots...)
	}
	if codex.Exit != nil {
		*codex.Exit = sanitizeRunAuditLink(*codex.Exit, roots...)
	}
	return codex
}

func sanitizeRunAuditCommands(commands []runAuditCommand, roots ...security.CommandPathRoot) []runAuditCommand {
	out := make([]runAuditCommand, 0, len(commands))
	for _, command := range commands {
		command.Source = sanitizeRunAuditString(command.Source, roots...)
		command.Label = sanitizeRunAuditString(command.Label, roots...)
		command.Name = sanitizeRunAuditString(command.Name, roots...)
		command.Provider = sanitizeRunAuditString(command.Provider, roots...)
		command.Model = sanitizeRunAuditString(command.Model, roots...)
		command.CWD = sanitizeRunAuditString(command.CWD, roots...)
		command.RunID = sanitizeRunAuditString(command.RunID, roots...)
		command.Argv = sanitizeRunAuditList(command.Argv, roots...)
		command.Status = sanitizeRunAuditString(firstNonEmpty(command.Status, "unknown"), roots...)
		command.Duration = sanitizeRunAuditString(command.Duration, roots...)
		command.Error = sanitizeRunAuditString(command.Error, roots...)
		command.Note = sanitizeRunAuditString(command.Note, roots...)
		if command.Stdout != nil {
			*command.Stdout = sanitizeRunAuditLink(*command.Stdout, roots...)
		}
		if command.Stderr != nil {
			*command.Stderr = sanitizeRunAuditLink(*command.Stderr, roots...)
		}
		out = append(out, command)
	}
	return out
}

func sanitizeRunAuditLinks(links []runAuditLink, roots ...security.CommandPathRoot) []runAuditLink {
	out := make([]runAuditLink, 0, len(links))
	for _, link := range links {
		link = sanitizeRunAuditLink(link, roots...)
		if link.Path != "" || link.URL != "" || link.Label != "" {
			out = append(out, link)
		}
	}
	return out
}

func sanitizeRunAuditLink(link runAuditLink, roots ...security.CommandPathRoot) runAuditLink {
	link.Label = sanitizeRunAuditString(link.Label, roots...)
	link.Path = sanitizeRunAuditString(link.Path, roots...)
	link.URL = sanitizeRunAuditString(link.URL, roots...)
	link.Status = sanitizeRunAuditString(firstNonEmpty(link.Status, "unknown"), roots...)
	return link
}

func sanitizeRunAuditSecurity(securityMeta runAuditSecurity, roots ...security.CommandPathRoot) runAuditSecurity {
	securityMeta.Summary = sanitizeRunAuditString(firstNonEmpty(securityMeta.Summary, "security diagnostics unavailable"), roots...)
	securityMeta.Details = sanitizeRunAuditList(securityMeta.Details, roots...)
	securityMeta.RootLabels = dashboardCategoryList(securityMeta.RootLabels, "root")
	securityMeta.DeniedPathCategories = dashboardCategoryList(securityMeta.DeniedPathCategories, "path_denied")
	securityMeta.DeniedPathCategoryCounts = runAuditCategoryCounts(securityMeta.DeniedPathCategoryCounts, "path_denied")
	securityMeta.FailureCategories = dashboardCategoryList(securityMeta.FailureCategories, "security_failure")
	securityMeta.FailureCategoryCounts = runAuditCategoryCounts(securityMeta.FailureCategoryCounts, "security_failure")
	securityMeta.CommandCWDLabel = runAuditSecurityLabel(securityMeta.CommandCWDLabel, "")
	securityMeta.CommandSanitizationStatus = dashboardCategory(securityMeta.CommandSanitizationStatus, "")
	securityMeta.DryRunParityStatus = dashboardCategory(securityMeta.DryRunParityStatus, "")
	if securityMeta.RedactionCount < 0 {
		securityMeta.RedactionCount = 0
	}
	if securityMeta.DeniedPathCount < 0 {
		securityMeta.DeniedPathCount = 0
	}
	if securityMeta.CommandRecordCount < 0 {
		securityMeta.CommandRecordCount = 0
	}
	return securityMeta
}

func sanitizeRunAuditString(value string, roots ...security.CommandPathRoot) string {
	return strings.TrimSpace(sanitizeRunDetailText(value, roots...))
}

func sanitizeRunAuditList(items []string, roots ...security.CommandPathRoot) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := sanitizeRunAuditString(item, roots...); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func (s *Server) writeRunAuditExport(w http.ResponseWriter, status int, export runAuditExport) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	export = sanitizeRunAuditExport(export, security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace})
	if err := json.NewEncoder(w).Encode(export); err != nil {
		http.Error(w, sanitizeDashboardText(err.Error(), security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace}), http.StatusInternalServerError)
	}
}

func (s *Server) loadRunCompare(query url.Values) runCompare {
	left := s.loadRunCompareSide("Left Run", "left", query)
	right := s.loadRunCompareSide("Right Run", "right", query)
	compare := runCompare{Sides: []runCompareSide{left, right}}
	if left.validID && right.validID && left.ID == right.ID {
		compare.Notice = "Comparison requires two different run IDs."
		compare.Sides[0] = unavailableRunCompareSide("Left Run", left.ID, "comparison unavailable", "identical run IDs are not compared")
		compare.Sides[0].validID = true
		compare.Sides[1] = unavailableRunCompareSide("Right Run", right.ID, "comparison unavailable", "identical run IDs are not compared")
		compare.Sides[1].validID = true
	}
	return compare
}

func (s *Server) loadRunCompareSide(label, queryName string, query url.Values) runCompareSide {
	rawRunID, ok, state := compareQueryRunID(query, queryName)
	if !ok {
		return state
	}
	runID := strings.TrimSpace(rawRunID)
	return s.runCompareSideFromInspection(label, s.loadRunInspection(runID))
}

func compareQueryRunID(query url.Values, name string) (string, bool, runCompareSide) {
	values, exists := query[name]
	label := "Left Run"
	if name == "right" {
		label = "Right Run"
	}
	if !exists || len(values) == 0 || (len(values) == 1 && strings.TrimSpace(values[0]) == "") {
		return "", false, unavailableRunCompareSide(label, "", "run id unavailable", "run id is required")
	}
	if len(values) != 1 {
		return "", false, deniedRunCompareSide(label, "", "run id denied", "exactly one run id is required")
	}
	return values[0], true, runCompareSide{}
}

func unavailableRunCompareSide(label, id, manifestState, message string) runCompareSide {
	return runCompareSide{
		Label:           label,
		ID:              id,
		State:           "unavailable",
		ManifestState:   manifestState,
		Error:           message,
		Status:          "unknown",
		SecuritySummary: "security diagnostics unavailable",
		SecurityDetails: []string{"diagnostics unknown"},
		Validation: runValidationDetail{
			Status:         "unknown",
			EvidenceStatus: "unknown",
		},
		Codex: runCodexDetail{Status: "unknown"},
	}
}

func deniedRunCompareSide(label, id, manifestState, message string) runCompareSide {
	side := unavailableRunCompareSide(label, id, manifestState, message)
	side.State = "denied"
	return side
}

func (s *Server) runCompareSideFromInspection(label string, inspection runInspection) runCompareSide {
	if inspection.State != "available" {
		side := unavailableRunCompareSide(
			label,
			inspection.ID,
			firstNonEmpty(inspection.ManifestState, "manifest unavailable"),
			firstNonEmpty(inspection.Error, "manifest unavailable"),
		)
		side.State = firstNonEmpty(inspection.State, "unavailable")
		side.validID = inspection.ValidID
		return side
	}
	detail := inspection.Detail
	manifest := inspection.manifest
	return runCompareSide{
		Label:                    label,
		ID:                       detail.ID,
		State:                    "available",
		ManifestState:            detail.ManifestState,
		Status:                   detail.Status,
		StartedAt:                detail.StartedAt,
		FinishedAt:               detail.FinishedAt,
		Duration:                 detail.Duration,
		DryRun:                   detail.DryRun,
		PlannerProvider:          detail.PlannerProvider,
		PlannerModel:             detail.PlannerModel,
		TaskProposalMode:         detail.TaskProposalMode,
		ResolvedTaskProposalMode: detail.ResolvedTaskProposalMode,
		SelectedTaskID:           detail.SelectedTaskID,
		Docs:                     s.runCompareDocs(manifest, inspection.RunDir, inspection.rawID, inspection.Roots...),
		Artifacts:                detail.Artifacts,
		Validation:               detail.Validation,
		Codex:                    detail.Codex,
		Commands:                 runCompareCommandDetails(manifest, inspection.RunDir, inspection.rawID, inspection.Roots...),
		SecuritySummary:          detail.SecuritySummary,
		SecurityDetails:          detail.SecurityDetails,
		validID:                  true,
	}
}

func runCompareCommandDetails(manifest dashboardManifest, runDir, runID string, roots ...security.CommandPathRoot) []runCommandDetail {
	commands := make([]runCommandDetail, 0, len(manifest.Validation.Commands)+1)
	for _, command := range manifest.Validation.Commands {
		commands = append(commands, validationCommandDetail(manifest, runDir, runID, command, roots...))
	}
	if manifest.Codex.Ran || strings.TrimSpace(manifest.Codex.Status) != "" || strings.TrimSpace(manifest.Codex.Model) != "" || manifest.Codex.ExitCode != 0 || manifest.Codex.DurationMS > 0 {
		safeText := func(value string) string {
			return sanitizeRunDetailText(value, roots...)
		}
		commands = append(commands, runCommandDetail{
			Source:   "Codex",
			Label:    "codex",
			Name:     "codex",
			Provider: "codex",
			Model:    safeText(manifest.Codex.Model),
			Status:   safeText(firstNonEmpty(manifest.Codex.Status, "unknown")),
			ExitCode: manifest.Codex.ExitCode,
			Duration: formatDurationMS(manifest.Codex.DurationMS),
			Error:    safeText(manifest.Codex.Error),
			Note:     "metadata from manifest",
		})
	}
	return commands
}

func (s *Server) runCompareDocs(manifest dashboardManifest, runDir, runID string, roots ...security.CommandPathRoot) []runDetailLink {
	var docs []runDetailLink
	addDoc := func(label, raw string) {
		if strings.TrimSpace(raw) == "" {
			return
		}
		clean, err := cleanAllowedProjectPath(raw)
		if err != nil || !isProjectDocPath(clean) {
			return
		}
		display, ok := safeRunDetailPath(clean, roots...)
		if !ok {
			return
		}
		path, err := safeJoinProject(s.cwd, clean)
		available := false
		status := "missing"
		if err == nil {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				available = true
				status = "available"
			}
		}
		link := runDetailLink{Label: label, Path: display, Available: available, Status: status}
		if available {
			link.URL = docURL(clean)
		}
		docs = append(docs, link)
	}
	addArtifact := func(label, raw string) {
		status := artifactStatusForPath(manifest, runDir, runID, raw, roots...)
		if status.Path == "" {
			return
		}
		docs = append(docs, runDetailLink{
			Label:     label,
			Path:      status.Path,
			URL:       status.URL,
			Available: status.Available,
			Status:    status.Status,
		})
	}
	addDoc("Workspace SPEC", manifest.Workspace.SpecPath)
	addDoc("Workspace TASK", manifest.Workspace.TaskPath)
	addArtifact("Planned SPEC snapshot", listedArtifactPath(manifest.Artifacts, "snapshot_spec_after"))
	addArtifact("Planned TASK snapshot", listedArtifactPath(manifest.Artifacts, "snapshot_tasks_after"))
	addArtifact("Legacy SPEC doc", artifactPathByValue(manifest.Artifacts, "docs/SPEC.md"))
	addArtifact("Legacy TASK doc", artifactPathByValue(manifest.Artifacts, "docs/TASK.md"))
	return docs
}

func (s *Server) handleRunManifest(w http.ResponseWriter, runID string) {
	data, status, err := s.loadRunManifestResponse(runID)
	if err != nil {
		s.renderError(w, status, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (s *Server) loadRunManifestResponse(runID string) ([]byte, int, error) {
	runDir, err := s.runDir(runID)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	data, err := readRunFile(runDir, "manifest.json")
	if err != nil {
		return nil, http.StatusNotFound, errors.New("manifest unavailable")
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		data, err := json.Marshal(map[string]string{
			"run_id": runID,
			"status": "unknown",
			"error":  "manifest is malformed",
		})
		if err != nil {
			return nil, http.StatusInternalServerError, errors.New("manifest unavailable")
		}
		return append(data, '\n'), http.StatusOK, nil
	}
	sanitized := sanitizeDashboardValue(
		security.RedactJSONValue(decoded),
		security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace},
		security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + runID},
	)
	redacted, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return nil, http.StatusInternalServerError, errors.New("manifest unavailable")
	}
	redacted = append(redacted, '\n')
	return redacted, http.StatusOK, nil
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run")
	rawRel := r.URL.Query().Get("path")
	data, status, err := s.loadArtifactPage(runID, rawRel)
	if err != nil {
		s.renderError(w, status, err)
		return
	}
	s.render(w, data)
}

func (s *Server) loadArtifactPage(runID, rawRel string) (pageData, int, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(rawRel) == "" {
		return pageData{}, http.StatusBadRequest, errors.New("run and path are required")
	}
	rel, err := cleanAllowedArtifactPath(rawRel)
	if err != nil {
		return pageData{}, http.StatusForbidden, err
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		return pageData{}, http.StatusBadRequest, err
	}
	if ok, err := isManifestListedArtifact(runDir, rel); err != nil {
		return pageData{}, http.StatusBadRequest, err
	} else if !ok {
		return pageData{}, http.StatusForbidden, errors.New("artifact path is not listed in manifest")
	}
	path, err := safeJoin(runDir, rel)
	if err != nil {
		return pageData{}, http.StatusForbidden, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pageData{}, http.StatusNotFound, errors.New("artifact unavailable")
	}
	content, rendered := presentContent(
		rel,
		data,
		security.CommandPathRoot{Path: s.cwd, Label: displayWorkspace},
		security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + runID},
	)
	return pageData{
		Title:    runID + "/" + rel,
		CWD:      displayWorkspace,
		RunID:    runID,
		Path:     filepath.ToSlash(rel),
		Content:  content,
		Rendered: rendered,
	}, http.StatusOK, nil
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
		if !safeDisplayRunID(entry.Name()) {
			continue
		}
		inspection := s.loadRunInspection(entry.Name())
		if inspection.History.ID == "" {
			continue
		}
		runs = append(runs, inspection.History)
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].ID > runs[j].ID
	})
	return runs, nil
}

func safeDisplayRunID(runID string) bool {
	redacted := secrets.Redact(runID)
	return redacted == runID && !strings.Contains(redacted, security.RedactionMarker)
}

func runHistoryFilterOptionsFor(runs []runLink) runHistoryFilterOptions {
	statuses := map[string]bool{
		"cancelled":               true,
		"complete":                true,
		"completed":               true,
		"completed_with_warnings": true,
		"dry_run_complete":        true,
		"failed":                  true,
		"implementing":            true,
		"partial_failed":          true,
		"planned":                 true,
		"planning":                true,
		"running":                 true,
		"success":                 true,
		"unavailable":             true,
		"unknown":                 true,
		"validating":              true,
	}
	providers := map[string]bool{
		"codex":       true,
		"local":       true,
		"openai":      true,
		"unavailable": true,
		"unknown":     true,
	}
	evaluations := map[string]bool{
		"failed":      true,
		"missing":     true,
		"passed":      true,
		"recorded":    true,
		"skipped":     true,
		"unavailable": true,
		"unknown":     true,
	}
	for _, run := range runs {
		statuses[runHistoryStatusValue(run)] = true
		providers[runHistoryProviderValue(run)] = true
		evaluations[runHistoryEvaluationValue(run)] = true
	}
	return runHistoryFilterOptions{
		Statuses: sortedRunHistoryValues(statuses),
		DryRunModes: []runHistoryFilterOption{
			{Value: "true", Label: "dry-run"},
			{Value: "false", Label: "full-run"},
		},
		PlannerProviders: sortedRunHistoryValues(providers),
		Evaluations:      sortedRunHistoryValues(evaluations),
	}
}

func runHistoryFiltersFromQuery(query url.Values, options runHistoryFilterOptions) runHistoryFilters {
	var filters runHistoryFilters
	ignored := false
	if value, ok := parseAllowlistedRunHistoryFilter(firstQueryValue(query, "status"), options.Statuses); ok {
		filters.Status = value
	} else {
		ignored = true
	}
	if value, ok := parseRunHistoryDryRunFilter(firstQueryValue(query, "dry_run", "dry-run", "dry")); ok {
		filters.DryRun = value
	} else {
		ignored = true
	}
	if value, ok := parseAllowlistedRunHistoryFilter(firstQueryValue(query, "planner_provider", "provider"), options.PlannerProviders); ok {
		filters.PlannerProvider = value
	} else {
		ignored = true
	}
	if value, ok := parseAllowlistedRunHistoryFilter(firstQueryValue(query, "evaluation", "eval"), options.Evaluations); ok {
		filters.Evaluation = value
	} else {
		ignored = true
	}
	if value, ok := parseRunHistoryQueryFilter(firstQueryValue(query, "q", "run_id", "id")); ok {
		filters.Query = value
	} else {
		ignored = true
	}
	filters.HasActive = filters.Status != "" || filters.DryRun != "" || filters.PlannerProvider != "" || filters.Evaluation != "" || filters.Query != ""
	if ignored {
		filters.Notice = "Some unsupported filters were ignored."
	}
	return filters
}

func firstQueryValue(query url.Values, names ...string) string {
	for _, name := range names {
		if values, ok := query[name]; ok && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func parseAllowlistedRunHistoryFilter(raw string, allowed []string) (string, bool) {
	token, ok := runHistoryQueryToken(raw)
	if !ok || token == "" {
		return "", ok
	}
	if token == "all" || token == "any" {
		return "", true
	}
	for _, value := range allowed {
		if token == value {
			return token, true
		}
	}
	return "", false
}

func parseRunHistoryDryRunFilter(raw string) (string, bool) {
	token, ok := runHistoryQueryToken(raw)
	if !ok || token == "" {
		return "", ok
	}
	switch token {
	case "1", "true", "yes", "dry_run", "dryrun":
		return "true", true
	case "0", "false", "no", "full_run", "fullrun", "non_dry_run", "nondryrun":
		return "false", true
	case "all", "any":
		return "", true
	default:
		return "", false
	}
}

func parseRunHistoryQueryFilter(raw string) (string, bool) {
	value := strings.TrimSpace(secrets.Redact(raw))
	if value == "" {
		return "", true
	}
	if strings.Contains(value, security.RedactionMarker) || len(value) > 128 {
		return "", false
	}
	if value == "." || value == ".." || strings.HasPrefix(value, ".") || strings.Contains(value, "..") {
		return "", false
	}
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
		if !valid {
			return "", false
		}
	}
	return value, true
}

func runHistoryQueryToken(raw string) (string, bool) {
	value := strings.TrimSpace(secrets.Redact(raw))
	if value == "" {
		return "", true
	}
	if strings.Contains(value, security.RedactionMarker) {
		return "", false
	}
	token := dashboardCategory(value, "")
	if token == "" {
		return "", false
	}
	return token, true
}

func applyRunHistoryFilters(runs []runLink, filters runHistoryFilters) []runLink {
	if !filters.HasActive {
		return runs
	}
	out := make([]runLink, 0, len(runs))
	query := strings.ToLower(filters.Query)
	for _, run := range runs {
		if filters.Status != "" && runHistoryStatusValue(run) != filters.Status {
			continue
		}
		if filters.DryRun != "" && runHistoryDryRunValue(run) != filters.DryRun {
			continue
		}
		if filters.PlannerProvider != "" && runHistoryProviderValue(run) != filters.PlannerProvider {
			continue
		}
		if filters.Evaluation != "" && runHistoryEvaluationValue(run) != filters.Evaluation {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(run.ID), query) {
			continue
		}
		out = append(out, run)
	}
	return out
}

func runHistoryStatusValue(run runLink) string {
	if run.Invalid && strings.TrimSpace(run.Status) == "" {
		return "unavailable"
	}
	return runHistoryToken(firstNonEmpty(run.Status, "unknown"), "unknown")
}

func runHistoryDryRunValue(run runLink) string {
	if run.Invalid {
		return "unknown"
	}
	if run.DryRun {
		return "true"
	}
	return "false"
}

func runHistoryProviderValue(run runLink) string {
	if run.Invalid {
		return "unavailable"
	}
	return runHistoryToken(firstNonEmpty(run.PlannerProvider, "unknown"), "unknown")
}

func runHistoryEvaluationValue(run runLink) string {
	if run.Invalid {
		return "unavailable"
	}
	value := strings.ToLower(strings.TrimSpace(secrets.Redact(run.Validation)))
	if value == "" {
		return "unknown"
	}
	if strings.Contains(value, security.RedactionMarker) {
		return "unknown"
	}
	for _, candidate := range []string{"failed", "passed", "missing", "skipped", "recorded"} {
		if strings.Contains(value, candidate) {
			return candidate
		}
	}
	return runHistoryToken(value, "unknown")
}

func runHistoryToken(value, fallback string) string {
	value = strings.TrimSpace(secrets.Redact(value))
	if value == "" || strings.Contains(value, security.RedactionMarker) {
		return fallback
	}
	return dashboardCategory(value, fallback)
}

func sortedRunHistoryValues(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) sanitizeRunHistoryLinks(runs []runLink) []runLink {
	out := make([]runLink, 0, len(runs))
	for _, run := range runs {
		out = append(out, s.sanitizeRunHistoryLink(run))
	}
	return out
}

func addRunCompareLinks(runs, candidates []runLink) []runLink {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if err := artifact.ValidateRunID(candidate.ID); err != nil || !safeDisplayRunID(candidate.ID) {
			continue
		}
		ids = append(ids, candidate.ID)
	}
	if len(ids) < 2 {
		return runs
	}
	out := make([]runLink, 0, len(runs))
	for _, run := range runs {
		if err := artifact.ValidateRunID(run.ID); err == nil && safeDisplayRunID(run.ID) {
			for _, other := range ids {
				if other == run.ID {
					continue
				}
				run.CompareURL = runCompareURL(run.ID, other)
				break
			}
		}
		out = append(out, run)
	}
	return out
}

func runCompareURL(left, right string) string {
	return "/runs/compare?left=" + template.URLQueryEscaper(left) + "&right=" + template.URLQueryEscaper(right)
}

func (s *Server) sanitizeRunHistoryLink(run runLink) runLink {
	runDir, _ := s.runDir(run.ID)
	roots := []security.CommandPathRoot{{Path: s.cwd, Label: displayWorkspace}}
	if strings.TrimSpace(runDir) != "" {
		roots = append(roots, security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + run.ID})
	}
	run.Status = historyDisplayText(run.Status, "unknown", roots...)
	run.StartedAt = historyDisplayText(run.StartedAt, "", roots...)
	run.FinishedAt = historyDisplayText(run.FinishedAt, "", roots...)
	run.PlannerProvider = historyDisplayText(run.PlannerProvider, "unknown", roots...)
	run.Validation = historyDisplayText(run.Validation, "", roots...)
	run.TaskProposalMode = historyDisplayText(run.TaskProposalMode, "", roots...)
	run.ResolvedTaskProposalMode = historyDisplayText(run.ResolvedTaskProposalMode, "", roots...)
	run.SelectedTaskID = historyDisplayText(run.SelectedTaskID, "", roots...)
	run.RepositoryURL = historyDisplayText(run.RepositoryURL, "", roots...)
	run.BaseBranch = historyDisplayText(run.BaseBranch, "", roots...)
	run.WorkBranch = historyDisplayText(run.WorkBranch, "", roots...)
	run.PushStatus = historyDisplayText(run.PushStatus, "", roots...)
	run.PushedRef = historyDisplayText(run.PushedRef, "", roots...)
	run.ErrorSummary = historySensitiveText(run.ErrorSummary, roots...)
	run.RiskSummary = historySensitiveText(run.RiskSummary, roots...)
	run.Risks = historySensitiveList(run.Risks, roots...)
	run.Failures = historySensitiveList(run.Failures, roots...)
	run.NextActions = historySensitiveList(run.NextActions, roots...)
	run.SecuritySummary = historyDisplayText(run.SecuritySummary, "", roots...)
	run.SecurityDetails = historySensitiveList(run.SecurityDetails, roots...)
	if display, ok := safeRunDetailPath(run.ValidationArtifact, roots...); ok {
		run.ValidationArtifact = display
	} else {
		run.ValidationArtifact = ""
	}
	return run
}

func historyDisplayText(value, fallback string, roots ...security.CommandPathRoot) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	raw := sanitizeDashboardText(value, roots...)
	if strings.Contains(raw, security.RedactionMarker) {
		return fallback
	}
	text := strings.TrimSpace(sanitizeRunDetailText(value, roots...))
	if text == "" {
		return fallback
	}
	return text
}

func historySensitiveText(value string, roots ...security.CommandPathRoot) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	text := strings.TrimSpace(sanitizeRunDetailText(value, roots...))
	if text == "" {
		return ""
	}
	return text
}

func historySensitiveList(items []string, roots ...security.CommandPathRoot) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := historySensitiveText(item, roots...); text != "" {
			out = append(out, text)
		}
	}
	return out
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

func (s *Server) loadRunInspection(runID string) runInspection {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return s.finalizeRunInspection(runInspection{
			State:         "unavailable",
			HTTPStatus:    http.StatusBadRequest,
			ManifestState: "run id unavailable",
			Error:         "run id is required",
		})
	}
	if err := artifact.ValidateRunID(runID); err != nil || !safeDisplayRunID(runID) {
		return s.finalizeRunInspection(runInspection{
			State:         "denied",
			HTTPStatus:    http.StatusForbidden,
			ManifestState: "run id denied",
			Error:         "run id is not allowed",
		})
	}
	runDir, err := s.runDir(runID)
	if err != nil {
		return s.finalizeRunInspection(runInspection{
			State:         "denied",
			HTTPStatus:    http.StatusForbidden,
			ManifestState: "run id denied",
			Error:         "run id is not allowed",
		})
	}
	roots := runInspectionRoots(s.cwd, runDir, runID)
	inspection := runInspection{
		ID:            sanitizeRunDetailText(runID, roots...),
		rawID:         runID,
		RunDir:        runDir,
		Roots:         roots,
		ValidID:       true,
		State:         "unavailable",
		HTTPStatus:    http.StatusOK,
		ManifestState: "manifest unavailable",
		Error:         "manifest unavailable",
	}
	info, err := os.Stat(runDir)
	if errors.Is(err, os.ErrNotExist) {
		inspection.ManifestState = "run unavailable"
		inspection.Error = "run unavailable"
		inspection.HTTPStatus = http.StatusNotFound
		return s.finalizeRunInspection(inspection)
	}
	if err != nil {
		inspection.State = "denied"
		inspection.ManifestState = "run unavailable"
		inspection.Error = "run unavailable"
		inspection.HTTPStatus = http.StatusForbidden
		return s.finalizeRunInspection(inspection)
	}
	if !info.IsDir() {
		inspection.ManifestState = "run unavailable"
		inspection.Error = "run unavailable"
		inspection.HTTPStatus = http.StatusNotFound
		return s.finalizeRunInspection(inspection)
	}
	data, err := readRunFile(runDir, "manifest.json")
	if errors.Is(err, os.ErrNotExist) {
		return s.finalizeRunInspection(inspection)
	}
	if err != nil {
		inspection.State = "denied"
		inspection.ManifestState = "manifest unavailable"
		inspection.Error = "manifest unavailable"
		inspection.HTTPStatus = http.StatusForbidden
		return s.finalizeRunInspection(inspection)
	}
	var manifest dashboardManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		inspection.ManifestState = "manifest is malformed"
		inspection.Error = "manifest is malformed"
		return s.finalizeRunInspection(inspection)
	}
	inspection.ManifestLoaded = true
	inspection.manifest = manifest
	inspection.ManifestState, inspection.TrustedManifest = runManifestState(runID, manifest)
	if inspection.TrustedManifest {
		inspection.State = "available"
		inspection.Error = ""
	} else {
		inspection.State = "unavailable"
		inspection.Error = inspection.ManifestState
	}
	return s.finalizeRunInspection(inspection)
}

func runInspectionRoots(cwd, runDir, runID string) []security.CommandPathRoot {
	roots := []security.CommandPathRoot{{Path: cwd, Label: displayWorkspace}}
	if strings.TrimSpace(runDir) != "" && strings.TrimSpace(runID) != "" {
		roots = append(roots, security.CommandPathRoot{Path: runDir, Label: ".jj/runs/" + runID})
	}
	return roots
}

func (s *Server) finalizeRunInspection(inspection runInspection) runInspection {
	if inspection.State == "" {
		inspection.State = "unavailable"
	}
	if inspection.HTTPStatus == 0 {
		inspection.HTTPStatus = http.StatusOK
	}
	if strings.TrimSpace(inspection.ManifestState) == "" {
		inspection.ManifestState = "manifest unavailable"
	}
	if inspection.State != "available" && strings.TrimSpace(inspection.Error) == "" {
		inspection.Error = inspection.ManifestState
	}
	inspection.Detail = s.runDetailFromInspection(inspection)
	inspection.History = s.runHistoryLinkFromInspection(inspection)
	if inspection.ManifestLoaded {
		inspection.AuditSecurity = runAuditSecurityFromManifest(inspection.manifest.Security)
	} else {
		inspection.AuditSecurity = unavailableRunAuditSecurity()
	}
	return inspection
}

func runManifestState(runID string, manifest dashboardManifest) (string, bool) {
	switch {
	case strings.TrimSpace(manifest.RunID) == "":
		return "manifest is incomplete: missing run_id", false
	case manifest.RunID != runID:
		return "manifest is incomplete: run_id mismatch", false
	case strings.TrimSpace(manifest.Status) == "":
		return "manifest is incomplete: missing status", false
	case manifest.Artifacts == nil:
		return "manifest is incomplete: missing artifacts", false
	default:
		return "manifest available", true
	}
}

func (s *Server) runHistoryLinkFromInspection(inspection runInspection) runLink {
	run := runLink{ID: inspection.ID}
	if !inspection.ValidID || strings.TrimSpace(inspection.ID) == "" {
		return run
	}
	if !inspection.TrustedManifest {
		run.Invalid = true
		run.Status = "unavailable"
		run.ErrorSummary = unavailableRunError(inspection.ManifestState)
		run.Failures = []string{run.ErrorSummary}
		return run
	}
	manifest := inspection.manifest
	safeText := func(value string) string {
		return sanitizeDashboardText(value, inspection.Roots...)
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
		run.Failures = sanitizeDashboardList(manifest.Errors, inspection.Roots...)
	}
	if len(manifest.Risks) > 0 {
		run.RiskSummary = safeText(manifest.Risks[0])
		run.Risks = sanitizeDashboardList(manifest.Risks, inspection.Roots...)
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
	return run
}

func (s *Server) loadRunDetail(runID, runDir string) runDetail {
	inspection := s.loadRunInspection(runID)
	if inspection.RunDir != "" && runDir != "" && filepath.Clean(inspection.RunDir) != filepath.Clean(runDir) {
		inspection.State = "denied"
		inspection.ManifestState = "run id denied"
		inspection.Error = "run id is not allowed"
		inspection.HTTPStatus = http.StatusForbidden
		inspection = s.finalizeRunInspection(inspection)
	}
	return inspection.Detail
}

func (s *Server) runDetailFromInspection(inspection runInspection) runDetail {
	roots := inspection.Roots
	safeText := func(value string) string {
		return sanitizeRunDetailText(value, roots...)
	}
	detail := runDetail{
		ID:              safeText(inspection.ID),
		Status:          "unknown",
		ManifestState:   safeText(inspection.ManifestState),
		Error:           safeText(inspection.Error),
		ArtifactNote:    unavailableRunError(inspection.ManifestState),
		SecuritySummary: "security diagnostics unavailable",
		SecurityDetails: []string{"diagnostics unknown"},
		Validation: runValidationDetail{
			Status:         "unknown",
			EvidenceStatus: "unknown",
		},
		Codex: runCodexDetail{Status: "unknown"},
	}
	if !inspection.ManifestLoaded {
		detail.NextActions = runDetailNextActions(detail, dashboardManifest{}, false)
		return detail
	}
	manifest := inspection.manifest
	trustedManifest := inspection.TrustedManifest
	if strings.TrimSpace(manifest.Status) == "" {
		detail.Status = "unknown"
	} else {
		detail.Status = safeText(manifest.Status)
	}
	if trustedManifest {
		detail.ArtifactNote = ""
	}

	detail.StartedAt = safeText(manifest.StartedAt)
	detail.FinishedAt = safeText(firstNonEmpty(manifest.FinishedAt, manifest.EndedAt))
	detail.Duration = formatDurationMS(manifest.DurationMS)
	detail.DryRun = manifest.DryRun
	detail.PlannerProvider = safeText(firstNonEmpty(manifest.PlannerProvider, manifest.Planner.Provider))
	detail.PlannerModel = safeText(manifest.Planner.Model)
	detail.TaskProposalMode = safeText(manifest.TaskProposalMode)
	detail.ResolvedTaskProposalMode = safeText(manifest.ResolvedTaskProposalMode)
	detail.SelectedTaskID = safeText(manifest.SelectedTaskID)
	if manifest.Repository.Enabled {
		repo := safeText(firstNonEmpty(manifest.Repository.SanitizedRepoURL, manifest.Repository.RepoURL))
		base := safeText(manifest.Repository.BaseBranch)
		work := safeText(manifest.Repository.WorkBranch)
		push := safeText(manifest.Repository.PushStatus)
		detail.RepositorySummary = strings.TrimSpace(fmt.Sprintf("%s base %s work %s push %s", repo, base, work, push))
	}

	if trustedManifest {
		detail.Artifacts = s.runArtifactStatuses(manifest, inspection.RunDir, inspection.rawID, roots...)
		detail.Docs = s.runDetailDocs(manifest, inspection.RunDir, inspection.rawID, roots...)
		detail.Codex = s.runCodexDetail(manifest, inspection.RunDir, inspection.rawID, roots...)
		detail.Commands = s.runCommandDetails(manifest, inspection.RunDir, inspection.rawID, roots...)
	} else if detail.ArtifactNote == "" {
		detail.ArtifactNote = unavailableRunError(detail.ManifestState)
	}
	detail.Validation = s.runValidationDetail(manifest, inspection.RunDir, inspection.rawID, trustedManifest, roots...)
	detail.SecuritySummary, detail.SecurityDetails = runDetailSecurityDiagnostics(manifest.Security)
	if detail.SecuritySummary == "" {
		detail.SecuritySummary = "security diagnostics unavailable"
		detail.SecurityDetails = []string{"diagnostics unknown"}
	}
	detail.NextActions = runDetailNextActions(detail, manifest, trustedManifest)
	return detail
}

func (s *Server) runDetailDocs(manifest dashboardManifest, runDir, runID string, roots ...security.CommandPathRoot) []runDetailLink {
	var docs []runDetailLink
	addDoc := func(label, raw string) {
		clean, err := cleanAllowedProjectPath(raw)
		if err != nil || !isProjectDocPath(clean) {
			return
		}
		display, ok := safeRunDetailPath(clean, roots...)
		if !ok {
			return
		}
		path, err := safeJoinProject(s.cwd, clean)
		available := false
		status := "missing"
		if err == nil {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				available = true
				status = "available"
			}
		}
		link := runDetailLink{Label: label, Path: display, Available: available, Status: status}
		if available {
			link.URL = docURL(clean)
		}
		docs = append(docs, link)
	}
	addArtifact := func(label, raw string) {
		status := artifactStatusForPath(manifest, runDir, runID, raw, roots...)
		if status.Path == "" {
			return
		}
		docs = append(docs, runDetailLink{
			Label:     label,
			Path:      status.Path,
			URL:       status.URL,
			Available: status.Available,
			Status:    status.Status,
		})
	}
	addDoc("Workspace SPEC", firstNonEmpty(manifest.Workspace.SpecPath, runpkg.DefaultSpecStatePath))
	addDoc("Workspace TASK", firstNonEmpty(manifest.Workspace.TaskPath, runpkg.DefaultTasksStatePath))
	addArtifact("Planned SPEC snapshot", listedArtifactPath(manifest.Artifacts, "snapshot_spec_after"))
	addArtifact("Planned TASK snapshot", listedArtifactPath(manifest.Artifacts, "snapshot_tasks_after"))
	addArtifact("Legacy SPEC doc", artifactPathByValue(manifest.Artifacts, "docs/SPEC.md"))
	addArtifact("Legacy TASK doc", artifactPathByValue(manifest.Artifacts, "docs/TASK.md"))
	return docs
}

func (s *Server) runArtifactStatuses(manifest dashboardManifest, runDir, runID string, roots ...security.CommandPathRoot) []runArtifactStatus {
	seen := map[string]bool{}
	artifacts := make([]runArtifactStatus, 0, len(manifest.Artifacts))
	for _, raw := range manifest.Artifacts {
		clean, err := cleanAllowedArtifactPath(raw)
		if err != nil || seen[clean] {
			continue
		}
		seen[clean] = true
		status := artifactStatusForPath(manifest, runDir, runID, clean, roots...)
		if status.Path != "" {
			artifacts = append(artifacts, status)
		}
	}
	sort.SliceStable(artifacts, func(i, j int) bool {
		return artifactRank(artifacts[i].Path) < artifactRank(artifacts[j].Path) ||
			(artifactRank(artifacts[i].Path) == artifactRank(artifacts[j].Path) && artifacts[i].Path < artifacts[j].Path)
	})
	return artifacts
}

func (s *Server) runValidationDetail(manifest dashboardManifest, runDir, runID string, trustedManifest bool, roots ...security.CommandPathRoot) runValidationDetail {
	safeText := func(value string) string {
		return sanitizeRunDetailText(value, roots...)
	}
	detail := runValidationDetail{
		Status:         safeText(firstNonEmpty(manifest.Validation.Status, "unknown")),
		EvidenceStatus: safeText(firstNonEmpty(manifest.Validation.EvidenceStatus, "unknown")),
		CommandCount:   manifest.Validation.CommandCount,
		PassedCount:    manifest.Validation.PassedCount,
		FailedCount:    manifest.Validation.FailedCount,
	}
	if detail.CommandCount == 0 {
		detail.CommandCount = len(manifest.Validation.Commands)
	}
	if trustedManifest {
		detail.Reason = safeText(manifest.Validation.Reason)
		detail.Summary = safeText(manifest.Validation.Summary)
		if status := artifactStatusForPath(manifest, runDir, runID, manifest.Validation.ResultsPath, roots...); status.Path != "" {
			detail.ResultsPath = status.Path
			detail.ResultsURL = status.URL
		}
		if status := artifactStatusForPath(manifest, runDir, runID, manifest.Validation.SummaryPath, roots...); status.Path != "" {
			detail.SummaryPath = status.Path
			detail.SummaryURL = status.URL
		}
	}
	return detail
}

func (s *Server) runCodexDetail(manifest dashboardManifest, runDir, runID string, roots ...security.CommandPathRoot) runCodexDetail {
	safeText := func(value string) string {
		return sanitizeRunDetailText(value, roots...)
	}
	detail := runCodexDetail{
		Ran:      manifest.Codex.Ran,
		Skipped:  manifest.Codex.Skipped,
		Status:   safeText(firstNonEmpty(manifest.Codex.Status, "unknown")),
		Model:    safeText(manifest.Codex.Model),
		ExitCode: manifest.Codex.ExitCode,
		Duration: formatDurationMS(manifest.Codex.DurationMS),
		Error:    safeText(manifest.Codex.Error),
	}
	if status := artifactStatusForPath(manifest, runDir, runID, manifest.Codex.EventsPath, roots...); status.Path != "" {
		detail.EventsPath = status.Path
		detail.EventsURL = status.URL
	}
	if status := artifactStatusForPath(manifest, runDir, runID, manifest.Codex.SummaryPath, roots...); status.Path != "" {
		detail.SummaryPath = status.Path
		detail.SummaryURL = status.URL
	}
	if status := artifactStatusForPath(manifest, runDir, runID, manifest.Codex.ExitPath, roots...); status.Path != "" {
		detail.ExitPath = status.Path
		detail.ExitURL = status.URL
	}
	return detail
}

func (s *Server) runCommandDetails(manifest dashboardManifest, runDir, runID string, roots ...security.CommandPathRoot) []runCommandDetail {
	commands := make([]runCommandDetail, 0, len(manifest.Validation.Commands)+1)
	for _, command := range manifest.Validation.Commands {
		commands = append(commands, validationCommandDetail(manifest, runDir, runID, command, roots...))
	}
	if record, ok := loadCodexCommandRecord(manifest, runDir, runID, roots...); ok {
		commands = append(commands, commandRecordDetail("Codex", record, roots...))
	} else if strings.TrimSpace(manifest.Codex.ExitPath) != "" {
		status := artifactStatusForPath(manifest, runDir, runID, manifest.Codex.ExitPath, roots...)
		commands = append(commands, runCommandDetail{
			Source: "Codex",
			Label:  "codex",
			Status: firstNonEmpty(status.Status, "unavailable"),
			Note:   "codex command metadata unavailable",
		})
	}
	return commands
}

func validationCommandDetail(manifest dashboardManifest, runDir, runID string, command runpkg.ManifestValidationCommand, roots ...security.CommandPathRoot) runCommandDetail {
	safeText := func(value string) string {
		return sanitizeRunDetailText(value, roots...)
	}
	detail := runCommandDetail{
		Source:   "Validation",
		Label:    safeText(command.Label),
		Name:     safeText(command.Name),
		Provider: safeText(command.Provider),
		Model:    safeText(command.Model),
		CWD:      safeText(command.CWD),
		RunID:    safeText(command.RunID),
		Argv:     sanitizeRunDetailList(command.Argv, roots...),
		Status:   safeText(command.Status),
		ExitCode: command.ExitCode,
		Duration: formatDurationMS(command.DurationMS),
		Error:    safeText(command.Error),
	}
	if strings.TrimSpace(command.Command) != "" {
		detail.Note = "raw command text not shown"
	}
	if status := artifactStatusForPath(manifest, runDir, runID, command.StdoutPath, roots...); status.Path != "" {
		detail.StdoutPath = status.Path
		detail.StdoutURL = status.URL
	}
	if status := artifactStatusForPath(manifest, runDir, runID, command.StderrPath, roots...); status.Path != "" {
		detail.StderrPath = status.Path
		detail.StderrURL = status.URL
	}
	return detail
}

func loadCodexCommandRecord(manifest dashboardManifest, runDir, runID string, roots ...security.CommandPathRoot) (commandRecord, bool) {
	rel := strings.TrimSpace(manifest.Codex.ExitPath)
	if rel == "" {
		rel = listedArtifactPath(manifest.Artifacts, "codex_exit")
	}
	status := artifactStatusForPath(manifest, runDir, runID, rel, roots...)
	if !status.Available || status.URL == "" {
		return commandRecord{}, false
	}
	clean, err := cleanAllowedArtifactPath(rel)
	if err != nil {
		return commandRecord{}, false
	}
	path, err := safeJoin(runDir, clean)
	if err != nil {
		return commandRecord{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return commandRecord{}, false
	}
	data = security.RedactContent(clean, data)
	var record commandRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return commandRecord{}, false
	}
	return record, true
}

func commandRecordDetail(source string, record commandRecord, roots ...security.CommandPathRoot) runCommandDetail {
	safeText := func(value string) string {
		return sanitizeRunDetailText(value, roots...)
	}
	return runCommandDetail{
		Source:   source,
		Label:    safeText(firstNonEmpty(record.Name, source)),
		Name:     safeText(record.Name),
		Provider: safeText(record.Provider),
		Model:    safeText(record.Model),
		CWD:      safeText(record.CWD),
		RunID:    safeText(record.RunID),
		Argv:     sanitizeRunDetailList(record.Argv, roots...),
		Status:   safeText(record.Status),
		ExitCode: record.ExitCode,
		Duration: formatDurationMS(record.DurationMS),
		Error:    safeText(record.Error),
	}
}

func runDetailSecurityDiagnostics(securityMeta runpkg.ManifestSecurity) (string, []string) {
	summary, details := dashboardSecurityDiagnostics(securityMeta)
	if summary == "" {
		return "", nil
	}
	return sanitizeRunDetailText(summary), sanitizeRunDetailList(details)
}

func runDetailNextActions(detail runDetail, manifest dashboardManifest, trustedManifest bool) []string {
	var actions []string
	if !trustedManifest {
		actions = append(actions, "Artifact links are disabled until a trusted manifest is available.")
	}
	if detail.Validation.SummaryURL != "" {
		actions = append(actions, "Open validation summary for the recorded evaluation evidence.")
	}
	if len(manifest.Errors) > 0 || strings.EqualFold(detail.Status, "failed") || strings.Contains(strings.ToLower(detail.Status), "failed") {
		actions = append(actions, "Review sanitized failures and validation metadata before starting another full run.")
	}
	if detail.SecuritySummary == "security diagnostics unavailable" {
		actions = append(actions, "Security diagnostics are unavailable for this older or incomplete manifest.")
	}
	return appendUnique(nil, actions...)
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

func artifactStatusForPath(manifest dashboardManifest, runDir, runID, raw string, roots ...security.CommandPathRoot) runArtifactStatus {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return runArtifactStatus{}
	}
	clean, err := cleanAllowedArtifactPath(raw)
	if err != nil {
		return runArtifactStatus{Path: "guarded artifact", Status: "guarded"}
	}
	display, ok := safeRunDetailPath(clean, roots...)
	if !ok {
		return runArtifactStatus{Path: "guarded artifact", Status: "guarded"}
	}
	status := runArtifactStatus{Path: display, Status: "not listed"}
	if !manifestHasArtifactPath(manifest.Artifacts, clean) {
		return status
	}
	path, err := safeJoin(runDir, clean)
	if err != nil {
		status.Status = "guarded"
		return status
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		status.Status = "missing"
		return status
	}
	if err != nil {
		status.Status = "unavailable"
		return status
	}
	if info.IsDir() {
		status.Status = "unavailable"
		return status
	}
	status.Available = true
	status.Status = "available"
	status.URL = artifactURL(runID, clean)
	return status
}

func manifestHasArtifactPath(artifacts map[string]string, clean string) bool {
	if len(artifacts) == 0 {
		return false
	}
	for _, raw := range artifacts {
		if path, err := cleanAllowedArtifactPath(raw); err == nil && path == clean {
			return true
		}
	}
	return false
}

func artifactURL(runID, path string) string {
	return "/artifact?run=" + template.URLQueryEscaper(runID) + "&path=" + template.URLQueryEscaper(path)
}

func docURL(path string) string {
	return "/doc?path=" + template.URLQueryEscaper(path)
}

func formatDurationMS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return (time.Duration(ms) * time.Millisecond).String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func safeRunDetailPath(path string, roots ...security.CommandPathRoot) (string, bool) {
	sanitized := sanitizeDashboardText(path, roots...)
	if sanitized == "" || sanitized != path || strings.Contains(sanitized, security.RedactionMarker) {
		return "", false
	}
	return sanitized, true
}

func sanitizeRunDetailList(items []string, roots ...security.CommandPathRoot) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(sanitizeRunDetailText(item, roots...))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func sanitizeRunDetailText(text string, roots ...security.CommandPathRoot) string {
	text = sanitizeDashboardText(text, roots...)
	if strings.Contains(text, security.RedactionMarker) {
		text = strings.ReplaceAll(text, security.RedactionMarker, "sensitive value removed")
	}
	text = strings.ReplaceAll(text, "[REDACTED]", "sensitive value removed")
	text = strings.ReplaceAll(text, "[redacted]", "sensitive value removed")
	text = strings.ReplaceAll(text, "[omitted]", "sensitive value removed")
	text = strings.ReplaceAll(text, "{removed}", "sensitive value removed")
	text = strings.ReplaceAll(text, "<hidden>", "sensitive value removed")
	return text
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

func isRunAuditRequestPath(rawPath, escapedPath string) bool {
	lowerRaw := strings.ToLower(rawPath)
	lowerEscaped := strings.ToLower(escapedPath)
	if lowerRaw == "/runs/audit" || lowerEscaped == "/runs/audit" {
		return true
	}
	for _, suffix := range []string{"/audit", "/audit.json"} {
		if strings.HasSuffix(lowerRaw, suffix) || strings.HasSuffix(lowerEscaped, suffix) {
			return strings.HasPrefix(lowerRaw, "/runs/") || strings.HasPrefix(lowerEscaped, "/runs/")
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
    h3 { margin: 18px 0 8px; font-size: 14px; }
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
    form.filters { max-width: none; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); align-items: end; margin: 12px 0 18px; }
    form.filters .actions { margin: 0; }
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
      <p><a href="/runs/{{q .RunResult.RunID}}">Open run {{.RunResult.RunID}}</a></p>
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
      <p><a href="/runs/{{q .RunResult.RunID}}">Open run {{.RunResult.RunID}}</a></p>
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
    {{else if .RunCompare}}
      <p><a href="/">← dashboard</a> · <a href="/runs">all runs</a></p>
      {{if .RunCompare.Notice}}<p class="muted">{{.RunCompare.Notice}}</p>{{end}}
      <div class="grid">
      {{range .RunCompare.Sides}}
        <section>
          <h2>{{.Label}}</h2>
          <p>{{if .ID}}<a href="/runs/{{q .ID}}"><strong>{{.ID}}</strong></a>{{else}}<strong>{{.Label}}</strong>{{end}} <span class="muted">{{.State}}</span></p>
          <p class="muted">status {{.Status}} · started {{if .StartedAt}}{{.StartedAt}}{{else}}unknown{{end}} · finished {{if .FinishedAt}}{{.FinishedAt}}{{else}}unknown{{end}}{{if .Duration}} · duration {{.Duration}}{{end}} · dry-run {{.DryRun}}</p>
          <p class="muted">manifest {{.ManifestState}}</p>
          {{if .Error}}<p class="error">{{.Error}}</p>{{end}}

          <h3>Planner</h3>
          <p class="muted">provider {{if .PlannerProvider}}{{.PlannerProvider}}{{else}}unknown{{end}}{{if .PlannerModel}} · model {{.PlannerModel}}{{end}}{{if .TaskProposalMode}} · mode {{.TaskProposalMode}}{{if .ResolvedTaskProposalMode}} → {{.ResolvedTaskProposalMode}}{{end}}{{end}}{{if .SelectedTaskID}} · selected task {{.SelectedTaskID}}{{end}}</p>

          <h3>Generated State And Docs</h3>
          <ul>
          {{range .Docs}}
            <li>{{.Label}} · {{if .URL}}<a href="{{.URL}}">{{.Path}}</a>{{else}}{{.Path}}{{end}} <span class="muted">{{.Status}}</span></li>
          {{else}}
            <li class="muted">No generated state or doc links are available.</li>
          {{end}}
          </ul>

          <h3>Evaluation</h3>
          <p class="muted">status {{.Validation.Status}} · evidence {{.Validation.EvidenceStatus}} · commands {{.Validation.CommandCount}} · passed {{.Validation.PassedCount}} · failed {{.Validation.FailedCount}}</p>
          {{if .Validation.Reason}}<p class="muted">{{.Validation.Reason}}</p>{{end}}
          <p>{{if .Validation.SummaryURL}}<a href="{{.Validation.SummaryURL}}">Validation summary</a>{{else if .Validation.SummaryPath}}<span class="muted">Validation summary {{.Validation.SummaryPath}}</span>{{end}}{{if .Validation.ResultsURL}} · <a href="{{.Validation.ResultsURL}}">Validation results</a>{{else if .Validation.ResultsPath}} · <span class="muted">Validation results {{.Validation.ResultsPath}}</span>{{end}}</p>

          <h3>Codex</h3>
          <p class="muted">ran {{.Codex.Ran}} · skipped {{.Codex.Skipped}} · status {{.Codex.Status}}{{if .Codex.Model}} · model {{.Codex.Model}}{{end}} · exit {{.Codex.ExitCode}}{{if .Codex.Duration}} · duration {{.Codex.Duration}}{{end}}</p>
          <p>{{if .Codex.SummaryURL}}<a href="{{.Codex.SummaryURL}}">Codex summary</a>{{else if .Codex.SummaryPath}}<span class="muted">Codex summary {{.Codex.SummaryPath}}</span>{{end}}{{if .Codex.EventsURL}} · <a href="{{.Codex.EventsURL}}">Codex events</a>{{else if .Codex.EventsPath}} · <span class="muted">Codex events {{.Codex.EventsPath}}</span>{{end}}{{if .Codex.ExitURL}} · <a href="{{.Codex.ExitURL}}">Codex command metadata</a>{{else if .Codex.ExitPath}} · <span class="muted">Codex command metadata {{.Codex.ExitPath}}</span>{{end}}</p>

          <h3>Command Metadata</h3>
          <ul>
          {{range .Commands}}
            <li>
              <strong>{{.Source}}</strong> {{if .Label}}{{.Label}}{{end}} <span class="muted">{{.Provider}} {{.Name}} {{.Status}} exit {{.ExitCode}}{{if .Duration}} · {{.Duration}}{{end}}{{if .CWD}} · cwd {{.CWD}}{{end}}</span>
              {{if .Argv}}<div class="muted">argv {{range $i, $arg := .Argv}}{{if $i}} {{end}}<code>{{$arg}}</code>{{end}}</div>{{end}}
              {{if .StdoutURL}}<a href="{{.StdoutURL}}">stdout</a>{{else if .StdoutPath}}<span class="muted">stdout {{.StdoutPath}}</span>{{end}}{{if .StderrURL}} · <a href="{{.StderrURL}}">stderr</a>{{else if .StderrPath}} · <span class="muted">stderr {{.StderrPath}}</span>{{end}}
              {{if .Note}}<div class="muted">{{.Note}}</div>{{end}}
              {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
            </li>
          {{else}}
            <li class="muted">No sanitized command metadata recorded.</li>
          {{end}}
          </ul>

          <h3>Security Diagnostics</h3>
          <p class="muted">{{.SecuritySummary}}{{range .SecurityDetails}} · {{.}}{{end}}</p>

          <h3>Artifact Availability</h3>
          <ul>
          {{range .Artifacts}}
            <li>{{if .URL}}<a href="{{.URL}}">{{.Path}}</a>{{else}}{{.Path}}{{end}} <span class="muted">{{.Status}}</span></li>
          {{else}}
            <li class="muted">No manifest-listed artifacts available.</li>
          {{end}}
          </ul>
        </section>
      {{end}}
      </div>
    {{else if .RunDetail}}
      <p><a href="/">← dashboard</a> · <a href="/runs">all runs</a> · <a href="/runs/audit?run={{q .RunDetail.ID}}">Audit export</a>{{if eq .RunDetail.ManifestState "manifest available"}} · <a href="/runs/{{q .RunDetail.ID}}/manifest">Raw manifest</a>{{end}}</p>
      <section>
        <h2>Overview</h2>
        <p><strong>{{.RunDetail.ID}}</strong> <span class="muted">{{.RunDetail.Status}}</span></p>
        <p class="muted">started {{if .RunDetail.StartedAt}}{{.RunDetail.StartedAt}}{{else}}unknown{{end}} · finished {{if .RunDetail.FinishedAt}}{{.RunDetail.FinishedAt}}{{else}}unknown{{end}}{{if .RunDetail.Duration}} · duration {{.RunDetail.Duration}}{{end}} · dry-run {{.RunDetail.DryRun}}</p>
        <p class="muted">manifest {{.RunDetail.ManifestState}}</p>
        {{if .RunDetail.Error}}<p class="error">{{.RunDetail.Error}}</p>{{end}}
      </section>
      <section>
        <h2>Planner</h2>
        <p class="muted">provider {{if .RunDetail.PlannerProvider}}{{.RunDetail.PlannerProvider}}{{else}}unknown{{end}}{{if .RunDetail.PlannerModel}} · model {{.RunDetail.PlannerModel}}{{end}}{{if .RunDetail.TaskProposalMode}} · mode {{.RunDetail.TaskProposalMode}}{{if .RunDetail.ResolvedTaskProposalMode}} → {{.RunDetail.ResolvedTaskProposalMode}}{{end}}{{end}}{{if .RunDetail.SelectedTaskID}} · selected task {{.RunDetail.SelectedTaskID}}{{end}}</p>
        {{if .RunDetail.RepositorySummary}}<p class="muted">repository {{.RunDetail.RepositorySummary}}</p>{{end}}
      </section>
      <section>
        <h2>Generated State And Docs</h2>
        <ul>
        {{range .RunDetail.Docs}}
          <li>{{.Label}} · {{if .URL}}<a href="{{.URL}}">{{.Path}}</a>{{else}}{{.Path}}{{end}} <span class="muted">{{.Status}}</span></li>
        {{else}}
          <li class="muted">No generated state or doc links are available.</li>
        {{end}}
        </ul>
      </section>
      <section>
        <h2>Evaluation</h2>
        <p class="muted">status {{.RunDetail.Validation.Status}} · evidence {{.RunDetail.Validation.EvidenceStatus}} · commands {{.RunDetail.Validation.CommandCount}} · passed {{.RunDetail.Validation.PassedCount}} · failed {{.RunDetail.Validation.FailedCount}}</p>
        {{if .RunDetail.Validation.Reason}}<p class="muted">{{.RunDetail.Validation.Reason}}</p>{{end}}
        {{if .RunDetail.Validation.Summary}}<p>{{.RunDetail.Validation.Summary}}</p>{{end}}
        <p>{{if .RunDetail.Validation.SummaryURL}}<a href="{{.RunDetail.Validation.SummaryURL}}">Validation summary</a>{{else if .RunDetail.Validation.SummaryPath}}<span class="muted">Validation summary {{.RunDetail.Validation.SummaryPath}}</span>{{end}}{{if .RunDetail.Validation.ResultsURL}} · <a href="{{.RunDetail.Validation.ResultsURL}}">Validation results</a>{{else if .RunDetail.Validation.ResultsPath}} · <span class="muted">Validation results {{.RunDetail.Validation.ResultsPath}}</span>{{end}}</p>
      </section>
      <section>
        <h2>Codex</h2>
        <p class="muted">ran {{.RunDetail.Codex.Ran}} · skipped {{.RunDetail.Codex.Skipped}} · status {{.RunDetail.Codex.Status}}{{if .RunDetail.Codex.Model}} · model {{.RunDetail.Codex.Model}}{{end}} · exit {{.RunDetail.Codex.ExitCode}}{{if .RunDetail.Codex.Duration}} · duration {{.RunDetail.Codex.Duration}}{{end}}</p>
        {{if .RunDetail.Codex.Error}}<p class="error">{{.RunDetail.Codex.Error}}</p>{{end}}
        <p>{{if .RunDetail.Codex.SummaryURL}}<a href="{{.RunDetail.Codex.SummaryURL}}">Codex summary</a>{{else if .RunDetail.Codex.SummaryPath}}<span class="muted">Codex summary {{.RunDetail.Codex.SummaryPath}}</span>{{end}}{{if .RunDetail.Codex.EventsURL}} · <a href="{{.RunDetail.Codex.EventsURL}}">Codex events</a>{{else if .RunDetail.Codex.EventsPath}} · <span class="muted">Codex events {{.RunDetail.Codex.EventsPath}}</span>{{end}}{{if .RunDetail.Codex.ExitURL}} · <a href="{{.RunDetail.Codex.ExitURL}}">Codex command metadata</a>{{else if .RunDetail.Codex.ExitPath}} · <span class="muted">Codex command metadata {{.RunDetail.Codex.ExitPath}}</span>{{end}}</p>
      </section>
      <section>
        <h2>Command Metadata</h2>
        <ul>
        {{range .RunDetail.Commands}}
          <li>
            <strong>{{.Source}}</strong> {{if .Label}}{{.Label}}{{end}} <span class="muted">{{.Provider}} {{.Name}} {{.Status}} exit {{.ExitCode}}{{if .Duration}} · {{.Duration}}{{end}}{{if .CWD}} · cwd {{.CWD}}{{end}}</span>
            {{if .Argv}}<div class="muted">argv {{range $i, $arg := .Argv}}{{if $i}} {{end}}<code>{{$arg}}</code>{{end}}</div>{{end}}
            {{if .StdoutURL}}<a href="{{.StdoutURL}}">stdout</a>{{else if .StdoutPath}}<span class="muted">stdout {{.StdoutPath}}</span>{{end}}{{if .StderrURL}} · <a href="{{.StderrURL}}">stderr</a>{{else if .StderrPath}} · <span class="muted">stderr {{.StderrPath}}</span>{{end}}
            {{if .Note}}<div class="muted">{{.Note}}</div>{{end}}
            {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
          </li>
        {{else}}
          <li class="muted">No sanitized command metadata recorded.</li>
        {{end}}
        </ul>
      </section>
      <section>
        <h2>Security Diagnostics</h2>
        <p class="muted">{{.RunDetail.SecuritySummary}}{{range .RunDetail.SecurityDetails}} · {{.}}{{end}}</p>
      </section>
      <section>
        <h2>Artifacts</h2>
        {{if .RunDetail.ArtifactNote}}<p class="muted">{{.RunDetail.ArtifactNote}}</p>{{end}}
        <ul>
        {{range .RunDetail.Artifacts}}
          <li>{{if .URL}}<a href="{{.URL}}">{{.Path}}</a>{{else}}{{.Path}}{{end}} <span class="muted">{{.Status}}</span></li>
        {{else}}
          <li class="muted">No manifest-listed artifacts available.</li>
        {{end}}
        </ul>
      </section>
      {{if .RunDetail.NextActions}}
      <section>
        <h2>Next Actions</h2>
        <ul>{{range .RunDetail.NextActions}}<li>{{.}}</li>{{end}}</ul>
      </section>
      {{end}}
    {{else if .RunsOnly}}
      <p><a href="/">← dashboard</a></p>
      <h2>Filters</h2>
      <form class="filters" method="get" action="/runs">
        <label>status
          <select name="status">
            <option value="">any</option>
            {{range .RunFilterOptions.Statuses}}
              <option value="{{.}}" {{if eq . $.RunFilters.Status}}selected{{end}}>{{.}}</option>
            {{end}}
          </select>
        </label>
        <label>dry-run
          <select name="dry_run">
            <option value="">any</option>
            {{range .RunFilterOptions.DryRunModes}}
              <option value="{{.Value}}" {{if eq .Value $.RunFilters.DryRun}}selected{{end}}>{{.Label}}</option>
            {{end}}
          </select>
        </label>
        <label>planner provider
          <select name="planner_provider">
            <option value="">any</option>
            {{range .RunFilterOptions.PlannerProviders}}
              <option value="{{.}}" {{if eq . $.RunFilters.PlannerProvider}}selected{{end}}>{{.}}</option>
            {{end}}
          </select>
        </label>
        <label>evaluation
          <select name="evaluation">
            <option value="">any</option>
            {{range .RunFilterOptions.Evaluations}}
              <option value="{{.}}" {{if eq . $.RunFilters.Evaluation}}selected{{end}}>{{.}}</option>
            {{end}}
          </select>
        </label>
        <label>run id
          <input name="q" value="{{.RunFilters.Query}}" placeholder="substring">
        </label>
        <div class="actions">
          <button class="button primary" type="submit">Apply Filters</button>
          <a class="button" href="/runs">Clear</a>
        </div>
      </form>
      {{if .RunFilters.Notice}}<p class="muted">{{.RunFilters.Notice}}</p>{{end}}
      {{if .RunFilters.HasActive}}<p class="muted">Filtered run history.</p>{{end}}
      <h2>Runs</h2>
      <ul>
      {{range .Runs}}
        <li><a href="/runs/{{q .ID}}">{{.ID}}</a> <span class="muted">{{.Status}} {{.StartedAt}} {{.PlannerProvider}} · dry-run {{.DryRun}}{{if .Validation}} · validation {{.Validation}}{{end}}{{if .TaskProposalMode}} · mode {{.TaskProposalMode}}{{if .ResolvedTaskProposalMode}} → {{.ResolvedTaskProposalMode}}{{end}}{{end}}{{if .SecuritySummary}} · {{.SecuritySummary}}{{end}}</span>{{if .CompareURL}} · <a href="{{.CompareURL}}">compare</a>{{end}}{{if .ErrorSummary}} <span class="error">{{.ErrorSummary}}</span>{{end}}</li>
      {{else}}
        <li class="muted">No jj runs found.</li>
      {{end}}
      </ul>
    {{else if or .Content .Rendered}}
      <p><a href="/">← index</a>{{if .RunID}} · <a href="/runs/{{q .RunID}}">run {{.RunID}}</a>{{end}}</p>
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
	            <p><a href="/runs/{{q .ID}}">{{.ID}}</a> <span class="muted">{{.Status}} {{.StartedAt}}</span></p>
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
	            <li>{{if .Invalid}}<strong>{{.ID}}</strong>{{else}}<a href="/runs/{{q .ID}}">{{.ID}}</a>{{end}} <span class="muted">{{.Status}} {{.StartedAt}} {{.PlannerProvider}}{{if .Validation}} · validation {{.Validation}}{{end}}{{if .TaskProposalMode}} · mode {{.TaskProposalMode}}{{if .ResolvedTaskProposalMode}} → {{.ResolvedTaskProposalMode}}{{end}}{{end}}{{if .SecuritySummary}} · {{.SecuritySummary}}{{end}}</span>{{if not .Invalid}} <a href="/runs/{{q .ID}}/manifest">manifest</a>{{end}}{{if .ErrorSummary}} <span class="error">{{.ErrorSummary}}</span>{{else if .RiskSummary}} <span class="muted">{{.RiskSummary}}</span>{{end}}</li>
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
