package serve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runpkg "github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/security"
)

func TestSecurityRegressionServeGuardedDocsAndArtifacts(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260428-010203-secserve"
	secret := "serve-regression-secret-value"
	t.Setenv("JJ_SERVE_REGRESSION_TOKEN", secret)
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"started_at": "2026-04-28T01:02:03Z",
		"artifacts": {
			"manifest": "manifest.json",
			"validation_summary": "validation/summary.md",
			"missing": "validation/missing.md"
		},
		"errors": ["Authorization: Bearer %s [REDACTED]"],
		"validation": {"status":"passed","summary_path":"validation/summary.md"}
	}`, runID, secret))
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/summary.md", "Authorization: Bearer "+secret+"\nprovider returned [omitted] and {removed}\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/unlisted.md", "token="+secret+"\n")
	outside := t.TempDir()
	outsideDoc := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsideDoc, []byte("outside "+secret+"\n"), 0o644); err != nil {
		t.Fatalf("write outside doc: %v", err)
	}
	server := newTestServer(t, dir, "")

	allowed := []struct {
		target string
		want   string
	}{
		{"/doc?path=README.md", "<h1>Root</h1>"},
		{"/doc?path=.jj/tasks.json", "TASK-0001"},
		{"/runs/" + runID + "/manifest", security.RedactionMarker},
		{"/artifact?run=" + runID + "&path=validation/summary.md", security.RedactionMarker},
	}
	for _, tc := range allowed {
		t.Run("allowed "+tc.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK || !strings.Contains(body, tc.want) {
				t.Fatalf("expected allowed response containing %q, got status=%d body=%s", tc.want, rec.Code, body)
			}
			assertServeBodyClean(t, body, dir, outsideDoc, secret)
		})
	}

	denied := []struct {
		name   string
		target string
		status int
	}{
		{name: "relative traversal", target: "/artifact?run=" + runID + "&path=../manifest.json", status: http.StatusForbidden},
		{name: "nested traversal", target: "/artifact?run=" + runID + "&path=validation/../manifest.json", status: http.StatusForbidden},
		{name: "encoded traversal", target: "/artifact?run=" + runID + "&path=validation%2f..%2fmanifest.json", status: http.StatusForbidden},
		{name: "absolute escape", target: "/artifact?run=" + runID + "&path=" + url.QueryEscape(outsideDoc), status: http.StatusForbidden},
		{name: "unlisted artifact", target: "/artifact?run=" + runID + "&path=validation/unlisted.md", status: http.StatusForbidden},
		{name: "listed missing artifact", target: "/artifact?run=" + runID + "&path=validation/missing.md", status: http.StatusNotFound},
		{name: "unlisted project doc", target: "/doc?path=docs/PRIVATE.md", status: http.StatusForbidden},
		{name: "encoded project traversal", target: "/doc?path=docs%2f..%2fREADME.md", status: http.StatusForbidden},
		{name: "unsafe route path", target: "/docs/../README.md", status: http.StatusForbidden},
	}
	for _, probe := range denied {
		t.Run(probe.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, probe.target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != probe.status {
				t.Fatalf("status for %s = %d, want %d body=%s", probe.target, rec.Code, probe.status, body)
			}
			assertServeBodyClean(t, body, dir, outsideDoc, secret)
		})
	}

	if err := os.Remove(filepath.Join(dir, "docs", "SPEC.md")); err != nil {
		t.Fatalf("remove spec doc: %v", err)
	}
	if err := os.Symlink(outsideDoc, filepath.Join(dir, "docs", "SPEC.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/doc?path=docs/SPEC.md", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("symlinked doc status = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	assertServeBodyClean(t, rec.Body.String(), dir, outsideDoc, secret)

	linkTarget := filepath.Join(outside, "summary.md")
	if err := os.WriteFile(linkTarget, []byte("linked "+secret+"\n"), 0o644); err != nil {
		t.Fatalf("write outside summary: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".jj", "runs", runID, "validation", "summary.md")); err != nil {
		t.Fatalf("remove summary artifact: %v", err)
	}
	if err := os.Symlink(linkTarget, filepath.Join(dir, ".jj", "runs", runID, "validation", "summary.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/artifact?run="+runID+"&path=validation/summary.md", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("symlinked artifact status = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	assertServeBodyClean(t, rec.Body.String(), dir, linkTarget, secret)
}

func TestSecurityRegressionWebRunLogsRedactSecretsBeforeStatusAndPersistence(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "web-run-regression-secret-value"
	t.Setenv("JJ_WEB_RUN_REGRESSION_TOKEN", secret)
	server := newTestServerWithExecutor(t, Config{
		CWD:  dir,
		Addr: "127.0.0.1:7331",
		RunExecutor: func(_ context.Context, cfg runpkg.Config) (*runpkg.Result, error) {
			runDir := filepath.Join(cfg.CWD, ".jj", "runs", cfg.RunID)
			manifest := fmt.Sprintf(`{"run_id":%q,"status":"complete","started_at":"2026-04-28T00:00:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed"},"commit":{"ran":false,"status":"skipped"}}`, cfg.RunID)
			if err := writeFakeRunFile(runDir, "manifest.json", manifest); err != nil {
				return nil, err
			}
			fmt.Fprintf(cfg.Stdout, "jj: creating run directory %s\nOPENAI_API_KEY=%s\n", runDir, secret)
			fmt.Fprintf(cfg.Stderr, "Authorization: Bearer %s\n", secret)
			return &runpkg.Result{RunID: cfg.RunID, RunDir: runDir}, nil
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartPromptForm("web-run-redaction", "Build from prompt.\n", true, false, false, 0)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	waitForRunStatus(t, server, "web-run-redaction", runpkg.StatusComplete)
	body := getRunStatusBody(t, server, "web-run-redaction")
	assertServeBodyClean(t, body, dir, filepath.Join(dir, ".jj", "runs", "web-run-redaction"), secret)
	if !strings.Contains(body, security.RedactionMarker) || !strings.Contains(body, "[path]") {
		t.Fatalf("status response should retain redaction/path evidence:\n%s", body)
	}

	logPath := filepath.Join(dir, ".jj", "runs", "web-run-redaction", "web-run.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read web-run log: %v", err)
	}
	logText := string(data)
	assertServeBodyClean(t, logText, dir, logPath, secret)
	if !strings.Contains(logText, security.RedactionMarker) || !strings.Contains(logText, "[path]") {
		t.Fatalf("persisted web-run log should retain redaction/path evidence:\n%s", logText)
	}
}

func TestSecurityRegressionDashboardShowsSafeSecurityDiagnostics(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260428-030405-diag"
	secret := "dashboard-diagnostic-secret-value"
	t.Setenv("JJ_DASHBOARD_DIAGNOSTIC_TOKEN", secret)
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"started_at": "2026-04-28T03:04:05Z",
		"artifacts": {"manifest": "manifest.json"},
		"validation": {"status": "passed"},
		"security": {
			"redaction_applied": true,
			"workspace_guardrails_applied": true,
			"redaction_count": 7,
			"diagnostics": {
				"version": "1",
				"redacted": true,
				"secret_material_present": true,
				"root_labels": ["workspace", "run_artifacts", "token=%s"],
				"guarded_roots": [
					{"label": "workspace", "path": "[workspace]"},
					{"label": "escape", "path": %q}
				],
				"denied_path_count": 2,
				"denied_path_categories": ["outside_workspace", "token=%s"],
				"denied_path_category_counts": {"outside_workspace": 1, "token=%s": 1},
				"failure_categories": ["symlink_path"],
				"failure_category_counts": {"symlink_path": 1},
				"command_record_count": 2,
				"command_metadata_sanitized": true,
				"command_argv_sanitized": true,
				"command_cwd_label": "[workspace]",
				"command_sanitization_status": "sanitized",
				"raw_command_text_persisted": false,
				"raw_environment_persisted": false,
				"dry_run_parity_applied": true,
				"dry_run_parity_status": "equivalent"
			}
		}
	}`, runID, secret, filepath.Join(dir, "outside-"+secret), secret, secret))
	server := newTestServer(t, dir, "")

	for _, target := range []string{"/", "/runs"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rec.Code, body)
			}
			for _, want := range []string{"security redactions 7", "denied paths 2", "command metadata sanitized", "dry-run parity equivalent"} {
				if !strings.Contains(body, want) {
					t.Fatalf("dashboard missing security diagnostic %q:\n%s", want, body)
				}
			}
			if target == "/" {
				for _, want := range []string{"roots", "denied categories"} {
					if !strings.Contains(body, want) {
						t.Fatalf("dashboard missing security diagnostic detail %q:\n%s", want, body)
					}
				}
			}
			for _, leaked := range []string{secret, filepath.Join(dir, "outside-"+secret), filepath.ToSlash(filepath.Join(dir, "outside-"+secret)), security.RedactionMarker, "token="} {
				if strings.Contains(body, leaked) {
					t.Fatalf("dashboard security diagnostics leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func assertServeBodyClean(t *testing.T, body, workspacePath, extraPath, secret string) {
	t.Helper()
	for _, leaked := range []string{
		secret,
		workspacePath,
		filepath.ToSlash(workspacePath),
		extraPath,
		filepath.ToSlash(extraPath),
		"Bearer [jj-omitted]",
		"[REDACTED]",
		"[redacted]",
		"[omitted]",
		"<hidden>",
		"{removed}",
	} {
		if leaked != "" && strings.Contains(body, leaked) {
			t.Fatalf("served response leaked %q:\n%s", leaked, body)
		}
	}
}
