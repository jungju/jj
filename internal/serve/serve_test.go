package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	runpkg "github.com/jungju/jj/internal/run"
)

func TestIndexShowsDocsAndRuns(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "20260425-120000-bbbbbb")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "README.md") || !strings.Contains(body, "plan.md") || !strings.Contains(body, "playground/plan.md") {
		t.Fatalf("index missing docs:\n%s", body)
	}
	for _, want := range []string{"Workspace Readiness", "Risks And Failures", "Plan Ready", "README Ready", "SPEC Ready", "TASK Ready", `href="/runs"`, "Raw manifest"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q:\n%s", want, body)
		}
	}
	first := strings.Index(body, "20260425-120000-bbbbbb")
	second := strings.Index(body, "20260425-110000-aaaaaa")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("selected/latest run should appear first:\n%s", body)
	}
}

func TestIndexShowsMissingReadiness(t *testing.T) {
	dir := t.TempDir()
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{"Plan Missing", "README Missing", "SPEC Missing", "TASK Missing", "Open Runs"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q:\n%s", want, body)
		}
	}
}

func TestResolveConfigUsesServeJJRC(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".jjrc", `{"serve_host":"localhost","serve_port":0}`)

	cfg, err := ResolveConfig(Config{CWD: dir, ConfigSearchDir: dir})
	if err != nil {
		t.Fatalf("resolve serve config: %v", err)
	}
	if cfg.Addr != "localhost:0" || cfg.ConfigFile != filepath.Join(dir, ".jjrc") {
		t.Fatalf("unexpected serve config: %#v", cfg)
	}
}

func TestResolveConfigEnvOverridesServeJJRC(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".jjrc", `{"serve_addr":"127.0.0.1:7331"}`)
	t.Setenv("JJ_SERVE_ADDR", "127.0.0.1:0")

	cfg, err := ResolveConfig(Config{CWD: dir, ConfigSearchDir: dir})
	if err != nil {
		t.Fatalf("resolve serve config: %v", err)
	}
	if cfg.Addr != "127.0.0.1:0" {
		t.Fatalf("env should override .jjrc addr: %#v", cfg)
	}
}

func TestResolveConfigUsesEnvTargetCWDServeJJRC(t *testing.T) {
	invocation := t.TempDir()
	target := t.TempDir()
	writeFile(t, target, ".jjrc", `{"serve_host":"localhost","serve_port":0}`)
	t.Setenv("JJ_CWD", target)

	cfg, err := ResolveConfig(Config{ConfigSearchDir: invocation})
	if err != nil {
		t.Fatalf("resolve serve config: %v", err)
	}
	if cfg.CWD != target || cfg.Addr != "localhost:0" || cfg.ConfigFile != filepath.Join(target, ".jjrc") {
		t.Fatalf("expected env target serve .jjrc to apply: %#v", cfg)
	}
}

func TestServeExposesRunMutationRoutes(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/run/new", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /run/new to render, got %d body=%s", rec.Code, body)
	}
	for _, want := range []string{`action="/run/start"`, "auto continue turns", "max turns"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/run/new missing %q:\n%s", want, body)
		}
	}
}

func TestDocShowsRedactedMarkdown(t *testing.T) {
	dir := newTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("token sk-proj-abcdef1234567890\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/doc?path=README.md", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, "sk-proj") || !strings.Contains(body, "[redacted-openai-key]") {
		t.Fatalf("doc was not redacted:\n%s", body)
	}
}

func TestDocRendersMarkdownAndEscapesHTML(t *testing.T) {
	dir := newTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Title\n\n<script>alert(1)</script>\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/doc?path=README.md", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "<h1>Title</h1>") {
		t.Fatalf("markdown heading was not rendered:\n%s", body)
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("HTML was not escaped:\n%s", body)
	}
}

func TestArtifactShowsRunFile(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifact?run=20260425-120000-bbbbbb&path=docs/TASK.md", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "Do the task") {
		t.Fatalf("artifact body missing task:\n%s", body)
	}
}

func TestArtifactRejectsUnlistedRunFile(t *testing.T) {
	dir := newTestWorkspace(t)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/docs/UNLISTED.md", "# Hidden\n")
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifact?run=20260425-120000-bbbbbb&path=docs/UNLISTED.md", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Fatalf("expected unlisted artifact rejection, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRunShowsDocsArtifactsFirst(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/run?id=20260425-120000-bbbbbb", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	spec := strings.Index(body, "docs/SPEC.md")
	task := strings.Index(body, "docs/TASK.md")
	eval := strings.Index(body, "docs/EVAL.md")
	if spec < 0 || task < 0 || eval < 0 || !(spec < task && task < eval) {
		t.Fatalf("docs artifacts missing or not first in expected order:\n%s", body)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	for _, target := range []string{
		"/doc?path=../README.md",
		"/artifact?run=20260425-120000-bbbbbb&path=../manifest.json",
		"/artifact?run=20260425-120000-bbbbbb&path=docs/../manifest.json",
		"/artifact?run=20260425-120000-bbbbbb&path=docs%2f..%2fmanifest.json",
		"/artifact?run=20260425-120000-bbbbbb&path=%2e%2e",
		"/artifact?run=20260425-120000-bbbbbb&path=.secret/../manifest.json",
		"/artifact?run=20260425-120000-bbbbbb&path=/etc/passwd",
		"/artifact?run=20260425-120000-bbbbbb&path=C:/secret.txt",
		"/artifact?run=20260425-120000-bbbbbb&path=docs%5c..%5cmanifest.json",
		"/artifact?run=20260425-120000-bbbbbb&path=docs/TASK.md%00",
		"/artifact?run=20260425-120000-bbbbbb&path=docs/.secret",
		"/artifact?run=20260425-120000-bbbbbb&path=.secret/file.md",
		"/artifact?run=20260425-120000-bbbbbb&path=",
		"/run?id=../bad",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		server.Handler().ServeHTTP(rec, req)
		if rec.Code < 400 {
			t.Fatalf("expected rejection for %s, got %d", target, rec.Code)
		}
	}
}

func TestSymlinkTraversalRejected(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	outsideDoc := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsideDoc, []byte("# Outside\n"), 0o644); err != nil {
		t.Fatalf("write outside doc: %v", err)
	}
	linkPath := filepath.Join(dir, "docs", "outside.md")
	if err := os.Symlink(outsideDoc, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/doc?path=docs/outside.md", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Fatalf("expected symlink escape rejection, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExecuteStartsAndStopsWithContext(t *testing.T) {
	dir := newTestWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out strings.Builder
	errCh := make(chan error, 1)
	go func() {
		errCh <- Execute(ctx, Config{CWD: dir, Addr: "127.0.0.1:0", Stdout: &out})
	}()

	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), "jj: serving docs at http://") {
		select {
		case err := <-errCh:
			t.Fatalf("server exited early: %v", err)
		case <-deadline:
			t.Fatal("server did not start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("execute should stop cleanly on context cancel: %v", err)
	}
}

func TestWebRunAutoContinuesUntilPass(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	executor := &loopFakeExecutor{
		results: []string{runpkg.StatusPartial, runpkg.StatusSuccess},
		evals:   []string{"PARTIAL", "PASS"},
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartForm("loop-pass", false, true, true, 3)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	status := waitForRunStatus(t, server, "loop-pass", "success")
	if len(status.Turns) != 2 || status.CurrentTurn.RunID != "loop-pass-t02" {
		t.Fatalf("expected two turns and current t02, got %#v", status)
	}
	if !strings.Contains(executor.contextFor("loop-pass-t02"), "Previous Manifest") {
		t.Fatalf("second turn did not receive continuation context: %q", executor.contextFor("loop-pass-t02"))
	}
}

func TestWebRunFinishStopsAfterCurrentTurn(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	release := make(chan struct{})
	executor := &loopFakeExecutor{
		results: []string{runpkg.StatusPartial, runpkg.StatusSuccess},
		evals:   []string{"PARTIAL", "PASS"},
		block:   release,
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartForm("loop-finish", false, true, true, 3)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}
	waitForRunStatus(t, server, "loop-finish", "running")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/run/finish", strings.NewReader("id=loop-finish"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d body=%s", rec.Code, rec.Body.String())
	}
	close(release)

	status := waitForRunStatus(t, server, "loop-finish", runpkg.StatusPartial)
	if len(status.Turns) != 1 || !status.FinishRequested || status.StopReason != "finish requested" {
		t.Fatalf("expected finish after one turn, got %#v", status)
	}
}

func TestWebRunAutoContinueValidation(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: (&loopFakeExecutor{}).Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartForm("dry-loop", true, true, true, 3)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "auto continue turns requires full-run") {
		t.Fatalf("expected dry-run auto continue rejection, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartForm("no-confirm", false, false, true, 3)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "full run requires explicit confirmation") {
		t.Fatalf("expected confirmation rejection, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebRunSingleFullRunDoesNotEnableCommit(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	executor := &loopFakeExecutor{
		results: []string{runpkg.StatusSuccess},
		evals:   []string{"PASS"},
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartForm("single-full", false, true, false, 0)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}
	status := waitForRunStatus(t, server, "single-full", runpkg.StatusComplete)
	if len(status.Turns) != 1 {
		t.Fatalf("expected one turn, got %#v", status)
	}
	call := executor.callFor("single-full")
	if call.CommitOnSuccess || call.CommitMessage != "" {
		t.Fatalf("expected web full-run to leave commit disabled, got %#v", call)
	}
}

func TestWebRunAutoContinueAllowsDirtyWorkspace(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	writeFile(t, dir, "dirty.txt", "dirty\n")
	executor := &loopFakeExecutor{
		results: []string{runpkg.StatusSuccess},
		evals:   []string{"PASS"},
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartForm("dirty-loop", false, true, true, 3)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected dirty workspace to start, got %d body=%s", rec.Code, rec.Body.String())
	}
	status := waitForRunStatus(t, server, "dirty-loop", "success")
	if len(status.Turns) != 1 {
		t.Fatalf("expected one successful turn, got %#v", status)
	}
}

func newTestServer(t *testing.T, dir, runID string) *Server {
	t.Helper()
	server, err := New(dir, runID)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func newTestServerWithExecutor(t *testing.T, cfg Config) *Server {
	t.Helper()
	server, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func getRunStatus(t *testing.T, server *Server, runID string) webRunView {
	t.Helper()
	body := getRunStatusBody(t, server, runID)
	var status webRunView
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode status: %v\n%s", err, body)
	}
	return status
}

func getRunStatusBody(t *testing.T, server *Server, runID string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/run/status?id="+runID, nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func waitForRunStatus(t *testing.T, server *Server, runID, want string) webRunView {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := getRunStatus(t, server, runID)
		if status.Status == want {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := getRunStatus(t, server, runID)
	t.Fatalf("status for %s did not become %s; got %#v", runID, want, status)
	return webRunView{}
}

func containsLine(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

type loopFakeExecutor struct {
	mu      sync.Mutex
	calls   []runpkg.Config
	results []string
	evals   []string
	block   <-chan struct{}
	blocked bool
}

func (f *loopFakeExecutor) Run(ctx context.Context, cfg runpkg.Config) (*runpkg.Result, error) {
	f.mu.Lock()
	callIndex := len(f.calls)
	f.calls = append(f.calls, cfg)
	block := f.block
	if block != nil && !f.blocked {
		f.blocked = true
	} else {
		block = nil
	}
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-block:
		}
	}
	status := runpkg.StatusSuccess
	if callIndex < len(f.results) && f.results[callIndex] != "" {
		status = f.results[callIndex]
	}
	eval := "PASS"
	if callIndex < len(f.evals) && f.evals[callIndex] != "" {
		eval = f.evals[callIndex]
	}
	runDir := filepath.Join(cfg.CWD, ".jj", "runs", cfg.RunID)
	if err := writeFakeRunFile(runDir, "docs/SPEC.md", "# SPEC\n"); err != nil {
		return nil, err
	}
	if err := writeFakeRunFile(runDir, "docs/TASK.md", "# TASK\n"); err != nil {
		return nil, err
	}
	if err := writeFakeRunFile(runDir, "docs/EVAL.md", "# EVAL\n\n## Result\n\n"+eval+"\n"); err != nil {
		return nil, err
	}
	if err := writeFakeRunFile(runDir, "git/diff-summary.txt", "## git diff --stat\nfake.go\n"); err != nil {
		return nil, err
	}
	if err := writeFakeRunFile(runDir, "codex/summary.md", "Changed files: fake.go\n"); err != nil {
		return nil, err
	}
	manifest := fmt.Sprintf(`{"run_id":%q,"status":%q,"started_at":"2026-04-25T00:00:00Z","finished_at":"2026-04-25T00:00:01Z","artifacts":{"manifest":"manifest.json","spec":"docs/SPEC.md","task":"docs/TASK.md","eval":"docs/EVAL.md","git_diff_summary":"git/diff-summary.txt","codex_summary":"codex/summary.md"},"evaluation":{"ran":true,"result":%q,"score":80},"commit":{"ran":false,"status":"skipped"}}`, cfg.RunID, status, eval)
	if err := writeFakeRunFile(runDir, "manifest.json", manifest); err != nil {
		return nil, err
	}
	return &runpkg.Result{RunID: cfg.RunID, RunDir: runDir}, nil
}

func (f *loopFakeExecutor) contextFor(runID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.RunID == runID {
			return call.AdditionalPlanContext
		}
	}
	return ""
}

func (f *loopFakeExecutor) callFor(runID string) runpkg.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.RunID == runID {
			return call
		}
	}
	return runpkg.Config{}
}

func writeFakeRunFile(runDir, rel, data string) error {
	path := filepath.Join(runDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o644)
}

func runStartForm(runID string, dryRun, confirm, autoContinue bool, maxTurns int) string {
	values := "plan_path=plan.md&cwd=&run_id=" + runID + "&planning_agents=1&spec_doc=docs%2FSPEC.md&task_doc=docs%2FTASK.md&eval_doc=docs%2FEVAL.md"
	if dryRun {
		values += "&dry_run=true"
	}
	if confirm {
		values += "&confirm_full_run=true"
	}
	if autoContinue {
		values += "&auto_continue=true"
	}
	if maxTurns > 0 {
		values += fmt.Sprintf("&max_turns=%d", maxTurns)
	}
	return values
}

func newCleanGitWorkspace(t *testing.T) string {
	t.Helper()
	dir := newTestWorkspace(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	runServeGit(t, dir, "init")
	runServeGit(t, dir, "config", "user.email", "jj@example.com")
	runServeGit(t, dir, "config", "user.name", "jj test")
	runServeGit(t, dir, "add", "--all")
	runServeGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runServeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func newTestWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Root\n")
	writeFile(t, dir, "plan.md", "# Product Plan\n")
	writeFile(t, dir, "docs/SPEC.md", "# SPEC\n")
	writeFile(t, dir, "docs/TASK.md", "# TASK\n")
	writeFile(t, dir, "docs/guide.md", "# Guide\n")
	writeFile(t, dir, "playground/plan.md", "# Plan\n")
	writeFile(t, dir, ".git/ignored.md", "# ignored\n")
	writeFile(t, dir, ".jj/runs/20260425-110000-aaaaaa/manifest.json", `{"run_id":"20260425-110000-aaaaaa","status":"success","started_at":"2026-04-25T11:00:00Z","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/manifest.json", `{"run_id":"20260425-120000-bbbbbb","status":"failed","started_at":"2026-04-25T12:00:00Z","artifacts":{"manifest":"manifest.json","spec":"docs/SPEC.md","task":"docs/TASK.md","eval":"docs/EVAL.md"},"risks":["review required"]}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/docs/SPEC.md", "# SPEC\n\nDo the spec.\n")
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/docs/TASK.md", "# TASK\n\nDo the task.\n")
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/docs/EVAL.md", "# EVAL\n\nPASS.\n")
	return dir
}

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
