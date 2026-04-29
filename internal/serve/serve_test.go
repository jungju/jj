package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	runpkg "github.com/jungju/jj/internal/run"
	"github.com/jungju/jj/internal/security"
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
	if !strings.Contains(body, "README.md") || !strings.Contains(body, "plan.md") || !strings.Contains(body, "docs/SPEC.md") || !strings.Contains(body, "docs/TASK.md") || !strings.Contains(body, ".jj/spec.json") || !strings.Contains(body, ".jj/tasks.json") || strings.Contains(body, ".jj/eval.json") {
		t.Fatalf("index missing docs:\n%s", body)
	}
	for _, blocked := range []string{"docs/guide.md", "playground/plan.md"} {
		if strings.Contains(body, blocked) {
			t.Fatalf("index advertised non-allowlisted doc %q:\n%s", blocked, body)
		}
	}
	for _, want := range []string{"Workspace Readiness", "Risks And Failures", "Plan Ready", "README Ready", "SPEC Ready", "TASK Ready", "Latest Run", `href="/runs"`, `href="/runs/20260425-120000-bbbbbb"`, `href="/runs/audit?run=20260425-120000-bbbbbb"`, "provider/result result failed", "evaluation failed", "mode auto"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q:\n%s", want, body)
		}
	}
	latest := htmlSection(body, "Latest Run", "Risks And Failures")
	for _, blocked := range []string{"Raw manifest", "Repository:", "Task Proposal Mode:", "Validation artifact", "ghp_dashboardsecret1234567890"} {
		if strings.Contains(latest, blocked) {
			t.Fatalf("latest-run summary leaked extra field %q:\n%s", blocked, latest)
		}
	}
	if strings.Contains(body, "ghp_dashboardsecret1234567890") {
		t.Fatalf("dashboard leaked repository token:\n%s", body)
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

func TestDashboardProjectDocsShortcutsPresentAndPreserveSummaries(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-projectdocs1234567890"
	writeFile(t, dir, "plan.md", "# Plan\n\n"+secret+"\n")
	writeFile(t, dir, "README.md", "# README\n\n"+secret+"\n")
	writeFile(t, dir, "docs/SPEC.md", "# SPEC\n\n"+secret+"\n")
	writeFile(t, dir, "docs/TASK.md", `# Work Queue

- [~] TASK-0045 [feature] Show guarded project document shortcuts

raw document body
Authorization: Bearer `+secret+`
`)
	writeFile(t, dir, "docs/EVAL.md", "# Eval\n\n"+secret+"\n")
	writeFile(t, dir, ".jj/runs/20260429-120000-docs/manifest.json", `{
		"run_id": "20260429-120000-docs",
		"status": "complete",
		"started_at": "2026-04-29T12:00:00Z",
		"planner_provider": "codex",
		"artifacts": {"manifest": "manifest.json"},
		"validation": {"status": "passed", "evidence_status": "recorded"}
	}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}

	projectDocs := htmlSection(body, "Project Docs", "Workspace Readiness")
	for _, rel := range []string{"plan.md", "docs/SPEC.md", "docs/TASK.md", "docs/EVAL.md", "README.md"} {
		want := `href="` + docURL(rel) + `">` + rel + `</a> <span class="muted">available</span>`
		if !strings.Contains(projectDocs, want) {
			t.Fatalf("project docs missing available shortcut %q:\n%s", want, projectDocs)
		}
	}
	for _, leaked := range []string{secret, "raw document body", "Authorization: Bearer", security.RedactionMarker, "[omitted]"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("dashboard project docs leaked %q:\n%s", leaked, body)
		}
	}

	taskSection := htmlSection(body, "Current TASK", "Latest Run")
	for _, want := range []string{"TASK.md: 1 total, 0 done, 1 in progress, 0 pending, 0 blocked.", "TASK-0045", "feature", "in-progress", "Show guarded project document shortcuts"} {
		if !strings.Contains(taskSection, want) {
			t.Fatalf("TASK summary changed, missing %q:\n%s", want, taskSection)
		}
	}
	latest := htmlSection(body, "Latest Run", "Risks And Failures")
	for _, want := range []string{"20260429-120000-docs", "complete", "provider/result codex", "evaluation passed (recorded)"} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest-run summary changed, missing %q:\n%s", want, latest)
		}
	}
	next := htmlSection(body, "Next Action", "Project Docs")
	for _, want := range []string{"Continue Task", "continue_task", "TASK-0045", "feature", "in-progress"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next-action summary changed, missing %q:\n%s", want, next)
		}
	}
}

func TestDashboardProjectDocsShortcutsMissingUnavailableAndDeniedAreSafe(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := "sk-proj-projectdocdeny1234567890"
	writeFile(t, dir, "README.md", "# README\n")
	writeFile(t, outside, "SPEC.md", "# Outside\n"+secret+"\n")
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "SPEC.md"), filepath.Join(dir, "docs", "SPEC.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs", "TASK.md"), 0o755); err != nil {
		t.Fatalf("mkdir TASK dir: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	projectDocs := htmlSection(body, "Project Docs", "Workspace Readiness")
	for _, want := range []string{
		`<strong>plan.md</strong> <span class="muted">missing</span>`,
		`<strong>docs/SPEC.md</strong> <span class="muted">denied</span>`,
		`<strong>docs/TASK.md</strong> <span class="muted">unavailable</span>`,
		`<strong>docs/EVAL.md</strong> <span class="muted">missing</span>`,
		`href="` + docURL("README.md") + `">README.md</a> <span class="muted">available</span>`,
	} {
		if !strings.Contains(projectDocs, want) {
			t.Fatalf("project docs missing safe state %q:\n%s", want, projectDocs)
		}
	}
	for _, leaked := range []string{secret, outside, filepath.ToSlash(outside), filepath.Join(outside, "SPEC.md"), filepath.ToSlash(filepath.Join(outside, "SPEC.md")), "Outside"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("project docs leaked denied payload %q:\n%s", leaked, body)
		}
	}
}

func TestDashboardProjectDocsShortcutsSanitizeHostileLabelsAndUnknownStates(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-projectdoclabel1234567890"
	rawPath := filepath.Join(dir, "outside", "secret.md")
	t.Setenv("JJ_PROJECT_DOC_LABEL_TOKEN", secret)
	writeFile(t, dir, "README.md", "# README\n")
	originalSpecs := projectDocShortcutSpecs
	projectDocShortcutSpecs = []projectDocShortcutSpec{
		{Label: "token=" + secret + " raw artifact body " + rawPath, Path: "docs/../" + secret + ".md"},
		{Label: "Unknown Doc", Path: ""},
		{Label: "README.md", Path: "README.md"},
	}
	t.Cleanup(func() { projectDocShortcutSpecs = originalSpecs })
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	projectDocs := htmlSection(body, "Project Docs", "Workspace Readiness")
	for _, want := range []string{
		`<strong>Project doc</strong> <span class="muted">denied</span>`,
		`<strong>Unknown Doc</strong> <span class="muted">unknown</span>`,
		`href="` + docURL("README.md") + `">README.md</a> <span class="muted">available</span>`,
	} {
		if !strings.Contains(projectDocs, want) {
			t.Fatalf("hostile project docs missing %q:\n%s", want, projectDocs)
		}
	}
	for _, leaked := range []string{secret, "token=", "raw artifact body", rawPath, filepath.ToSlash(rawPath), "../", security.RedactionMarker, "[omitted]"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("hostile project docs leaked %q:\n%s", leaked, body)
		}
	}
	if got := sanitizeProjectDocState("stale"); got != "unknown" {
		t.Fatalf("stale project doc state should render as unknown, got %q", got)
	}
}

func TestDashboardLatestRunSummaryIsCompactSanitizedAndTimestampSelected(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-latestrun1234567890"
	writeFile(t, dir, ".jj/runs/20260425-090000-clock-late/manifest.json", fmt.Sprintf(`{
		"run_id": "20260425-090000-clock-late",
		"status": "complete",
		"started_at": "2026-04-25T13:00:00Z",
		"planner_provider": "openai",
		"repository": {"enabled": true, "repo_url": "https://user:%s@example.invalid/repo.git"},
		"artifacts": {"manifest": "manifest.json", "validation_summary": "validation/summary.md"},
		"validation": {"status": "passed", "evidence_status": "recorded", "summary_path": "validation/summary.md"}
	}`, secret))
	writeFile(t, dir, ".jj/runs/20260425-235959-id-late/manifest.json", `{
		"run_id": "20260425-235959-id-late",
		"status": "failed",
		"started_at": "2026-04-25T10:00:00Z",
		"planner_provider": "codex",
		"artifacts": {"manifest": "manifest.json"},
		"validation": {"status": "failed"}
	}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	latest := htmlSection(body, "Latest Run", "Risks And Failures")
	for _, want := range []string{
		"20260425-090000-clock-late",
		"complete",
		"provider/result openai",
		"evaluation passed (recorded)",
		"2026-04-25T13:00:00Z",
		`href="/runs/20260425-090000-clock-late"`,
		`href="/runs"`,
		`href="/runs/audit?run=20260425-090000-clock-late"`,
	} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest-run summary missing %q:\n%s", want, latest)
		}
	}
	for _, leaked := range []string{
		"20260425-235959-id-late",
		secret,
		"repo_url",
		"Repository:",
		"Raw manifest",
		"Validation artifact",
		"Task Proposal Mode",
		security.RedactionMarker,
	} {
		if strings.Contains(latest, leaked) {
			t.Fatalf("latest-run summary leaked %q:\n%s", leaked, latest)
		}
	}
}

func TestDashboardLatestRunSummaryUnavailableAndNoneStatesAreSafe(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		dir := t.TempDir()
		server := newTestServer(t, dir, "")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, body)
		}
		latest := htmlSection(body, "Latest Run", "Risks And Failures")
		for _, want := range []string{"No jj runs found.", `href="/runs"`} {
			if !strings.Contains(latest, want) {
				t.Fatalf("latest none state missing %q:\n%s", want, latest)
			}
		}
	})

	t.Run("malformed", func(t *testing.T) {
		dir := t.TempDir()
		secret := "sk-proj-latestbad1234567890"
		writeFile(t, dir, ".jj/runs/20260429-120000-badjson/manifest.json", `{"run_id":"20260429-120000-badjson","status":"`+secret+`",`)
		server := newTestServer(t, dir, "")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, body)
		}
		latest := htmlSection(body, "Latest Run", "Risks And Failures")
		for _, want := range []string{"20260429-120000-badjson", "unavailable", "manifest is malformed", "Run history", "Run detail"} {
			if !strings.Contains(latest, want) {
				t.Fatalf("latest unavailable state missing %q:\n%s", want, latest)
			}
		}
		for _, leaked := range []string{secret, security.RedactionMarker, "Raw manifest", "Validation artifact"} {
			if strings.Contains(latest, leaked) {
				t.Fatalf("latest unavailable state leaked %q:\n%s", leaked, latest)
			}
		}
	})
}

func TestLatestRunSelectionIsDeterministicForTimestampFallbacksAndTies(t *testing.T) {
	summary := latestRunSummaryFromRuns([]runLink{
		{ID: "20260425-235959-id-late", Status: "complete", StartedAt: "2026-04-25T10:00:00Z"},
		{ID: "20260425-090000-clock-late", Status: "complete", StartedAt: "2026-04-25T13:00:00Z"},
	})
	if summary.RunID != "20260425-090000-clock-late" || summary.TimestampLabel != "2026-04-25T13:00:00Z" {
		t.Fatalf("expected manifest timestamp to select latest run, got %#v", summary)
	}

	summary = latestRunSummaryFromRuns([]runLink{
		{ID: "20260425-110000-fallback", Status: "complete", StartedAt: "not-a-time"},
		{ID: "20260425-120000-fallback", Status: "complete"},
	})
	if summary.RunID != "20260425-120000-fallback" || summary.TimestampLabel != "unknown" {
		t.Fatalf("expected run-id timestamp fallback with unknown display label, got %#v", summary)
	}

	summary = latestRunSummaryFromRuns([]runLink{
		{ID: "tie-a", Status: "complete", StartedAt: "2026-04-25T12:00:00Z"},
		{ID: "tie-b", Status: "complete", StartedAt: "2026-04-25T12:00:00Z"},
	})
	if summary.RunID != "tie-b" {
		t.Fatalf("expected deterministic ID tie-break, got %#v", summary)
	}
}

func TestDashboardRecentRunsSummaryListsLimitedGuardedRunsAndPreservesSections(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-recentruns1234567890"
	writeFile(t, dir, "plan.md", "# Plan\n")
	writeFile(t, dir, "README.md", "# README\n")
	writeFile(t, dir, "docs/SPEC.md", "# SPEC\n")
	writeFile(t, dir, "docs/EVAL.md", "# Eval\n")
	writeFile(t, dir, "docs/TASK.md", `# Tasks

- [~] TASK-0046 [feature] Show sanitized recent runs on the dashboard
`)

	writeRecentRun := func(id, status, startedAt, provider, validation, extra string) {
		fields := []string{
			fmt.Sprintf(`"run_id":%q`, id),
			fmt.Sprintf(`"status":%q`, status),
			fmt.Sprintf(`"planner_provider":%q`, provider),
			`"artifacts":{"manifest":"manifest.json"}`,
			fmt.Sprintf(`"validation":{"status":%q}`, validation),
		}
		if startedAt != "" {
			fields = append(fields, fmt.Sprintf(`"started_at":%q`, startedAt))
		}
		if extra != "" {
			fields = append(fields, extra)
		}
		writeFile(t, dir, ".jj/runs/"+id+"/manifest.json", "{"+strings.Join(fields, ",")+"}")
	}
	extra := fmt.Sprintf(`"repository":{"enabled":true,"repo_url":"https://user:%s@example.invalid/repo.git"},"errors":["raw artifact body token=%s"]`, secret, secret)
	writeRecentRun("20260429-120000-tie-b", "needs_work", "2026-04-29T15:00:00Z", "codex", "needs_work", "")
	writeRecentRun("20260429-110000-tie-a", "complete", "2026-04-29T15:00:00Z", "openai", "passed", "")
	writeRecentRun("20260429-140000-id-fallback", "failed", "not-a-time", "local", "failed", extra)
	writeRecentRun("20260429-130000-no-time", "complete", "", "codex", "passed", "")
	writeRecentRun("20260429-100000-fifth", "complete", "2026-04-29T10:00:00Z", "openai", "passed", "")
	writeRecentRun("20260429-090000-excluded", "complete", "2026-04-29T09:00:00Z", "openai", "passed", "")

	server := newTestServer(t, dir, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}

	recent := htmlSection(body, "Recent Runs", "Next Action")
	for _, want := range []string{
		"Recent Runs",
		"20260429-120000-tie-b",
		"needs_work",
		"provider/result codex",
		"evaluation needs_work",
		`href="/runs/20260429-120000-tie-b"`,
		`href="/runs/audit?run=20260429-120000-tie-b"`,
		`href="/runs">Run history</a>`,
		"20260429-100000-fifth",
	} {
		if !strings.Contains(recent, want) {
			t.Fatalf("recent runs missing %q:\n%s", want, recent)
		}
	}
	ordered := []string{
		"20260429-120000-tie-b",
		"20260429-110000-tie-a",
		"20260429-140000-id-fallback",
		"20260429-130000-no-time",
		"20260429-100000-fifth",
	}
	last := -1
	for _, id := range ordered {
		idx := strings.Index(recent, id)
		if idx < 0 || idx <= last {
			t.Fatalf("recent runs order is not deterministic around %q:\n%s", id, recent)
		}
		last = idx
	}
	for _, leaked := range []string{secret, "repo_url", "raw artifact body", "token=", security.RedactionMarker, "[omitted]"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("dashboard leaked recent-run payload %q:\n%s", leaked, body)
		}
	}
	for _, leaked := range []string{"20260429-090000-excluded", "Raw manifest", "Validation artifact"} {
		if strings.Contains(recent, leaked) {
			t.Fatalf("recent runs leaked %q:\n%s", leaked, recent)
		}
	}

	taskSection := htmlSection(body, "Current TASK", "Latest Run")
	for _, want := range []string{"TASK-0046", "feature", "in-progress", "Show sanitized recent runs on the dashboard"} {
		if !strings.Contains(taskSection, want) {
			t.Fatalf("TASK summary changed, missing %q:\n%s", want, taskSection)
		}
	}
	latest := htmlSection(body, "Latest Run", "Risks And Failures")
	for _, want := range []string{"20260429-120000-tie-b", "provider/result codex", "evaluation needs_work"} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest-run summary changed, missing %q:\n%s", want, latest)
		}
	}
	next := htmlSection(body, "Next Action", "Project Docs")
	for _, want := range []string{"Continue Task", "continue_task", "TASK-0046"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next-action summary changed, missing %q:\n%s", want, next)
		}
	}
	projectDocs := htmlSection(body, "Project Docs", "Workspace Readiness")
	for _, want := range []string{"plan.md", "docs/SPEC.md", "docs/TASK.md", "docs/EVAL.md", "README.md"} {
		if !strings.Contains(projectDocs, want) {
			t.Fatalf("project docs summary changed, missing %q:\n%s", want, projectDocs)
		}
	}
}

func TestDashboardRecentRunsSummaryNoRunsState(t *testing.T) {
	dir := t.TempDir()
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	recent := htmlSection(body, "Recent Runs", "Next Action")
	for _, want := range []string{"Recent Runs", "No jj runs found.", `href="/runs">Run history</a>`} {
		if !strings.Contains(recent, want) {
			t.Fatalf("recent no-run state missing %q:\n%s", want, recent)
		}
	}
}

func TestDashboardRecentRunsSummaryInvalidMetadataAndHostileIDsAreSafe(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := "sk-proj-recentrunsafe1234567890"
	writeFile(t, dir, ".jj/runs/20260429-120000-badjson/manifest.json", `{"run_id":"20260429-120000-badjson","status":"`+secret+`",`)
	writeFile(t, dir, ".jj/runs/20260429-121000-incomplete/manifest.json", `{"run_id":"20260429-121000-incomplete","status":"success"}`)
	writeFile(t, dir, ".jj/runs/20260429-122000-mismatch/manifest.json", `{"run_id":"20260429-000000-other","status":"success","artifacts":{"manifest":"manifest.json"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs/20260429-123000-missing"), 0o755); err != nil {
		t.Fatalf("mkdir missing run: %v", err)
	}
	writeFile(t, dir, ".jj/runs/20260429-124000-secretstatus/manifest.json", `{"run_id":"20260429-124000-secretstatus","status":"`+secret+`","artifacts":{"manifest":"manifest.json"},"errors":["Authorization: Bearer `+secret+`"]}`)
	writeFile(t, dir, ".jj/runs/sk-proj-recentrunid1234567890/manifest.json", `{"run_id":"sk-proj-recentrunid1234567890","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260429-126000-%2fescape/manifest.json", `{"run_id":"20260429-126000-%2fescape","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, outside, "target/manifest.json", `{"run_id":"20260429-125000-link","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs"), 0o755); err != nil {
		t.Fatalf("mkdir runs: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, ".jj/runs/20260429-125000-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	server := newTestServer(t, dir, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	recent := htmlSection(body, "Recent Runs", "Next Action")
	for _, want := range []string{
		"20260429-124000-secretstatus",
		"unknown",
		"20260429-123000-missing",
		"manifest unavailable",
		"20260429-122000-mismatch",
		"manifest is incomplete: run_id mismatch",
		"20260429-121000-incomplete",
		"manifest is incomplete: missing artifacts",
		"20260429-120000-badjson",
		"manifest is malformed",
	} {
		if !strings.Contains(recent, want) {
			t.Fatalf("recent invalid state missing %q:\n%s", want, recent)
		}
	}
	for _, leaked := range []string{
		secret,
		"Authorization: Bearer",
		"sk-proj-recentrunid",
		"20260429-126000-%2fescape",
		"20260429-125000-link",
		outside,
		filepath.ToSlash(outside),
		security.RedactionMarker,
		"[omitted]",
		"Raw manifest",
		"Validation artifact",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("recent invalid states leaked %q:\n%s", leaked, recent)
		}
	}

	summary := recentRunsSummaryFromRuns([]runLink{
		{ID: "sk-proj-recentrunid1234567890", Status: "failed"},
		{ID: "20260429-130000-denied", Invalid: true, ErrorSummary: "run id denied"},
	})
	if summary.State != "available" || len(summary.Items) != 1 || summary.Items[0].State != "denied" || summary.Items[0].RunID != "20260429-130000-denied" {
		t.Fatalf("recent runs helper should drop token-like IDs and render denied state safely: %#v", summary)
	}
}

func TestDashboardActiveRunShowsSanitizedNonTerminalRunsAndPreservesSections(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-activerun1234567890"
	t.Setenv("JJ_ACTIVE_RUN_SECRET", secret)
	writeFile(t, dir, "plan.md", "# Plan\n")
	writeFile(t, dir, "README.md", "# README\n")
	writeFile(t, dir, "docs/SPEC.md", "# SPEC\n")
	writeFile(t, dir, "docs/EVAL.md", "# Eval\n")
	writeFile(t, dir, "docs/TASK.md", `# Tasks

- [~] TASK-0048 [feature] Show sanitized active run progress on the dashboard
`)
	writeFile(t, dir, ".jj/runs/20260429-130000-complete/manifest.json", `{
		"run_id":"20260429-130000-complete",
		"status":"complete",
		"started_at":"2026-04-29T13:00:00Z",
		"planner_provider":"openai",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"status":"passed","evidence_status":"recorded"}
	}`)
	writeFile(t, dir, ".jj/runs/20260429-120000-active/manifest.json", `{
		"run_id":"20260429-120000-active",
		"status":"implementing",
		"started_at":"2026-04-29T12:00:00Z",
		"planner_provider":"codex",
		"repository":{"enabled":true,"repo_url":"https://user:`+secret+`@example.invalid/repo.git"},
		"artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md"},
		"validation":{"status":"passed","evidence_status":"recorded","summary_path":"validation/summary.md"}
	}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}

	active := dashboardActiveRunSection(t, body)
	for _, want := range []string{
		"Active Run",
		"20260429-120000-active",
		"implementing",
		"provider/result codex",
		"evaluation passed (recorded)",
		"2026-04-29T12:00:00Z",
		`href="/runs/20260429-120000-active"`,
		`href="/runs/audit?run=20260429-120000-active"`,
	} {
		if !strings.Contains(active, want) {
			t.Fatalf("active run summary missing %q:\n%s", want, active)
		}
	}
	for _, leaked := range []string{
		secret,
		"repo_url",
		"raw command text",
		"OPENAI_API_KEY",
		"raw artifact body",
		"token=",
		"validation/summary.md",
		"manifest.json",
		security.RedactionMarker,
		"[omitted]",
		`href="/run/progress`,
		"turn ",
	} {
		if strings.Contains(active, leaked) {
			t.Fatalf("active run summary leaked %q:\n%s", leaked, active)
		}
	}
	if strings.Contains(active, "20260429-130000-complete") {
		t.Fatalf("active run summary included terminal run:\n%s", active)
	}

	taskSection := htmlSection(body, "Current TASK", "Latest Run")
	for _, want := range []string{"TASK-0048", "feature", "in-progress", "Show sanitized active run progress on the dashboard"} {
		if !strings.Contains(taskSection, want) {
			t.Fatalf("TASK summary changed, missing %q:\n%s", want, taskSection)
		}
	}
	latest := htmlSection(body, "Latest Run", "Risks And Failures")
	for _, want := range []string{"20260429-130000-complete", "provider/result openai", "evaluation passed (recorded)"} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest-run summary changed, missing %q:\n%s", want, latest)
		}
	}
	recent := htmlSection(body, "Recent Runs", "Next Action")
	for _, want := range []string{"20260429-130000-complete", "20260429-120000-active"} {
		if !strings.Contains(recent, want) {
			t.Fatalf("recent-runs summary changed, missing %q:\n%s", want, recent)
		}
	}
	findings := htmlSection(body, "Evaluation Findings", "Recent Runs")
	if !strings.Contains(findings, "20260429-130000-complete") || !strings.Contains(findings, "all-clear") {
		t.Fatalf("evaluation findings summary changed:\n%s", findings)
	}
	next := htmlSection(body, "Next Action", "Project Docs")
	for _, want := range []string{"Continue Task", "continue_task", "TASK-0048"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next-action summary changed, missing %q:\n%s", want, next)
		}
	}
	projectDocs := htmlSection(body, "Project Docs", "Workspace Readiness")
	for _, want := range []string{"plan.md", "docs/SPEC.md", "docs/TASK.md", "docs/EVAL.md", "README.md"} {
		if !strings.Contains(projectDocs, want) {
			t.Fatalf("project docs summary changed, missing %q:\n%s", want, projectDocs)
		}
	}
}

func TestDashboardRootRunSummaryActionsUseGuardedLinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "plan.md", "# Plan\n")
	writeFile(t, dir, "README.md", "# README\n")
	writeFile(t, dir, "docs/SPEC.md", "# SPEC\n")
	writeFile(t, dir, "docs/EVAL.md", "# Eval\n")
	writeFile(t, dir, "docs/TASK.md", "# Tasks\n\n- [ ] TASK-0058 [quality] Simplify dashboard run summaries\n")
	writeFile(t, dir, ".jj/runs/20260429-130000-complete/manifest.json", `{
		"run_id":"20260429-130000-complete",
		"status":"complete",
		"started_at":"2026-04-29T13:00:00Z",
		"planner_provider":"openai",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"passed","evidence_status":"recorded","command_count":1,"passed_count":1}
	}`)
	writeFile(t, dir, ".jj/runs/20260429-120000-active/manifest.json", `{
		"run_id":"20260429-120000-active",
		"status":"planning",
		"started_at":"2026-04-29T12:00:00Z",
		"planner_provider":"codex",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"status":"passed","evidence_status":"recorded"}
	}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}

	validation := dashboardValidationStatusSection(t, body)
	assertDashboardRunActions(t, validation, "20260429-130000-complete")
	recent := htmlSection(body, "Recent Runs", "Next Action")
	assertDashboardRunActions(t, recent, "20260429-130000-complete")
	assertDashboardRunActions(t, recent, "20260429-120000-active")
	active := dashboardActiveRunSection(t, body)
	assertDashboardRunActions(t, active, "20260429-120000-active")
	for name, section := range map[string]string{
		"validation": validation,
		"recent":     recent,
		"active":     active,
	} {
		if strings.Contains(section, `href=""`) || strings.Contains(section, "manifest.json") {
			t.Fatalf("%s run-summary actions rendered unsafe data:\n%s", name, section)
		}
	}
}

func TestDashboardActiveRunNoActiveState(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, "<h2>Active Run</h2>") || strings.Contains(body, "Active Web Runs") {
		t.Fatalf("dashboard should not render active section without guarded non-terminal runs:\n%s", body)
	}
	summary := activeRunsSummaryFromRuns([]runLink{{ID: "20260429-120000-complete", Status: "complete"}})
	if summary.State != "none" || len(summary.Items) != 0 {
		t.Fatalf("no-active helper state should be deterministic none, got %#v", summary)
	}
}

func TestDashboardActiveRunUnsafeMetadataStatesAreSafe(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := "sk-proj-activeunsafe1234567890"
	t.Setenv("JJ_ACTIVE_RUN_UNSAFE_SECRET", secret)
	writeFile(t, dir, ".jj/runs/20260429-120000-badjson/manifest.json", `{"run_id":"20260429-120000-badjson","status":"`+secret+`",`)
	writeFile(t, dir, ".jj/runs/20260429-121000-partial/manifest.json", `{"run_id":"20260429-121000-partial","status":"planning"}`)
	writeFile(t, dir, ".jj/runs/20260429-122000-mismatch/manifest.json", `{"run_id":"20260429-000000-other","status":"planning","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260429-123000-stale/manifest.json", `{"run_id":"20260429-123000-stale","status":"stale","started_at":"2026-04-29T12:30:00Z","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260429-124000-finished-active/manifest.json", `{"run_id":"20260429-124000-finished-active","status":"planning","started_at":"2026-04-29T12:40:00Z","finished_at":"2026-04-29T12:41:00Z","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260429-125000-hostile-status/manifest.json", `{"run_id":"20260429-125000-hostile-status","status":"token=`+secret+`","started_at":"2026-04-29T12:50:00Z","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260429-126000-token-provider/manifest.json", `{
		"run_id":"20260429-126000-token-provider",
		"status":"planning",
		"started_at":"not-a-time",
		"planner_provider":"token=`+secret+`",
		"artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md"},
		"validation":{"status":"`+secret+`","summary_path":"validation/summary.md"},
		"errors":["Authorization: Bearer `+secret+`"],
		"risks":["raw diff body `+secret+`"]
	}`)
	writeFile(t, dir, ".jj/runs/sk-proj-activerunid1234567890/manifest.json", `{"run_id":"sk-proj-activerunid1234567890","status":"planning","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260429-127000-%2fescape/manifest.json", `{"run_id":"20260429-127000-%2fescape","status":"planning","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, outside, "target/manifest.json", `{"run_id":"20260429-128000-link","status":"planning","artifacts":{"manifest":"manifest.json"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs"), 0o755); err != nil {
		t.Fatalf("mkdir runs: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, ".jj/runs/20260429-128000-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	active := dashboardActiveRunSection(t, body)
	for _, want := range []string{
		"20260429-126000-token-provider",
		"planning",
		"provider/result result planning",
		"evaluation unknown",
		"unknown",
		`href="/runs/20260429-126000-token-provider"`,
		`href="/runs/audit?run=20260429-126000-token-provider"`,
	} {
		if !strings.Contains(active, want) {
			t.Fatalf("unsafe active state missing %q:\n%s", want, active)
		}
	}
	for _, leaked := range []string{
		secret,
		"token=",
		"Authorization: Bearer",
		"raw diff body",
		"validation/summary.md",
		"20260429-120000-badjson",
		"20260429-121000-partial",
		"20260429-122000-mismatch",
		"20260429-123000-stale",
		"20260429-124000-finished-active",
		"20260429-125000-hostile-status",
		"sk-proj-activerunid",
		"20260429-127000-%2fescape",
		"20260429-128000-link",
		outside,
		filepath.ToSlash(outside),
		security.RedactionMarker,
		"[omitted]",
		"Raw manifest",
	} {
		if strings.Contains(active, leaked) {
			t.Fatalf("unsafe active state leaked %q:\n%s", leaked, active)
		}
	}
	for _, leaked := range []string{secret, "token=", "Authorization: Bearer", "raw diff body", outside, filepath.ToSlash(outside), security.RedactionMarker, "[omitted]"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("dashboard leaked unsafe active payload %q:\n%s", leaked, body)
		}
	}
}

func TestActiveRunSelectionIsDeterministicForTimestampFallbacksAndTies(t *testing.T) {
	summary := activeRunsSummaryFromRuns([]runLink{
		{ID: "20260429-235959-id-late", Status: "planning", StartedAt: "2026-04-29T10:00:00Z"},
		{ID: "20260429-090000-clock-late", Status: "planning", StartedAt: "2026-04-29T13:00:00Z"},
		{ID: "20260429-140000-tie-b", Status: "implementing", StartedAt: "2026-04-29T12:00:00Z"},
		{ID: "20260429-130000-tie-a", Status: "validating", StartedAt: "2026-04-29T12:00:00Z"},
		{ID: "20260429-160000-id-fallback", Status: "running", StartedAt: "not-a-time"},
		{ID: "20260429-150000-no-time", Status: "queued"},
		{ID: "20260429-170000-complete", Status: "complete", StartedAt: "2026-04-29T17:00:00Z"},
	})
	got := make([]string, 0, len(summary.Items))
	for _, item := range summary.Items {
		got = append(got, item.RunID)
	}
	want := []string{
		"20260429-160000-id-fallback",
		"20260429-150000-no-time",
		"20260429-090000-clock-late",
		"20260429-140000-tie-b",
		"20260429-130000-tie-a",
		"20260429-235959-id-late",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("active run order = %v, want %v", got, want)
	}
	if summary.State != "available" {
		t.Fatalf("active summary state = %q, want available", summary.State)
	}

	unsafe := activeRunsSummaryFromRuns([]runLink{
		{ID: "sk-proj-activehelper1234567890", Status: "planning"},
		{ID: "20260429-180000-denied", Status: "denied", Invalid: true, ErrorSummary: "run metadata denied"},
		{ID: "20260429-181000-stale", Status: "stale"},
		{ID: "20260429-182000-inconsistent", Status: "planning", Validation: "passed", Failures: []string{"failure"}},
	})
	if unsafe.State != "none" || len(unsafe.Items) != 0 {
		t.Fatalf("unsafe active metadata should produce deterministic none state: %#v", unsafe)
	}

	staleEvaluation := activeRunsSummaryFromRuns([]runLink{
		{ID: "20260429-183000-stale-eval", Status: "planning", Validation: "stale (recorded)"},
	})
	if staleEvaluation.State != "available" || len(staleEvaluation.Items) != 1 || staleEvaluation.Items[0].EvaluationState != "unavailable" {
		t.Fatalf("stale active evaluation should render unavailable, got %#v", staleEvaluation)
	}
}

func TestDashboardValidationStatusShowsSanitizedLatestCompletedRunAndPreservesSections(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-validationstatus1234567890"
	t.Setenv("JJ_VALIDATION_STATUS_SECRET", secret)
	writeFile(t, dir, "plan.md", "# Plan\n")
	writeFile(t, dir, "README.md", "# README\n")
	writeFile(t, dir, "docs/SPEC.md", "# SPEC\n")
	writeFile(t, dir, "docs/EVAL.md", "# Eval\n")
	writeFile(t, dir, "docs/TASK.md", `# Tasks

- [~] TASK-0049 [feature] Show sanitized validation status on the dashboard
`)
	writeFile(t, dir, ".jj/runs/20260429-120000-passed/manifest.json", `{
		"run_id":"20260429-120000-passed",
		"status":"complete",
		"started_at":"2026-04-29T12:00:00Z",
		"planner_provider":"openai",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"ran":true,"status":"passed","evidence_status":"recorded","command_count":1,"passed_count":1}
	}`)
	writeFile(t, dir, ".jj/runs/20260429-123000-active/manifest.json", `{
		"run_id":"20260429-123000-active",
		"status":"implementing",
		"started_at":"2026-04-29T12:30:00Z",
		"planner_provider":"codex",
		"artifacts":{"manifest":"manifest.json"},
		"validation":{"status":"passed","evidence_status":"recorded"}
	}`)
	writeFile(t, dir, ".jj/runs/20260429-130000-failed/manifest.json", `{
		"run_id":"20260429-130000-failed",
		"status":"partial_failed",
		"started_at":"2026-04-29T13:00:00Z",
		"planner_provider":"codex",
		"artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md","validation_results":"validation/results.json"},
		"validation":{
			"ran":true,
			"status":"failed",
			"evidence_status":"recorded",
			"summary":"raw validation payload token=`+secret+` [omitted]",
			"reason":"raw command text OPENAI_API_KEY=`+secret+` ./scripts/validate.sh",
			"summary_path":"validation/summary.md",
			"results_path":"validation/results.json",
			"command_count":2,
			"passed_count":1,
			"failed_count":1,
			"commands":[{"label":"validate","command":"OPENAI_API_KEY=`+secret+` ./scripts/validate.sh","stdout_path":"validation/stdout.txt","stderr_path":"validation/stderr.txt","status":"failed"}]
		}
	}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}

	validation := dashboardValidationStatusSection(t, body)
	for _, want := range []string{
		"Validation Status",
		"20260429-130000-failed",
		"validation failed",
		"commands 2 · passed 1 · failed 1",
		"2026-04-29T13:00:00Z",
		`href="/runs/20260429-130000-failed"`,
		`href="/runs/audit?run=20260429-130000-failed"`,
	} {
		if !strings.Contains(validation, want) {
			t.Fatalf("validation status missing %q:\n%s", want, validation)
		}
	}
	for _, leaked := range []string{
		"20260429-120000-passed",
		"20260429-123000-active",
		secret,
		"raw validation payload",
		"raw command text",
		"OPENAI_API_KEY",
		"validation/summary.md",
		"validation/results.json",
		"stdout",
		"stderr",
		"manifest.json",
		"provider/result",
		security.RedactionMarker,
		"[omitted]",
	} {
		if strings.Contains(validation, leaked) {
			t.Fatalf("validation status leaked %q:\n%s", leaked, validation)
		}
	}

	taskSection := htmlSection(body, "Current TASK", "Latest Run")
	for _, want := range []string{"TASK-0049", "feature", "in-progress", "Show sanitized validation status on the dashboard"} {
		if !strings.Contains(taskSection, want) {
			t.Fatalf("TASK summary changed, missing %q:\n%s", want, taskSection)
		}
	}
	latest := htmlSection(body, "Latest Run", "Risks And Failures")
	for _, want := range []string{"20260429-130000-failed", "provider/result codex", "evaluation failed (recorded)"} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest-run summary changed, missing %q:\n%s", want, latest)
		}
	}
	findings := htmlSection(body, "Evaluation Findings", "Recent Runs")
	for _, want := range []string{"20260429-130000-failed", "findings", "evaluation failed (recorded)"} {
		if !strings.Contains(findings, want) {
			t.Fatalf("evaluation findings summary changed, missing %q:\n%s", want, findings)
		}
	}
	recent := htmlSection(body, "Recent Runs", "Next Action")
	for _, want := range []string{"20260429-130000-failed", "20260429-123000-active"} {
		if !strings.Contains(recent, want) {
			t.Fatalf("recent-runs summary changed, missing %q:\n%s", want, recent)
		}
	}
	active := dashboardActiveRunSection(t, body)
	if !strings.Contains(active, "20260429-123000-active") || strings.Contains(active, "20260429-130000-failed") {
		t.Fatalf("active-run summary changed:\n%s", active)
	}
	next := htmlSection(body, "Next Action", "Project Docs")
	for _, want := range []string{"Continue Task", "continue_task", "TASK-0049"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next-action summary changed, missing %q:\n%s", want, next)
		}
	}
	projectDocs := htmlSection(body, "Project Docs", "Workspace Readiness")
	for _, want := range []string{"plan.md", "docs/SPEC.md", "docs/TASK.md", "docs/EVAL.md", "README.md"} {
		if !strings.Contains(projectDocs, want) {
			t.Fatalf("project docs summary changed, missing %q:\n%s", want, projectDocs)
		}
	}
}

func TestDashboardValidationStatusUnavailableUnknownDeniedAndNoneStatesAreSafe(t *testing.T) {
	secret := "sk-proj-validationstate1234567890"
	cases := []struct {
		name        string
		runID       string
		setup       func(t *testing.T, dir, runID string)
		wantSection bool
		want        []string
		forbidden   []string
	}{
		{
			name:  "no validation",
			runID: "20260429-120000-no-validation",
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T12:00:00Z","artifacts":{"manifest":"manifest.json"}}`)
			},
		},
		{
			name:        "missing validation",
			runID:       "20260429-121000-missing",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T12:10:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"missing","evidence_status":"missing"}}`)
			},
			want: []string{"20260429-121000-missing", "validation unavailable", "2026-04-29T12:10:00Z"},
		},
		{
			name:        "partial validation",
			runID:       "20260429-122000-partial",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"partial_failed","started_at":"2026-04-29T12:20:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"partial","evidence_status":"recorded","command_count":1,"failed_count":1}}`)
			},
			want: []string{"20260429-122000-partial", "validation unavailable", "commands 1 · passed 0 · failed 1"},
		},
		{
			name:        "stale validation",
			runID:       "20260429-123000-stale",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T12:30:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"stale","evidence_status":"recorded"}}`)
			},
			want: []string{"20260429-123000-stale", "validation unavailable"},
		},
		{
			name:        "skipped validation",
			runID:       "20260429-124000-skipped",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"dry_run_complete","started_at":"2026-04-29T12:40:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"skipped":true,"status":"skipped","evidence_status":"skipped","command_count":1}}`)
			},
			want: []string{"20260429-124000-skipped", "validation skipped", "commands 1 · passed 0 · failed 0"},
		},
		{
			name:        "hostile token-like validation",
			runID:       "20260429-125000-hostile",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				t.Setenv("JJ_VALIDATION_STATUS_STATE_SECRET", secret)
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{
					"run_id":"`+runID+`",
					"status":"complete",
					"started_at":"2026-04-29T12:50:00Z",
					"artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md"},
					"validation":{"ran":true,"status":"`+secret+`","summary":"raw validation payload token=`+secret+`","summary_path":"validation/summary.md"},
					"errors":["Authorization: Bearer `+secret+`"]
				}`)
			},
			want:      []string{"20260429-125000-hostile", "validation unknown"},
			forbidden: []string{secret, "raw validation payload", "Authorization: Bearer", "token=", "validation/summary.md"},
		},
		{
			name:        "inconsistent validation",
			runID:       "20260429-126000-inconsistent",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T13:00:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed","evidence_status":"recorded","command_count":1,"passed_count":1,"failed_count":1}}`)
			},
			want: []string{"20260429-126000-inconsistent", "validation unknown", "commands 1 · passed 1 · failed 1"},
		},
		{
			name:        "malformed manifest",
			runID:       "20260429-127000-malformed",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"`+secret+`",`)
			},
			want:      []string{"20260429-127000-malformed", "validation unavailable"},
			forbidden: []string{secret},
		},
		{
			name:        "denied manifest",
			runID:       "20260429-128000-denied",
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				outside := t.TempDir()
				writeFile(t, outside, "manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed"}}`)
				if err := os.MkdirAll(filepath.Join(dir, ".jj/runs", runID), 0o755); err != nil {
					t.Fatalf("mkdir run: %v", err)
				}
				if err := os.Symlink(filepath.Join(outside, "manifest.json"), filepath.Join(dir, ".jj/runs", runID, "manifest.json")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			want: []string{"20260429-128000-denied", "validation denied"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir, tc.runID)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("dashboard status = %d body=%s", rec.Code, body)
			}
			if !tc.wantSection {
				if strings.Contains(body, "<h2>Validation Status</h2>") {
					t.Fatalf("%s should not render validation status section:\n%s", tc.name, body)
				}
				return
			}
			section := dashboardValidationStatusSection(t, body)
			for _, want := range tc.want {
				if !strings.Contains(section, want) {
					t.Fatalf("%s validation status missing %q:\n%s", tc.name, want, section)
				}
			}
			for _, leaked := range append(tc.forbidden, security.RedactionMarker, "[omitted]", "Raw manifest", "stdout", "stderr", "raw command text", "raw environment") {
				if strings.Contains(section, leaked) {
					t.Fatalf("%s validation status leaked %q:\n%s", tc.name, leaked, section)
				}
			}
		})
	}
}

func TestValidationStatusSelectionIsDeterministicForTimestampFallbacksAndTies(t *testing.T) {
	passed := runEvaluationMetadata{State: "all-clear", Status: "passed", EvidenceStatus: "recorded", SummaryLabel: "evaluation passed"}
	failed := runEvaluationMetadata{State: "findings", Status: "failed", EvidenceStatus: "recorded", SummaryLabel: "evaluation failed", CommandCount: 1, FailedCount: 1}
	skipped := runEvaluationMetadata{State: "none", Status: "skipped", EvidenceStatus: "skipped", SummaryLabel: "evaluation skipped", CommandCount: 1}
	none := runEvaluationMetadata{State: "none", Status: "none", EvidenceStatus: "none", SummaryLabel: "evaluation none"}

	summary := validationStatusSummaryFromRuns([]runLink{
		{ID: "20260429-235959-id-late", Status: "complete", StartedAt: "2026-04-29T10:00:00Z", Evaluation: passed},
		{ID: "20260429-090000-clock-late", Status: "complete", StartedAt: "2026-04-29T13:00:00Z", Evaluation: failed},
	})
	if len(summary.Items) != 1 || summary.Items[0].RunID != "20260429-090000-clock-late" || summary.Items[0].ValidationState != "failed" {
		t.Fatalf("expected manifest timestamp to select failed validation, got %#v", summary)
	}

	summary = validationStatusSummaryFromRuns([]runLink{
		{ID: "20260429-170000-active", Status: "planning", StartedAt: "2026-04-29T17:00:00Z", Evaluation: failed},
		{ID: "20260429-160000-id-fallback", Status: "dry_run_complete", StartedAt: "not-a-time", Evaluation: skipped},
		{ID: "20260429-150000-no-validation", Status: "complete", Evaluation: none},
	})
	if len(summary.Items) != 1 || summary.Items[0].RunID != "20260429-160000-id-fallback" || summary.Items[0].ValidationState != "skipped" {
		t.Fatalf("expected run-id timestamp fallback and skipped validation, got %#v", summary)
	}

	summary = validationStatusSummaryFromRuns([]runLink{
		{ID: "20260429-130000-tie-a", Status: "complete", StartedAt: "2026-04-29T12:00:00Z", Evaluation: passed},
		{ID: "20260429-140000-tie-b", Status: "complete", StartedAt: "2026-04-29T12:00:00Z", Evaluation: failed},
	})
	if len(summary.Items) != 1 || summary.Items[0].RunID != "20260429-140000-tie-b" {
		t.Fatalf("expected deterministic ID tie-break, got %#v", summary)
	}

	unsafe := validationStatusSummaryFromRuns([]runLink{
		{ID: "sk-proj-validationhelper1234567890", Status: "complete", Evaluation: passed},
		{ID: "20260429-180000-inconsistent", Status: "complete", Evaluation: runEvaluationMetadata{State: "all-clear", Status: "passed", EvidenceStatus: "recorded", CommandCount: 1, FailedCount: 1}},
	})
	if len(unsafe.Items) != 1 || unsafe.Items[0].RunID != "20260429-180000-inconsistent" || unsafe.Items[0].ValidationState != "unknown" {
		t.Fatalf("unsafe validation metadata should drop token-like IDs and render unknown state: %#v", unsafe)
	}
}

func TestDashboardEvaluationFindingsShowsLatestFindingsAndPreservesSections(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-evalfindings1234567890"
	writeFile(t, dir, "plan.md", "# Plan\n")
	writeFile(t, dir, "README.md", "# README\n")
	writeFile(t, dir, "docs/SPEC.md", "# SPEC\n")
	writeFile(t, dir, "docs/EVAL.md", "# Eval\n"+secret+"\n")
	writeFile(t, dir, "docs/TASK.md", `# Tasks

- [~] TASK-0047 [feature] Show sanitized evaluation findings on the dashboard
`)
	writeFile(t, dir, ".jj/runs/20260429-120000-findings/manifest.json", `{
		"run_id":"20260429-120000-findings",
		"status":"failed",
		"started_at":"2026-04-29T12:00:00Z",
		"planner_provider":"codex",
		"artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md"},
		"validation":{"status":"failed","evidence_status":"recorded","summary_path":"validation/summary.md","command_count":1,"failed_count":1},
		"errors":["validation failed in safe package"],
		"risks":["review required"],
		"git":{"warnings":["git metadata unavailable"]}
	}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	findings := htmlSection(body, "Evaluation Findings", "Recent Runs")
	for _, want := range []string{
		"Evaluation Findings",
		"20260429-120000-findings",
		"findings",
		"evaluation failed (recorded)",
		"issues 1 · risks 1 · warnings 1",
		"validation failed in safe package",
		"review required",
		"git metadata unavailable",
		`href="/runs/20260429-120000-findings"`,
		`href="/runs"`,
		`href="/runs/audit?run=20260429-120000-findings"`,
	} {
		if !strings.Contains(findings, want) {
			t.Fatalf("evaluation findings missing %q:\n%s", want, findings)
		}
	}
	for _, leaked := range []string{secret, "Raw manifest", "Validation summary", security.RedactionMarker, "[omitted]"} {
		if strings.Contains(findings, leaked) {
			t.Fatalf("evaluation findings leaked %q:\n%s", leaked, findings)
		}
	}

	taskSection := htmlSection(body, "Current TASK", "Latest Run")
	for _, want := range []string{"TASK-0047", "feature", "in-progress", "Show sanitized evaluation findings on the dashboard"} {
		if !strings.Contains(taskSection, want) {
			t.Fatalf("TASK summary changed, missing %q:\n%s", want, taskSection)
		}
	}
	latest := htmlSection(body, "Latest Run", "Risks And Failures")
	for _, want := range []string{"20260429-120000-findings", "provider/result codex", "evaluation failed (recorded)"} {
		if !strings.Contains(latest, want) {
			t.Fatalf("latest-run summary changed, missing %q:\n%s", want, latest)
		}
	}
	recent := htmlSection(body, "Recent Runs", "Next Action")
	if !strings.Contains(recent, "20260429-120000-findings") || !strings.Contains(recent, "evaluation failed (recorded)") {
		t.Fatalf("recent-runs summary changed:\n%s", recent)
	}
	next := htmlSection(body, "Next Action", "Project Docs")
	for _, want := range []string{"Continue Task", "continue_task", "TASK-0047"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next-action summary changed, missing %q:\n%s", want, next)
		}
	}
	projectDocs := htmlSection(body, "Project Docs", "Workspace Readiness")
	for _, want := range []string{"plan.md", "docs/SPEC.md", "docs/TASK.md", "docs/EVAL.md", "README.md"} {
		if !strings.Contains(projectDocs, want) {
			t.Fatalf("project docs summary changed, missing %q:\n%s", want, projectDocs)
		}
	}
}

func TestDashboardEvaluationFindingsAllClearNoRunAndNoEvaluationStates(t *testing.T) {
	t.Run("all clear", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, ".jj/runs/20260429-120000-clear/manifest.json", `{
			"run_id":"20260429-120000-clear",
			"status":"complete",
			"started_at":"2026-04-29T12:00:00Z",
			"artifacts":{"manifest":"manifest.json"},
			"validation":{"ran":true,"status":"passed","evidence_status":"recorded","command_count":1,"passed_count":1}
		}`)
		section := dashboardEvaluationFindingsSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"20260429-120000-clear", "all-clear", "evaluation passed (recorded)", "issues 0 · risks 0 · warnings 0", "All clear."} {
			if !strings.Contains(section, want) {
				t.Fatalf("all-clear findings missing %q:\n%s", want, section)
			}
		}
	})

	t.Run("no run", func(t *testing.T) {
		dir := t.TempDir()
		section := dashboardEvaluationFindingsSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"Evaluation Findings", "No jj runs found.", `href="/runs">Run history</a>`} {
			if !strings.Contains(section, want) {
				t.Fatalf("no-run findings missing %q:\n%s", want, section)
			}
		}
	})

	t.Run("no evaluation", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, ".jj/runs/20260429-120000-noeval/manifest.json", `{
			"run_id":"20260429-120000-noeval",
			"status":"complete",
			"started_at":"2026-04-29T12:00:00Z",
			"artifacts":{"manifest":"manifest.json"}
		}`)
		section := dashboardEvaluationFindingsSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"20260429-120000-noeval", "none", "evaluation none", "No evaluation metadata recorded for latest run.", "issues 0 · risks 0 · warnings 0"} {
			if !strings.Contains(section, want) {
				t.Fatalf("no-evaluation findings missing %q:\n%s", want, section)
			}
		}
	})
}

func TestDashboardEvaluationFindingsUnavailableDeniedUnknownAndNeedsWorkStates(t *testing.T) {
	secret := "sk-proj-evalstate1234567890"
	cases := []struct {
		name      string
		runID     string
		setup     func(t *testing.T, dir, runID string)
		want      []string
		forbidden []string
	}{
		{
			name:  "missing evaluation",
			runID: "20260429-120000-missing",
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T12:00:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"missing","evidence_status":"missing"}}`)
			},
			want: []string{"20260429-120000-missing", "unavailable", "evaluation missing", "Evaluation metadata unavailable."},
		},
		{
			name:  "malformed manifest",
			runID: "20260429-121000-malformed",
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"`+secret+`",`)
			},
			want:      []string{"20260429-121000-malformed", "unavailable", "evaluation unavailable", "Evaluation metadata unavailable."},
			forbidden: []string{secret},
		},
		{
			name:  "partial manifest",
			runID: "20260429-122000-partial",
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete"}`)
			},
			want: []string{"20260429-122000-partial", "unavailable", "evaluation unavailable"},
		},
		{
			name:  "stale evaluation",
			runID: "20260429-123000-stale",
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T12:30:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"stale","evidence_status":"recorded"}}`)
			},
			want: []string{"20260429-123000-stale", "unavailable", "evaluation stale (recorded)"},
		},
		{
			name:  "denied manifest",
			runID: "20260429-124000-denied",
			setup: func(t *testing.T, dir, runID string) {
				outside := t.TempDir()
				writeFile(t, outside, "manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
				if err := os.MkdirAll(filepath.Join(dir, ".jj/runs", runID), 0o755); err != nil {
					t.Fatalf("mkdir run: %v", err)
				}
				if err := os.Symlink(filepath.Join(outside, "manifest.json"), filepath.Join(dir, ".jj/runs", runID, "manifest.json")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			want: []string{"20260429-124000-denied", "denied", "evaluation denied", "Evaluation metadata denied."},
		},
		{
			name:  "hostile token-like metadata",
			runID: "20260429-125000-hostile",
			setup: func(t *testing.T, dir, runID string) {
				t.Setenv("JJ_EVAL_FINDINGS_SECRET", secret)
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{
					"run_id":"`+runID+`",
					"status":"complete",
					"started_at":"2026-04-29T12:50:00Z",
					"artifacts":{"manifest":"manifest.json"},
					"validation":{"ran":true,"status":"`+secret+`","summary":"raw artifact body token=`+secret+`"},
					"errors":["raw artifact body token=`+secret+`"],
					"risks":["Authorization: Bearer `+secret+`"],
					"git":{"warnings":["../outside/`+secret+`"]}
				}`)
			},
			want:      []string{"20260429-125000-hostile", "unknown", "evaluation unknown", "issues 1 · risks 1 · warnings 1"},
			forbidden: []string{secret, "raw artifact body", "Authorization: Bearer", "../outside"},
		},
		{
			name:  "needs work",
			runID: "20260429-126000-needs",
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"needs_work","started_at":"2026-04-29T13:00:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"needs_work","evidence_status":"recorded"}}`)
			},
			want: []string{"20260429-126000-needs", "findings", "evaluation needs_work (recorded)", "issues 1 · risks 0 · warnings 0", "needs_work"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir, tc.runID)
			section := dashboardEvaluationFindingsSection(t, newTestServer(t, dir, ""))
			for _, want := range tc.want {
				if !strings.Contains(section, want) {
					t.Fatalf("%s findings missing %q:\n%s", tc.name, want, section)
				}
			}
			for _, leaked := range append(tc.forbidden, security.RedactionMarker, "[omitted]", "Raw manifest", "Validation summary") {
				if strings.Contains(section, leaked) {
					t.Fatalf("%s findings leaked %q:\n%s", tc.name, leaked, section)
				}
			}
		})
	}
}

func TestDashboardShowsTaskMarkdownQueueSummary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/TASK.md", `# Work Queue

## Current Task

### TASK-0042: Show sanitized TASK.md work queue

- Mode: feature
- Status: in_progress

## Pending

- TASK-0043 [docs/pending] Refresh dashboard copy

## Blocked

- TASK-0044 [security/blocked] Recheck blocked release gate

## Done

- [x] TASK-0041: Keep completed guardrails closed
`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		"Current TASK",
		"TASK.md: 4 total, 1 done, 1 in progress, 1 pending, 1 blocked.",
		"Next:",
		"TASK-0042",
		"feature",
		"in-progress",
		"Show sanitized TASK.md work queue",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard task summary missing %q:\n%s", want, body)
		}
	}
}

func TestTaskQueueSummaryParsesCommonTaskMarkdownFormats(t *testing.T) {
	summary := parseTaskQueueSummary(`# Tasks

## Pending Tasks

1. TASK-0042 [feature/queued] Build the visible queue
2. [~] TASK-0043 [docs] Document the queue

## Done

- **TASK-0041**: Finish the release gate
- TASK-0040 (mode: security, status: blocked) Verify boundary state
`)
	if !summary.Available || summary.State != "available" {
		t.Fatalf("summary should be available: %#v", summary)
	}
	if summary.Counts.Total != 4 || summary.Counts.Pending != 1 || summary.Counts.InProgress != 1 || summary.Counts.Done != 1 || summary.Counts.Blocked != 1 {
		t.Fatalf("unexpected counts: %#v", summary.Counts)
	}
	if summary.Next == nil || summary.Next.ID != "TASK-0042" || summary.Next.Category != "feature" || summary.Next.Status != "pending" || summary.Next.Title != "Build the visible queue" {
		t.Fatalf("unexpected next task: %#v", summary.Next)
	}
}

func TestDashboardTaskMarkdownAllDoneShowsNoRunnableTasks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/TASK.md", `# Work Queue

## Done

- [x] TASK-0041: Release validation gate
- TASK-0040 [security/done] Harden run inspection
`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		"TASK.md: 2 total, 2 done, 0 in progress, 0 pending, 0 blocked. All TASK.md tasks are done. No runnable tasks.",
		"No runnable tasks.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("all-done task summary missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Next:") || strings.Contains(body, "TASK-0040 [security/done]") {
		t.Fatalf("all-done summary should not expose a runnable next task or raw body:\n%s", body)
	}
}

func TestDashboardTaskMarkdownMissingMalformedAndDeniedStatesAreSafe(t *testing.T) {
	secret := "sk-proj-tasksummary1234567890"
	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		server := newTestServer(t, dir, "")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, body)
		}
		if !strings.Contains(body, "TASK.md unavailable.") {
			t.Fatalf("missing TASK.md state not shown:\n%s", body)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "docs/TASK.md", "# Task Doc\n\nraw artifact body\nAuthorization: Bearer "+secret+"\n")
		server := newTestServer(t, dir, "")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, body)
		}
		if !strings.Contains(body, "TASK.md task summary unknown.") {
			t.Fatalf("malformed TASK.md state not shown:\n%s", body)
		}
		for _, leaked := range []string{secret, "raw artifact body", "Authorization: Bearer", security.RedactionMarker} {
			if strings.Contains(body, leaked) {
				t.Fatalf("malformed TASK.md leaked %q:\n%s", leaked, body)
			}
		}
	})

	t.Run("denied symlink", func(t *testing.T) {
		dir := t.TempDir()
		outside := t.TempDir()
		writeFile(t, outside, "TASK.md", "# Tasks\n\n- [ ] TASK-0042: "+secret+"\n")
		if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
			t.Fatalf("mkdir docs: %v", err)
		}
		if err := os.Symlink(filepath.Join(outside, "TASK.md"), filepath.Join(dir, "docs", "TASK.md")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		server := newTestServer(t, dir, "")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, body)
		}
		if !strings.Contains(body, "TASK.md unavailable.") {
			t.Fatalf("denied TASK.md state not shown:\n%s", body)
		}
		for _, leaked := range []string{secret, outside, filepath.ToSlash(outside), filepath.Join(outside, "TASK.md"), filepath.ToSlash(filepath.Join(outside, "TASK.md"))} {
			if strings.Contains(body, leaked) {
				t.Fatalf("denied TASK.md leaked %q:\n%s", leaked, body)
			}
		}
	})
}

func TestDashboardTaskMarkdownSanitizesRenderedFields(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-proj-taskfields1234567890"
	rawPath := filepath.Join(dir, "outside", "token.txt")
	writeFile(t, dir, "docs/TASK.md", fmt.Sprintf(`# Tasks

## Current Task

### TASK-0042: Fix token=%s raw artifact body %s

- Mode: token=%s
- Status: in_progress
- Title: OPENAI_API_KEY=%s private key leaked %s
`, secret, rawPath, secret, secret, rawPath))
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{"TASK-0042", "unknown", "in-progress", "unsafe value removed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sanitized task summary missing %q:\n%s", want, body)
		}
	}
	for _, leaked := range []string{
		secret,
		"token=",
		"OPENAI_API_KEY",
		"private key",
		"raw artifact body",
		rawPath,
		filepath.ToSlash(rawPath),
		security.RedactionMarker,
		"[omitted]",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("task summary leaked %q:\n%s", leaked, body)
		}
	}
}

func TestDashboardNextActionTaskDrivenStates(t *testing.T) {
	t.Run("in progress precedes pending", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "docs/TASK.md", `# Tasks

- [ ] TASK-0100 [feature] Start later
- [~] TASK-0101 [quality] Continue current
`)
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"Next Action", "Continue Task", "continue_task", "TASK-0101", "quality", "in-progress", "Continue current", `href="/doc?path=docs/TASK.md"`} {
			if !strings.Contains(section, want) {
				t.Fatalf("in-progress next action missing %q:\n%s", want, section)
			}
		}
		if strings.Contains(section, "TASK-0100") || strings.Contains(section, "Start Web Run") {
			t.Fatalf("in-progress next action should not start the pending task:\n%s", section)
		}
	})

	t.Run("pending task", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "docs/TASK.md", `# Tasks

- [ ] TASK-0102 [docs] Write dashboard docs
`)
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"Start Task", "start_task", "TASK-0102", "docs", "pending", "Write dashboard docs", `href="/run/new">Start Web Run</a>`} {
			if !strings.Contains(section, want) {
				t.Fatalf("pending next action missing %q:\n%s", want, section)
			}
		}
	})

	t.Run("all done", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "docs/TASK.md", `# Tasks

- [x] TASK-0103 [feature] Finished task
`)
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"All Done", "all_done", "All TASK.md tasks are done.", `href="/doc?path=docs/TASK.md"`} {
			if !strings.Contains(section, want) {
				t.Fatalf("all-done next action missing %q:\n%s", want, section)
			}
		}
	})

	t.Run("no run", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "docs/TASK.md", `# Tasks

- TASK-0104 [blocked] Waiting for external input
`)
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"No Runs", "no_run", "No runnable TASK.md tasks and no jj runs are available for review.", `href="/runs">Run History</a>`} {
			if !strings.Contains(section, want) {
				t.Fatalf("no-run next action missing %q:\n%s", want, section)
			}
		}
	})
}

func TestDashboardNextActionTaskUnavailableUnknownDeniedAndHostileStates(t *testing.T) {
	secret := "sk-proj-nextactiontask1234567890"

	t.Run("missing TASK", func(t *testing.T) {
		dir := t.TempDir()
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"TASK.md Missing", "task_missing", "docs/TASK.md is unavailable.", `href="/run/new">Start Web Run</a>`} {
			if !strings.Contains(section, want) {
				t.Fatalf("missing TASK next action missing %q:\n%s", want, section)
			}
		}
	})

	t.Run("unavailable TASK", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "docs", "TASK.md"), 0o755); err != nil {
			t.Fatalf("mkdir TASK dir: %v", err)
		}
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"TASK.md Unavailable", "task_unavailable", "TASK.md cannot be read through the workspace guard."} {
			if !strings.Contains(section, want) {
				t.Fatalf("unavailable TASK next action missing %q:\n%s", want, section)
			}
		}
	})

	t.Run("unknown TASK", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "docs/TASK.md", "# Tasks\n\nraw artifact body\nAuthorization: Bearer "+secret+"\n")
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"TASK.md Unknown", "task_unknown", "recognized runnable task summary", `href="/doc?path=docs/TASK.md"`} {
			if !strings.Contains(section, want) {
				t.Fatalf("unknown TASK next action missing %q:\n%s", want, section)
			}
		}
		for _, leaked := range []string{secret, "raw artifact body", "Authorization: Bearer", security.RedactionMarker} {
			if strings.Contains(section, leaked) {
				t.Fatalf("unknown TASK next action leaked %q:\n%s", leaked, section)
			}
		}
	})

	t.Run("denied TASK", func(t *testing.T) {
		dir := t.TempDir()
		outside := t.TempDir()
		writeFile(t, outside, "TASK.md", "# Tasks\n\n- [~] TASK-0105: "+secret+"\n")
		if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
			t.Fatalf("mkdir docs: %v", err)
		}
		if err := os.Symlink(filepath.Join(outside, "TASK.md"), filepath.Join(dir, "docs", "TASK.md")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		if !strings.Contains(section, "TASK.md Unavailable") || !strings.Contains(section, "task_unavailable") {
			t.Fatalf("denied TASK next action missing unavailable state:\n%s", section)
		}
		for _, leaked := range []string{secret, outside, filepath.ToSlash(outside)} {
			if strings.Contains(section, leaked) {
				t.Fatalf("denied TASK next action leaked %q:\n%s", leaked, section)
			}
		}
	})

	t.Run("hostile task label", func(t *testing.T) {
		dir := t.TempDir()
		rawPath := filepath.Join(dir, "outside", "secret.txt")
		t.Setenv("JJ_NEXT_ACTION_SECRET", secret)
		writeFile(t, dir, "docs/TASK.md", fmt.Sprintf(`# Tasks

- [~] TASK-0106 [token=%s] Fix token=%s raw artifact body %s
`, secret, secret, rawPath))
		section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
		for _, want := range []string{"Continue Task", "TASK-0106", "unknown", "in-progress", "unsafe value removed"} {
			if !strings.Contains(section, want) {
				t.Fatalf("hostile next action missing %q:\n%s", want, section)
			}
		}
		for _, leaked := range []string{secret, "token=", "raw artifact body", rawPath, filepath.ToSlash(rawPath), security.RedactionMarker, "[omitted]"} {
			if strings.Contains(section, leaked) {
				t.Fatalf("hostile next action leaked %q:\n%s", leaked, section)
			}
		}
	})
}

func TestDashboardNextActionRunDrivenStates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		runID     string
		manifest  string
		want      []string
		forbidden []string
	}{
		{
			name:  "failed latest run",
			runID: "20260429-120000-failed",
			manifest: `{
				"run_id":"20260429-120000-failed",
				"status":"failed",
				"started_at":"2026-04-29T12:00:00Z",
				"artifacts":{"manifest":"manifest.json"},
				"validation":{"status":"failed"}
			}`,
			want: []string{"Review Latest Run", "review_latest_run", "20260429-120000-failed", "status failed; evaluation failed", `href="/runs/20260429-120000-failed">Run Detail</a>`, `href="/runs/audit?run=20260429-120000-failed">Audit Export</a>`},
		},
		{
			name:  "needs work latest run",
			runID: "20260429-121000-needs",
			manifest: `{
				"run_id":"20260429-121000-needs",
				"status":"needs_work",
				"started_at":"2026-04-29T12:10:00Z",
				"artifacts":{"manifest":"manifest.json"},
				"validation":{"status":"needs_work"}
			}`,
			want: []string{"Review Latest Run", "review_latest_run", "20260429-121000-needs", "status needs_work; evaluation needs_work"},
		},
		{
			name:  "unknown latest run",
			runID: "20260429-122000-unknown",
			manifest: `{
				"run_id":"20260429-122000-unknown",
				"status":"complete",
				"started_at":"2026-04-29T12:20:00Z",
				"artifacts":{"manifest":"manifest.json"}
			}`,
			want: []string{"Review Latest Run", "review_latest_run", "20260429-122000-unknown", "status complete; evaluation unknown"},
		},
		{
			name:      "malformed latest run",
			runID:     "20260429-123000-malformed",
			manifest:  `{"run_id":"20260429-123000-malformed","status":"sk-proj-nextactionrun1234567890",`,
			want:      []string{"Review Latest Run", "review_latest_run", "20260429-123000-malformed", "status unavailable", "manifest is malformed"},
			forbidden: []string{"sk-proj-nextactionrun1234567890", security.RedactionMarker, "Raw manifest"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "docs/TASK.md", `# Tasks

- [x] TASK-0107 [feature] Completed task
`)
			writeFile(t, dir, ".jj/runs/"+tc.runID+"/manifest.json", tc.manifest)
			section := dashboardNextActionSection(t, newTestServer(t, dir, ""))
			for _, want := range tc.want {
				if !strings.Contains(section, want) {
					t.Fatalf("run-driven next action missing %q:\n%s", want, section)
				}
			}
			for _, leaked := range tc.forbidden {
				if strings.Contains(section, leaked) {
					t.Fatalf("run-driven next action leaked %q:\n%s", leaked, section)
				}
			}
		})
	}
}

func TestDashboardUsesSafeWorkspaceDisplayPath(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	for _, target := range []string{"/", "/run/new"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rec.Code, body)
			}
			if strings.Contains(body, dir) || strings.Contains(body, filepath.ToSlash(dir)) {
				t.Fatalf("dashboard leaked workspace absolute path:\n%s", body)
			}
			if !strings.Contains(body, "[workspace]") {
				t.Fatalf("dashboard should show safe workspace label:\n%s", body)
			}
		})
	}
}

func TestDashboardResponsesUseNoStoreCacheControl(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	for _, target := range []string{
		"/",
		"/doc?path=docs/SPEC.md",
		"/artifact?run=20260425-120000-bbbbbb&path=snapshots/tasks.after.json",
		"/runs/20260425-120000-bbbbbb/manifest",
	} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Fatalf("X-Frame-Options = %q, want DENY", got)
			}
			if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") || !strings.Contains(got, "object-src 'none'") {
				t.Fatalf("Content-Security-Policy missing safe defaults: %q", got)
			}
		})
	}
}

func TestTruncateDisplayPreservesUTF8(t *testing.T) {
	text := strings.Repeat("a", 10) + "한글 continuation context"
	got := truncateDisplay(text, 11)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated text is not valid UTF-8: %q", got)
	}
	if strings.Contains(got, "한") || !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation before partial multibyte rune, got %q", got)
	}

	got = truncateDisplay("ok\x80한글", 100)
	if !utf8.ValidString(got) || !strings.Contains(got, "\uFFFD") {
		t.Fatalf("invalid input bytes should be replaced with valid UTF-8, got %q", got)
	}
}

func TestIndexShowsMalformedIncompleteAndLegacyRuns(t *testing.T) {
	dir := newTestWorkspace(t)
	writeFile(t, dir, ".jj/runs/20260425-130000-badjson/manifest.json", `{"run_id":"20260425-130000-badjson","status":"sk-proj-abcdef1234567890",`)
	writeFile(t, dir, ".jj/runs/20260425-140000-incomplete/manifest.json", `{"run_id":"20260425-140000-incomplete","status":"success"}`)
	writeFile(t, dir, ".jj/runs/20260425-150000-legacy/manifest.json", `{"run_id":"20260425-150000-legacy","status":"success","started_at":"2026-04-25T15:00:00Z","artifacts":{"manifest":"manifest.json"},"commit":{"ran":true,"status":"success","sha":"abc123"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs/20260425-160000-missing"), 0o755); err != nil {
		t.Fatalf("mkdir missing manifest run: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		"20260425-130000-badjson",
		"manifest is malformed",
		"20260425-140000-incomplete",
		"manifest is incomplete: missing artifacts",
		"20260425-120000-bbbbbb",
		"20260425-150000-legacy",
		"Legacy commit-success metadata is historical",
		"20260425-160000-missing",
		"manifest unavailable",
		"artifact links unavailable because this run lacks a trusted top-level artifacts map or trusted manifest",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, body)
		}
	}
	for _, leaked := range []string{
		"sk-proj-abcdef1234567890",
		`href="/run?id=20260425-130000-badjson"`,
		`href="/runs/20260425-130000-badjson/manifest"`,
		`artifact?run=20260425-130000-badjson`,
		`href="/run?id=20260425-160000-missing"`,
		`artifact?run=20260425-160000-missing`,
		"commit_failed",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("dashboard leaked or linked invalid data %q:\n%s", leaked, body)
		}
	}
}

func TestRunHistoryListsNewestFirstAndLinksGuardedDetails(t *testing.T) {
	dir := newTestWorkspace(t)
	writeFile(t, dir, ".jj/runs/20260425-125000-codex/manifest.json", `{"run_id":"20260425-125000-codex","status":"complete","started_at":"2026-04-25T12:50:00Z","dry_run":false,"planner_provider":"codex","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed"}}`)
	writeFile(t, dir, ".jj/runs/20260425-130000-openai/manifest.json", `{"run_id":"20260425-130000-openai","status":"dry_run_complete","started_at":"2026-04-25T13:00:00Z","dry_run":true,"planner_provider":"openai","artifacts":{"manifest":"manifest.json"},"validation":{"status":"missing","evidence_status":"missing"}}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		"Filters",
		`name="status"`,
		`name="dry_run"`,
		`name="planner_provider"`,
		`name="evaluation"`,
		`href="/runs/20260425-130000-openai"`,
		`href="/runs/20260425-125000-codex"`,
		"dry-run true",
		"dry-run false",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("history missing %q:\n%s", want, body)
		}
	}
	first := strings.Index(body, "20260425-130000-openai")
	second := strings.Index(body, "20260425-125000-codex")
	third := strings.Index(body, "20260425-120000-bbbbbb")
	if first < 0 || second < 0 || third < 0 || !(first < second && second < third) {
		t.Fatalf("history is not newest-first:\n%s", body)
	}
	if strings.Contains(body, `href="/run?id=`) {
		t.Fatalf("history used unguarded legacy run links:\n%s", body)
	}
}

func TestRunHistoryFiltersAndInvalidQueries(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-historyfilter1234567890"
	writeFile(t, dir, ".jj/runs/20260425-125000-openai-pass/manifest.json", `{"run_id":"20260425-125000-openai-pass","status":"complete","started_at":"2026-04-25T12:50:00Z","dry_run":true,"planner_provider":"openai","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed","evidence_status":"recorded"}}`)
	writeFile(t, dir, ".jj/runs/20260425-130000-openai-fail/manifest.json", `{"run_id":"20260425-130000-openai-fail","status":"failed","started_at":"2026-04-25T13:00:00Z","dry_run":true,"planner_provider":"openai","artifacts":{"manifest":"manifest.json"},"validation":{"status":"failed","evidence_status":"recorded"}}`)
	writeFile(t, dir, ".jj/runs/20260425-131000-codex-fail/manifest.json", `{"run_id":"20260425-131000-codex-fail","status":"failed","started_at":"2026-04-25T13:10:00Z","dry_run":true,"planner_provider":"codex","artifacts":{"manifest":"manifest.json"},"validation":{"status":"failed","evidence_status":"recorded"}}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs?status=failed&dry_run=true&planner_provider=openai&evaluation=failed&q=openai-fail", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "20260425-130000-openai-fail") {
		t.Fatalf("filtered history missing matching run:\n%s", body)
	}
	for _, blocked := range []string{"20260425-125000-openai-pass", "20260425-131000-codex-fail", "20260425-120000-bbbbbb"} {
		if strings.Contains(body, blocked) {
			t.Fatalf("filtered history included %q:\n%s", blocked, body)
		}
	}
	for _, selected := range []string{`value="failed" selected`, `value="true" selected`, `value="openai" selected`} {
		if !strings.Contains(body, selected) {
			t.Fatalf("filter control did not preserve safe selected value %q:\n%s", selected, body)
		}
	}

	rec = httptest.NewRecorder()
	target := "/runs?status=..%2f" + url.QueryEscape(secret) +
		"&dry_run=maybe&planner_provider=" + url.QueryEscape(secret) +
		"&evaluation=%3Cscript%3E&q=..%2f" + url.QueryEscape(secret)
	req = httptest.NewRequest(http.MethodGet, target, nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("invalid filter status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "Some unsupported filters were ignored.") {
		t.Fatalf("invalid filters should produce a generic notice:\n%s", body)
	}
	for _, leaked := range []string{secret, "../", "<script", "maybe", security.RedactionMarker} {
		if strings.Contains(body, leaked) {
			t.Fatalf("invalid filter response leaked %q:\n%s", leaked, body)
		}
	}
}

func TestRunHistoryRendersMalformedMissingPartialAndSecretManifestsSafely(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-historysecret1234567890"
	writeFile(t, dir, ".jj/runs/20260425-140000-badjson/manifest.json", `{"run_id":"20260425-140000-badjson","status":"`+secret+`",`)
	writeFile(t, dir, ".jj/runs/20260425-141000-incomplete/manifest.json", `{"run_id":"20260425-141000-incomplete","status":"success"}`)
	writeFile(t, dir, ".jj/runs/20260425-142000-secretstatus/manifest.json", `{"run_id":"20260425-142000-secretstatus","status":"`+secret+`","started_at":"2026-04-25T14:20:00Z","planner_provider":"openai","artifacts":{"manifest":"manifest.json"},"errors":["token=`+secret+`"]}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs/20260425-143000-missing"), 0o755); err != nil {
		t.Fatalf("mkdir missing manifest run: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		`href="/runs/20260425-140000-badjson"`,
		"manifest is malformed",
		`href="/runs/20260425-141000-incomplete"`,
		"manifest is incomplete: missing artifacts",
		`href="/runs/20260425-142000-secretstatus"`,
		`href="/runs/20260425-143000-missing"`,
		"manifest unavailable",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("history missing safe state %q:\n%s", want, body)
		}
	}
	for _, leaked := range []string{secret, security.RedactionMarker, dir, filepath.ToSlash(dir), `href="/runs/` + secret} {
		if strings.Contains(body, leaked) {
			t.Fatalf("history leaked %q:\n%s", leaked, body)
		}
	}
}

func TestRunHistoryIgnoresSymlinkAndUnsafeRunDirectories(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	secret := "run-history-outside-secret"
	target := filepath.Join(outside, "target")
	writeFile(t, target, "manifest.json", `{"run_id":"20260425-150000-link","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, target, "secret.txt", secret)
	if err := os.Symlink(target, filepath.Join(dir, ".jj/runs/20260425-150000-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	writeFile(t, dir, ".jj/runs/20260425-151000-%2fescape/manifest.json", `{"run_id":"20260425-151000-%2fescape","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/sk-proj-historyrunid1234567890/manifest.json", `{"run_id":"sk-proj-historyrunid1234567890","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs?status=%2e%2e%2f&q=..%2foutside", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, leaked := range []string{
		"20260425-150000-link",
		"20260425-151000-%2fescape",
		"sk-proj-historyrunid1234567890",
		secret,
		outside,
		filepath.ToSlash(outside),
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("history exposed unsafe run directory data %q:\n%s", leaked, body)
		}
	}
}

func TestIndexShowsPlanningValidationFailureRun(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260425-170000-emptytask"
	secret := "sk-proj-emptytask1234567890"
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id":%q,
		"status":"failed",
		"started_at":"2026-04-25T17:00:00Z",
		"failed_stage":"planning",
		"failure_phase":"planning",
		"error_summary":"merge planning outputs: merged TASK content is empty %s",
		"errors":["merge planning outputs: merged TASK content is empty %s"],
		"artifacts":{
			"manifest":"manifest.json",
			"planning_merge":"planning/merge.json",
			"planning_merged":"planning/merged.json",
			"planning_merge_raw_response":"planning/raw_response_merge.txt"
		},
			"codex":{"skipped":true,"status":"skipped"}
	}`, runID, secret, secret))
	writeFile(t, dir, ".jj/runs/"+runID+"/planning/merge.json", `{"spec":"# SPEC\n\nValid","task":"","notes":[]}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/planning/merged.json", `{"spec":"# SPEC\n\nValid","task":"","notes":[]}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/planning/raw_response_merge.txt", "token="+secret+"\n")
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{runID, "failed", "merged TASK content is empty"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing planning failure %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, secret) || strings.Contains(body, security.RedactionMarker) || !strings.Contains(body, "sensitive value removed") {
		t.Fatalf("dashboard did not sanitize planning failure secret:\n%s", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/run?id="+runID, nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", rec.Code, body)
	}
	section := runDetailRunArtifactsSection(t, body)
	for _, want := range []string{"Run Artifacts", "Manifest summary", "manifest.json", "available"} {
		if !strings.Contains(section, want) {
			t.Fatalf("run artifact inventory missing %q:\n%s", want, section)
		}
	}
	for _, hidden := range []string{"planning/merge.json", "planning/merged.json", "planning/raw_response_merge.txt"} {
		if strings.Contains(section, hidden) {
			t.Fatalf("run artifact inventory exposed non-allowlisted artifact %q:\n%s", hidden, section)
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/artifact?run="+runID+"&path=planning/raw_response_merge.txt", nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("artifact status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, secret) || !strings.Contains(body, "[jj-omitted]") {
		t.Fatalf("planning artifact did not redact secret:\n%s", body)
	}
}

func TestRunArtifactsExposeUntrackedEvidence(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260425-180000-untracked"
	secret := "sk-proj-serveuntracked1234567890"
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id":%q,
		"status":"complete",
		"started_at":"2026-04-25T18:00:00Z",
		"artifacts":{
			"manifest":"manifest.json",
			"git_untracked_files":"git/untracked-files.txt",
			"git_untracked_patch":"git/untracked.patch",
			"git_untracked_summary":"git/untracked-summary.txt"
		}
	}`, runID))
	writeFile(t, dir, ".jj/runs/"+runID+"/git/untracked-files.txt", "new-script.sh\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/git/untracked.patch", "diff --git a/new-script.sh b/new-script.sh\n+api_key="+secret+"\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/git/untracked-summary.txt", "Captured text files: 1\n")
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/run?id="+runID, nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", rec.Code, body)
	}
	section := runDetailRunArtifactsSection(t, body)
	for _, want := range []string{"Run Artifacts", "Manifest summary", "manifest.json"} {
		if !strings.Contains(section, want) {
			t.Fatalf("run artifact inventory missing %q:\n%s", want, section)
		}
	}
	for _, hidden := range []string{"git/untracked-files.txt", "git/untracked.patch", "git/untracked-summary.txt"} {
		if strings.Contains(section, hidden) {
			t.Fatalf("run artifact inventory exposed non-allowlisted artifact %q:\n%s", hidden, section)
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/artifact?run="+runID+"&path=git/untracked.patch", nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("artifact status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "new-script.sh") || strings.Contains(body, secret) {
		t.Fatalf("untracked artifact was not served with redaction:\n%s", body)
	}
	if !strings.Contains(body, "[jj-omitted]") {
		t.Fatalf("untracked artifact missing redaction marker:\n%s", body)
	}
}

func TestMalformedManifestArtifactFailsClosedWithoutPathLeak(t *testing.T) {
	dir := newTestWorkspace(t)
	writeFile(t, dir, ".jj/runs/20260425-130000-badjson/manifest.json", `{"run_id":"20260425-130000-badjson","status":"sk-proj-abcdef1234567890",`)
	writeFile(t, dir, ".jj/runs/20260425-130000-badjson/docs/TASK.md", "# Secret task\n")
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifact?run=20260425-130000-badjson&path=docs/TASK.md", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code < 400 {
		t.Fatalf("expected malformed-manifest artifact rejection, got %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "manifest is malformed") {
		t.Fatalf("expected sanitized manifest error, got:\n%s", body)
	}
	for _, leaked := range []string{dir, "sk-proj-abcdef1234567890", "Secret task"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("artifact error leaked %q:\n%s", leaked, body)
		}
	}
}

func TestRunManifestMalformedResponseIsSanitized(t *testing.T) {
	dir := newTestWorkspace(t)
	writeFile(t, dir, ".jj/runs/20260425-130000-badjson/manifest.json", `{"run_id":"20260425-130000-badjson","status":"sk-proj-abcdef1234567890",`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/20260425-130000-badjson/manifest", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "manifest is malformed") || strings.Contains(body, "sk-proj-abcdef1234567890") {
		t.Fatalf("malformed manifest response was not sanitized:\n%s", body)
	}
}

func TestRunManifestResponseRedactsSecretsAndHostPaths(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/20260425-120000-bbbbbb/manifest", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	for _, leaked := range []string{dir, filepath.ToSlash(dir), "/tmp/acme-app", "ghp_dashboardsecret1234567890"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("manifest response leaked %q:\n%s", leaked, body)
		}
	}
	if !strings.Contains(body, `"repo_dir": "[path]"`) || !strings.Contains(body, "https://github.com/acme/app.git") {
		t.Fatalf("manifest response missing sanitized path or repository URL:\n%s", body)
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

func TestNewWithConfigRequiresExplicitExternalBind(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewWithConfig(Config{CWD: dir, Addr: "0.0.0.0:0"}); err == nil || !strings.Contains(err.Error(), "external dashboard binding requires explicit") {
		t.Fatalf("expected implicit external bind rejection, got %v", err)
	}
	if _, err := NewWithConfig(Config{CWD: dir, Addr: "0.0.0.0:0", AddrExplicit: true}); err != nil {
		t.Fatalf("explicit external addr should be allowed: %v", err)
	}
	if _, err := NewWithConfig(Config{CWD: dir, Host: "0.0.0.0", Port: 0, HostExplicit: true, PortExplicit: true}); err != nil {
		t.Fatalf("explicit external host should be allowed: %v", err)
	}
}

func TestExecuteWarnsOnExplicitExternalBind(t *testing.T) {
	dir := newTestWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out strings.Builder
	errCh := make(chan error, 1)
	go func() {
		errCh <- Execute(ctx, Config{CWD: dir, Addr: "0.0.0.0:0", AddrExplicit: true, Stdout: &out})
	}()

	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), "jj: serving dashboard at http://") {
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
	if !strings.Contains(out.String(), "warning: serving on non-local address") {
		t.Fatalf("expected external bind warning, got:\n%s", out.String())
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
	for _, want := range []string{`action="/run/start"`, `name="plan_prompt"`, `name="task_proposal_mode"`, `name="repo"`, `name="base_branch"`, `name="work_branch"`, `name="push"`, "auto continue turns", "max turns"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/run/new missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `name="plan_path" value="plan.md" required`) {
		t.Fatalf("/run/new should not require plan_path when prompt input is available:\n%s", body)
	}
}

func TestIndexShowsWebRunWhenPlanMissing(t *testing.T) {
	dir := t.TempDir()
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, `href="/run/new">Start Web Run</a>`) {
		t.Fatalf("dashboard should expose Web Run without plan.md:\n%s", body)
	}
	if strings.Contains(body, "Open Plan") {
		t.Fatalf("dashboard should not show Open Plan when plan.md is missing:\n%s", body)
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
	if strings.Contains(body, "sk-proj") || !strings.Contains(body, "[jj-omitted]") {
		t.Fatalf("doc was not redacted:\n%s", body)
	}
}

func TestProjectDocAllowlistServesOnlyDocumentedDocs(t *testing.T) {
	dir := newTestWorkspace(t)
	writeFile(t, dir, "docs/EVAL.md", "# Eval Doc\n")
	server := newTestServer(t, dir, "")

	allowed := []struct {
		target string
		want   string
	}{
		{"/doc?path=README.md", "<h1>Root</h1>"},
		{"/doc?path=plan.md", "<h1>Product Plan</h1>"},
		{"/doc?path=docs/SPEC.md", "<h1>Spec Doc</h1>"},
		{"/doc?path=docs/TASK.md", "<h1>Task Doc</h1>"},
		{"/doc?path=docs/EVAL.md", "<h1>Eval Doc</h1>"},
		{"/doc?path=.jj/spec.json", "SPEC"},
		{"/doc?path=.jj/tasks.json", "TASK-0001"},
	}
	for _, tc := range allowed {
		t.Run(tc.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK || !strings.Contains(body, tc.want) {
				t.Fatalf("expected allowed doc, got status=%d body=%s", rec.Code, body)
			}
		})
	}
}

func TestProjectDocRejectsUnlistedAndUnsafePathsWithoutLeaks(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "serve-doc-secret-1234567890"
	writeFile(t, dir, "docs/PRIVATE.md", "# Private\n"+secret+"\n")
	writeFile(t, dir, "playground/secret.md", "# Playground\n"+secret+"\n")
	writeFile(t, dir, "cmd/app/main.go", "package main\n// "+secret+"\n")
	writeFile(t, dir, ".hidden.md", "# Hidden\n"+secret+"\n")
	writeFile(t, dir, ".env", "API_KEY="+secret+"\n")
	writeFile(t, dir, "SPEC.md", "# Root SPEC\n"+secret+"\n")
	outside := t.TempDir()
	outsideDoc := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsideDoc, []byte("# Outside\n"+secret+"\n"), 0o644); err != nil {
		t.Fatalf("write outside doc: %v", err)
	}
	server := newTestServer(t, dir, "")

	probes := []struct {
		name   string
		target string
	}{
		{name: "private doc", target: "/doc?path=docs/PRIVATE.md"},
		{name: "arbitrary docs markdown", target: "/doc?path=docs/guide.md"},
		{name: "playground plan", target: "/doc?path=playground/plan.md"},
		{name: "playground markdown", target: "/doc?path=playground/secret.md"},
		{name: "source file", target: "/doc?path=cmd/app/main.go"},
		{name: "hidden markdown", target: "/doc?path=.hidden.md"},
		{name: "env file", target: "/doc?path=.env"},
		{name: "git internals", target: "/doc?path=.git/ignored.md"},
		{name: "root spec", target: "/doc?path=SPEC.md"},
		{name: "traversal", target: "/doc?path=../README.md"},
		{name: "nested traversal", target: "/doc?path=docs/../README.md"},
		{name: "encoded traversal", target: "/doc?path=docs%2f..%2fREADME.md"},
		{name: "absolute escape", target: "/doc?path=" + url.QueryEscape(outsideDoc)},
		{name: "docs route private", target: "/docs/PRIVATE.md"},
		{name: "docs route traversal", target: "/docs/../README.md"},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, probe.target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code < 400 {
				t.Fatalf("expected rejection, got %d body=%s", rec.Code, body)
			}
			for _, leaked := range []string{secret, dir, filepath.ToSlash(dir), outsideDoc, filepath.ToSlash(outsideDoc)} {
				if strings.Contains(body, leaked) {
					t.Fatalf("rejection leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func TestRunDashboardAndArtifactRedactSecrets(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "serve-secret-token-1234567890"
	t.Setenv("JJ_SERVE_TEST_TOKEN", secret)
	writeFile(t, dir, ".jj/runs/20260425-160000-redacted/manifest.json", fmt.Sprintf(`{"run_id":"20260425-160000-redacted","status":"failed","started_at":"2026-04-25T16:00:00Z","artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md"},"errors":["token=%s"],"risks":["Bearer %s"],"validation":{"status":"failed","summary_path":"validation/summary.md"}}`, secret, secret))
	writeFile(t, dir, ".jj/runs/20260425-160000-redacted/validation/summary.md", "Authorization: Bearer "+secret+"\n")
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, secret) || strings.Contains(body, security.RedactionMarker) || !strings.Contains(body, "unsafe value removed") {
		t.Fatalf("dashboard did not sanitize secret:\n%s", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/artifact?run=20260425-160000-redacted&path=validation/summary.md", nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("artifact status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, secret) || !strings.Contains(body, "Authorization: [jj-omitted]") {
		t.Fatalf("artifact did not redact secret:\n%s", body)
	}
}

func TestJSONArtifactAndManifestRedactSecretKeysWithSpaces(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260425-161000-jsonsecret"
	secret := "secret value with spaces"
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"started_at": "2026-04-25T16:10:00Z",
		"artifacts": {
			"manifest": "manifest.json",
			"json": "planning/secret.json"
		},
		"clientSecret": %q
	}`, runID, secret))
	writeFile(t, dir, ".jj/runs/"+runID+"/planning/secret.json", fmt.Sprintf(`{"token":%q,"visible":"ok"}`, secret))
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/manifest", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, secret) || !strings.Contains(body, "[jj-omitted]") {
		t.Fatalf("manifest response did not redact secret key:\n%s", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/artifact?run="+runID+"&path=planning/secret.json", nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("artifact status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, secret) || !strings.Contains(body, "[jj-omitted]") || !strings.Contains(body, "visible") || !strings.Contains(body, "ok") {
		t.Fatalf("json artifact response did not redact secret key:\n%s", body)
	}
}

func TestValidationArtifactsAreManifestKnownAndRedacted(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260426-160000-validation"
	secret := "sk-proj-servevalidation1234567890"
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"started_at": "2026-04-26T16:00:00Z",
		"artifacts": {
			"manifest": "manifest.json",
			"validation_summary": "validation/summary.md",
			"validation_results": "validation/results.json",
			"validation_001_stdout": "validation/001-validate.stdout.txt"
		},
		"validation": {
			"status": "passed",
			"evidence_status": "recorded",
			"summary_path": "validation/summary.md",
			"results_path": "validation/results.json"
			}
	}`, runID))
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/summary.md", "# Validation Evidence\n\n- Authorization: Bearer "+secret+"\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/results.json", `{"status":"passed"}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stdout.txt", "OPENAI_API_KEY="+secret+"\n")
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	if !strings.Contains(body, "evaluation passed (recorded)") || !strings.Contains(body, "Run detail") {
		t.Fatalf("dashboard missing latest validation state/link:\n%s", body)
	}
	if strings.Contains(body, secret) || strings.Contains(body, security.RedactionMarker) {
		t.Fatalf("dashboard leaked validation secret:\n%s", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/artifact?run="+runID+"&path=validation/001-validate.stdout.txt", nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("artifact status = %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, secret) || !strings.Contains(body, "[jj-omitted]") {
		t.Fatalf("validation artifact was not redacted:\n%s", body)
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
	req := httptest.NewRequest(http.MethodGet, "/artifact?run=20260425-120000-bbbbbb&path=snapshots/tasks.after.json", nil)
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

func TestRunShowsStateArtifactsFirst(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/run?id=20260425-120000-bbbbbb", nil)
	server.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, body)
	}
	spec := strings.Index(body, "snapshots/spec.after.json")
	task := strings.Index(body, "snapshots/tasks.after.json")
	manifest := strings.Index(body, "manifest.json")
	if spec < 0 || task < 0 || manifest < 0 || !(spec < task && task < manifest) || strings.Contains(body, "snapshots/eval.json") {
		t.Fatalf("state artifacts missing or not first in expected order:\n%s", body)
	}
}

func TestRunDetailShowsManifestMetadataAndGuardedLinks(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260428-090000-detail"
	secret := "run-detail-secret-value"
	t.Setenv("JJ_RUN_DETAIL_SECRET", secret)
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"started_at": "2026-04-28T09:00:00Z",
		"finished_at": "2026-04-28T09:00:03Z",
		"duration_ms": 3456,
		"dry_run": false,
		"planner_provider": "openai",
		"task_proposal_mode": "feature",
		"resolved_task_proposal_mode": "feature",
		"selected_task_id": "TASK-0001",
		"planner": {"provider": "openai", "model": "gpt-test"},
		"workspace": {"spec_path": ".jj/spec.json", "task_path": ".jj/tasks.json", "spec_written": true, "task_written": true},
		"artifacts": {
			"manifest": "manifest.json",
			"snapshot_spec_after": "snapshots/spec.after.json",
			"snapshot_tasks_after": "snapshots/tasks.after.json",
			"validation_summary": "validation/summary.md",
			"validation_results": "validation/results.json",
			"validation_stdout": "validation/001-validate.stdout.txt",
			"validation_stderr": "validation/001-validate.stderr.txt",
			"codex_summary": "codex/summary.md",
			"codex_events": "codex/events.jsonl",
			"codex_exit": "codex/exit.json",
			"missing": "validation/missing.md"
		},
		"validation": {
			"ran": true,
			"status": "passed",
			"evidence_status": "recorded",
			"summary": "validate passed",
			"results_path": "validation/results.json",
			"summary_path": "validation/summary.md",
			"command_count": 1,
			"passed_count": 1,
			"commands": [{
				"label": "validate",
				"name": "validate.sh",
				"command": "OPENAI_API_KEY=%s ./scripts/validate.sh",
				"provider": "local",
				"cwd": "[workspace]",
				"run_id": %q,
				"argv": ["./scripts/validate.sh"],
				"exit_code": 0,
				"duration_ms": 1200,
				"status": "passed",
				"stdout_path": "validation/001-validate.stdout.txt",
				"stderr_path": "validation/001-validate.stderr.txt"
			}]
		},
		"codex": {
			"ran": true,
			"status": "success",
			"model": "gpt-codex-test",
			"exit_code": 0,
			"duration_ms": 2200,
			"events_path": "codex/events.jsonl",
			"summary_path": "codex/summary.md",
			"exit_path": "codex/exit.json"
		},
		"security": {
			"redaction_applied": true,
			"workspace_guardrails_applied": true,
			"redaction_count": 3,
			"diagnostics": {
				"version": "1",
				"redacted": true,
				"root_labels": ["workspace", "run_artifacts"],
				"denied_path_count": 1,
				"denied_path_categories": ["outside_workspace"],
				"denied_path_category_counts": {"outside_workspace": 1},
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
	}`, runID, secret, runID))
	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/spec.after.json", `{"title":"SPEC"}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/tasks.after.json", `{"tasks":[{"id":"TASK-0001"}]}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/summary.md", "validation summary\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/results.json", `{"status":"passed"}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stdout.txt", "ok\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stderr.txt", "\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/summary.md", "summary\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/events.jsonl", `{"type":"done"}`+"\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/exit.json", fmt.Sprintf(`{"provider":"codex","name":"codex","model":"gpt-codex-test","cwd":"[workspace]","run_id":%q,"argv":["codex","--api-key=%s","exec"],"status":"success","exit_code":0,"duration_ms":2200}`, runID, secret))
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `href="/runs/`+runID+`"`) {
		t.Fatalf("dashboard did not link run detail:\n%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		"Overview",
		runID,
		"complete",
		"dry-run false",
		"provider openai",
		"model gpt-test",
		"selected task TASK-0001",
		"Generated State And Docs",
		"snapshots/spec.after.json",
		"snapshots/tasks.after.json",
		"Evaluation",
		"status passed",
		"Validation summary",
		"Codex",
		"gpt-codex-test",
		"Command Metadata",
		"./scripts/validate.sh",
		"raw command text not shown",
		"security redactions 3",
		"denied paths 1",
		"dry-run parity equivalent",
		"Run Artifacts",
		"Generated SPEC",
		"Generated TASK",
		"Evaluation",
		"Manifest summary",
		"Codex summary",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `href="/runs/audit?run=`+runID+`"`) {
		t.Fatalf("detail missing guarded audit export link:\n%s", body)
	}
	for _, leaked := range []string{
		secret,
		"OPENAI_API_KEY=",
		"JJ_RUN_DETAIL_SECRET",
		security.RedactionMarker,
		dir,
		filepath.ToSlash(dir),
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("detail leaked %q:\n%s", leaked, body)
		}
	}
	if !strings.Contains(body, `href="/artifact?run=`+runID) || strings.Contains(body, "validation/unlisted") || strings.Contains(body, "validation/missing.md") {
		t.Fatalf("detail did not use guarded artifact links:\n%s", body)
	}
}

func TestRunDetailValidationEvidenceShowsSanitizedCompletedRun(t *testing.T) {
	dir := t.TempDir()
	runID := "20260429-140000-evidence"
	secret := "sk-proj-rundetailevidence1234567890"
	t.Setenv("JJ_RUN_DETAIL_EVIDENCE_SECRET", secret)
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{
		"run_id":"`+runID+`",
		"status":"complete",
		"started_at":"2026-04-29T14:00:00Z",
		"artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md","validation_results":"validation/results.json"},
		"validation":{
			"ran":true,
			"status":"failed",
			"evidence_status":"recorded",
			"summary":"raw validation payload token=`+secret+` [omitted]",
			"reason":"raw command text OPENAI_API_KEY=`+secret+` ./scripts/validate.sh",
			"summary_path":"validation/summary.md",
			"results_path":"validation/results.json",
			"command_count":2,
			"passed_count":1,
			"failed_count":1,
			"commands":[
				{"label":"unit tests","status":"passed","stdout_path":"validation/stdout.txt","stderr_path":"validation/stderr.txt"},
				{"label":"`+secret+`","name":"OPENAI_API_KEY=`+secret+` ./scripts/validate.sh","status":"failed","error":"Authorization: Bearer `+secret+`"}
			]
		}
	}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", rec.Code, body)
	}
	section := runDetailValidationEvidenceSection(t, body)
	for _, want := range []string{
		"Validation Evidence",
		runID,
		"validation failed",
		"commands 2 · passed 1 · failed 1 · skipped 0 · errors 0",
		"2026-04-29T14:00:00Z",
		"unit tests",
		"status failed",
		`href="/runs/` + runID + `"`,
		`href="/runs/audit?run=` + runID + `"`,
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("validation evidence missing %q:\n%s", want, section)
		}
	}
	for _, leaked := range []string{
		secret,
		"raw validation payload",
		"raw command text",
		"OPENAI_API_KEY",
		"Authorization: Bearer",
		"validation/summary.md",
		"validation/results.json",
		"stdout",
		"stderr",
		"manifest.json",
		security.RedactionMarker,
		"[omitted]",
	} {
		if strings.Contains(section, leaked) {
			t.Fatalf("validation evidence leaked %q:\n%s", leaked, section)
		}
	}
	for _, want := range []string{"Overview", "Evaluation", "Codex", "Command Metadata", "Run Artifacts"} {
		if !strings.Contains(body, "<h2>"+want+"</h2>") {
			t.Fatalf("existing run detail section %q disappeared:\n%s", want, body)
		}
	}
}

func TestRunDetailValidationEvidenceStatesAreSafe(t *testing.T) {
	secret := "sk-proj-rundetailstates1234567890"
	cases := []struct {
		name        string
		runID       string
		setup       func(t *testing.T, dir, runID string)
		status      int
		wantSection bool
		want        []string
		forbidden   []string
	}{
		{
			name:  "no metadata",
			runID: "20260429-141000-none",
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T14:10:00Z","artifacts":{"manifest":"manifest.json"}}`)
			},
			status: http.StatusOK,
		},
		{
			name:        "missing metadata",
			runID:       "20260429-142000-missing",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T14:20:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"missing","evidence_status":"missing"}}`)
			},
			want: []string{"validation unavailable", "status missing", "2026-04-29T14:20:00Z"},
		},
		{
			name:        "partial metadata",
			runID:       "20260429-143000-partial",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"partial_failed","started_at":"2026-04-29T14:30:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"partial","evidence_status":"recorded","command_count":1,"failed_count":1}}`)
			},
			want: []string{"validation unavailable", "commands 1 · passed 0 · failed 1 · skipped 0 · errors 0", "status partial"},
		},
		{
			name:        "skipped metadata",
			runID:       "20260429-144000-skipped",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"dry_run_complete","started_at":"2026-04-29T14:40:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"skipped":true,"status":"skipped","evidence_status":"skipped","command_count":1,"commands":[{"label":"declared validation","status":"skipped"}]}}`)
			},
			want: []string{"validation skipped", "commands 1 · passed 0 · failed 0 · skipped 1 · errors 0", "declared validation"},
		},
		{
			name:        "stale metadata",
			runID:       "20260429-145000-stale",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T14:50:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"stale","evidence_status":"recorded"}}`)
			},
			want: []string{"validation unavailable", "status stale"},
		},
		{
			name:        "hostile token-like metadata",
			runID:       "20260429-146000-hostile",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				t.Setenv("JJ_RUN_DETAIL_EVIDENCE_STATE_SECRET", secret)
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{
					"run_id":"`+runID+`",
					"status":"complete",
					"started_at":"2026-04-29T15:00:00Z",
					"artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md"},
					"validation":{"ran":true,"status":"`+secret+`","summary":"raw validation payload token=`+secret+`","summary_path":"validation/summary.md","commands":[{"label":"`+secret+`","status":"error","error":"token=`+secret+`"}]}
				}`)
			},
			want:      []string{"validation unknown", "commands 1 · passed 0 · failed 0 · skipped 0 · errors 1", "status unknown"},
			forbidden: []string{secret, "raw validation payload", "token=", "validation/summary.md"},
		},
		{
			name:        "inconsistent metadata",
			runID:       "20260429-147000-inconsistent",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","started_at":"2026-04-29T15:10:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed","evidence_status":"recorded","command_count":1,"passed_count":1,"failed_count":1}}`)
			},
			want: []string{"validation unknown", "commands 1 · passed 1 · failed 1 · skipped 0 · errors 0", "status unknown"},
		},
		{
			name:        "malformed metadata",
			runID:       "20260429-148000-malformed",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"`+secret+`",`)
			},
			want:      []string{"validation unavailable", "category unavailable"},
			forbidden: []string{secret},
		},
		{
			name:   "denied run",
			runID:  "20260429-149000-denied",
			status: http.StatusForbidden,
			setup: func(t *testing.T, dir, runID string) {
				outside := t.TempDir()
				writeFile(t, outside, "manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed"}}`)
				if err := os.MkdirAll(filepath.Join(dir, ".jj/runs", runID), 0o755); err != nil {
					t.Fatalf("mkdir run: %v", err)
				}
				if err := os.Symlink(filepath.Join(outside, "manifest.json"), filepath.Join(dir, ".jj/runs", runID, "manifest.json")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			want: []string{"run unavailable"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir, tc.runID)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/runs/"+tc.runID, nil)
			newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != tc.status {
				t.Fatalf("detail status = %d, want %d body=%s", rec.Code, tc.status, body)
			}
			if !tc.wantSection {
				if strings.Contains(body, "<h2>Validation Evidence</h2>") {
					t.Fatalf("%s should not render validation evidence:\n%s", tc.name, body)
				}
				return
			}
			section := runDetailValidationEvidenceSection(t, body)
			for _, want := range append([]string{tc.runID}, tc.want...) {
				if !strings.Contains(section, want) {
					t.Fatalf("%s validation evidence missing %q:\n%s", tc.name, want, section)
				}
			}
			for _, leaked := range append(tc.forbidden, security.RedactionMarker, "[omitted]", "raw command text", "raw environment", "stdout", "stderr", "manifest.json") {
				if strings.Contains(section, leaked) {
					t.Fatalf("%s validation evidence leaked %q:\n%s", tc.name, leaked, section)
				}
			}
		})
	}
}

func TestRunDetailComparePreviousShowsSanitizedGuardedAction(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-compareprevious1234567890"
	t.Setenv("JJ_COMPARE_PREVIOUS_SECRET", secret)
	writeRunManifest(t, dir, "20260425-110000-aaaaaa", secret, "2026-04-25T11:00:00Z")
	writeRunManifest(t, dir, "20260425-123000-active", "running", "2026-04-25T12:30:00Z")
	tokenRunID := "sk-proj-comparepreviousrunid1234567890"
	writeFile(t, dir, ".jj/runs/"+tokenRunID+"/manifest.json", `{"run_id":"`+tokenRunID+`","status":"complete","started_at":"2026-04-25T11:59:00Z","artifacts":{"manifest":"manifest.json"}}`)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/20260425-120000-bbbbbb", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", rec.Code, body)
	}
	section := runDetailComparePreviousSection(t, body)
	for _, want := range []string{
		"Compare Previous",
		"Compare 20260425-120000-bbbbbb to 20260425-110000-aaaaaa",
		`href="/runs/compare?left=20260425-120000-bbbbbb&amp;right=20260425-110000-aaaaaa"`,
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("compare previous missing %q:\n%s", want, section)
		}
	}
	for _, leaked := range []string{
		secret,
		tokenRunID,
		"sk-proj",
		"manifest",
		"validation",
		"failed",
		"success",
		security.RedactionMarker,
		"[omitted]",
		dir,
		filepath.ToSlash(dir),
	} {
		if strings.Contains(section, leaked) {
			t.Fatalf("compare previous leaked %q:\n%s", leaked, section)
		}
	}
	for _, want := range []string{"Validation Evidence", "Codex", "Command Metadata", "Run Artifacts"} {
		if !strings.Contains(body, "<h2>"+want+"</h2>") {
			t.Fatalf("existing run detail section %q disappeared:\n%s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	dashboard := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", rec.Code, dashboard)
	}
	for _, want := range []string{"Recent Runs", "Active Run", "Validation Status", "Evaluation Findings"} {
		if !strings.Contains(dashboard, "<h2>"+want+"</h2>") {
			t.Fatalf("dashboard section %q disappeared:\n%s", want, dashboard)
		}
	}
	for _, leaked := range []string{secret, tokenRunID, "sk-proj", dir, filepath.ToSlash(dir)} {
		if strings.Contains(dashboard, leaked) {
			t.Fatalf("dashboard leaked compare previous fixture %q:\n%s", leaked, dashboard)
		}
	}
}

func TestRunDetailComparePreviousSafeStatesForAbsentMalformedAndDeniedRuns(t *testing.T) {
	secret := "sk-proj-comparepreviousstate1234567890"

	t.Run("absent previous", func(t *testing.T) {
		dir := t.TempDir()
		runID := "20260429-150000-solo"
		writeRunManifest(t, dir, runID, "complete", "2026-04-29T15:00:00Z")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
		newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("detail status = %d body=%s", rec.Code, body)
		}
		section := runDetailComparePreviousSection(t, body)
		if !strings.Contains(section, "Compare previous: none.") || strings.Contains(section, "/runs/compare?") {
			t.Fatalf("absent previous did not render deterministic none state:\n%s", section)
		}
	})

	t.Run("malformed current metadata", func(t *testing.T) {
		dir := t.TempDir()
		runID := "20260429-151000-malformed"
		writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"`+secret+`",`)
		writeRunManifest(t, dir, "20260429-150000-previous", "complete", "2026-04-29T15:00:00Z")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
		newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("detail status = %d body=%s", rec.Code, body)
		}
		section := runDetailComparePreviousSection(t, body)
		if !strings.Contains(section, "Compare previous: unavailable.") || strings.Contains(section, "/runs/compare?") {
			t.Fatalf("malformed metadata did not render unavailable state:\n%s", section)
		}
		for _, leaked := range []string{secret, "sk-proj", security.RedactionMarker, dir, filepath.ToSlash(dir)} {
			if strings.Contains(body, leaked) {
				t.Fatalf("malformed compare previous leaked %q:\n%s", leaked, body)
			}
		}
	})

	t.Run("denied run root", func(t *testing.T) {
		dir := t.TempDir()
		outside := t.TempDir()
		runID := "20260429-152000-denied"
		writeFile(t, outside, "manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
		writeFile(t, outside, "secret.txt", secret)
		if err := os.MkdirAll(filepath.Join(dir, ".jj/runs"), 0o755); err != nil {
			t.Fatalf("mkdir runs: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, ".jj/runs", runID)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
		newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusForbidden {
			t.Fatalf("denied run status = %d body=%s", rec.Code, body)
		}
		for _, leaked := range []string{secret, outside, filepath.ToSlash(outside), dir, filepath.ToSlash(dir), "Compare Previous"} {
			if strings.Contains(body, leaked) {
				t.Fatalf("denied compare previous leaked %q:\n%s", leaked, body)
			}
		}
	})
}

func TestRunDetailComparePreviousSelectionIsDeterministic(t *testing.T) {
	cases := []struct {
		name     string
		current  string
		previous string
		runs     []struct {
			id        string
			startedAt string
		}
	}{
		{
			name:     "tied timestamps use safe id order",
			current:  "20260429-160000-tie-c",
			previous: "20260429-160000-tie-b",
			runs: []struct {
				id        string
				startedAt string
			}{
				{id: "20260429-160000-tie-a", startedAt: "2026-04-29T16:00:00Z"},
				{id: "20260429-160000-tie-b", startedAt: "2026-04-29T16:00:00Z"},
				{id: "20260429-160000-tie-c", startedAt: "2026-04-29T16:00:00Z"},
			},
		},
		{
			name:     "missing and malformed timestamps fall back to safe id time",
			current:  "20260429-161000-clockless",
			previous: "20260429-160000-clockless-prev",
			runs: []struct {
				id        string
				startedAt string
			}{
				{id: "20260429-162000-newer", startedAt: ""},
				{id: "20260429-161000-clockless", startedAt: "not-a-timestamp"},
				{id: "20260429-160000-clockless-prev", startedAt: ""},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, run := range tc.runs {
				writeRunManifest(t, dir, run.id, "complete", run.startedAt)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/runs/"+tc.current, nil)
			newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("detail status = %d body=%s", rec.Code, body)
			}
			section := runDetailComparePreviousSection(t, body)
			wantURL := `/runs/compare?left=` + tc.current + `&amp;right=` + tc.previous
			if !strings.Contains(section, wantURL) || !strings.Contains(section, "Compare "+tc.current+" to "+tc.previous) {
				t.Fatalf("compare previous did not use deterministic previous %q:\n%s", tc.previous, section)
			}
		})
	}
}

func TestRunDetailRunArtifactsShowsAllowlistedInventoryAndPreservesSections(t *testing.T) {
	dir := t.TempDir()
	runID := "20260429-170000-artifacts"
	secret := "sk-proj-runartifacts1234567890"
	rawPath := filepath.Join(dir, "outside", "secret.txt")
	t.Setenv("JJ_RUN_ARTIFACT_SECRET", secret)
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id":%q,
		"status":"complete",
		"started_at":"2026-04-29T17:00:00Z",
		"artifacts":{
			"manifest":"manifest.json",
			"input_plan":"input/plan.md",
			"snapshot_spec_after":"snapshots/spec.after.json",
			"snapshot_tasks_after":"snapshots/tasks.after.json",
			"validation_summary":"validation/summary.md",
			"git_diff_summary":"git/diff-summary.txt",
			"codex_summary":"codex/summary.md",
			"hostile_label_%s":"planning/raw-response.txt",
			"token_like":"%s",
			"absolute":"%s"
		},
		"validation":{"status":"passed","evidence_status":"recorded","summary_path":"validation/summary.md","command_count":1,"passed_count":1},
		"git":{"diff_summary_path":"git/diff-summary.txt"},
		"codex":{"ran":true,"status":"success","summary_path":"codex/summary.md"}
	}`, runID, secret, secret, rawPath))
	for _, rel := range []string{
		"input/plan.md",
		"snapshots/spec.after.json",
		"snapshots/tasks.after.json",
		"validation/summary.md",
		"git/diff-summary.txt",
		"codex/summary.md",
		"planning/raw-response.txt",
	} {
		writeFile(t, dir, ".jj/runs/"+runID+"/"+rel, rel+"\n")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
	newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", rec.Code, body)
	}
	section := runDetailRunArtifactsSection(t, body)
	ordered := []string{"Input plan", "Generated SPEC", "Generated TASK", "Evaluation", "Manifest summary", "Git diff summary", "Codex summary"}
	last := -1
	for _, want := range ordered {
		idx := strings.Index(section, want)
		if idx < 0 {
			t.Fatalf("run artifact inventory missing %q:\n%s", want, section)
		}
		if idx < last {
			t.Fatalf("run artifact inventory order is not deterministic around %q:\n%s", want, section)
		}
		last = idx
	}
	for _, want := range []string{
		`href="/artifact?run=` + runID + `&amp;path=input%2Fplan.md"`,
		`href="/artifact?run=` + runID + `&amp;path=snapshots%2Fspec.after.json"`,
		`href="/artifact?run=` + runID + `&amp;path=validation%2Fsummary.md"`,
		"available",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("run artifact inventory missing %q:\n%s", want, section)
		}
	}
	for _, leaked := range []string{
		secret,
		"sk-proj",
		"hostile_label",
		"token_like",
		"absolute",
		rawPath,
		filepath.ToSlash(rawPath),
		"planning/raw-response.txt",
		"raw artifact body",
		security.RedactionMarker,
		"[omitted]",
	} {
		if strings.Contains(section, leaked) {
			t.Fatalf("run artifact inventory leaked %q:\n%s", leaked, section)
		}
	}
	for _, want := range []string{"Overview", "Generated State And Docs", "Evaluation", "Validation Evidence", "Compare Previous", "Codex", "Command Metadata", "Security Diagnostics"} {
		if !strings.Contains(body, "<h2>"+want+"</h2>") {
			t.Fatalf("existing run detail section %q disappeared:\n%s", want, body)
		}
	}
}

func TestRunDetailRunArtifactsSafeStates(t *testing.T) {
	secret := "sk-proj-runartifactstates1234567890"
	cases := []struct {
		name        string
		runID       string
		setup       func(t *testing.T, dir, runID string)
		status      int
		wantSection bool
		want        []string
		forbidden   []string
	}{
		{
			name:        "missing artifact metadata",
			runID:       "20260429-171000-missing",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{}}`)
			},
			want: []string{"No allowlisted run artifact metadata recorded.", "No allowlisted run artifacts recorded."},
		},
		{
			name:        "malformed metadata",
			runID:       "20260429-172000-malformed",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"`+secret+`",`)
			},
			want:      []string{"manifest is malformed", "No allowlisted run artifacts recorded."},
			forbidden: []string{secret, "sk-proj"},
		},
		{
			name:        "stale metadata",
			runID:       "20260429-173000-stale",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"stale","artifacts":{"manifest":"manifest.json","codex_summary":"codex/summary.md"}}`)
				writeFile(t, dir, ".jj/runs/"+runID+"/codex/summary.md", "stale summary\n")
			},
			want:      []string{"No allowlisted run artifact metadata recorded.", "No allowlisted run artifacts recorded."},
			forbidden: []string{"codex/summary.md"},
		},
		{
			name:        "partial metadata",
			runID:       "20260429-173500-partial",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete"}`)
			},
			want: []string{
				"manifest is incomplete: missing artifacts",
				"artifact links unavailable because this run lacks a trusted top-level artifacts map or trusted manifest",
				"No allowlisted run artifacts recorded.",
			},
		},
		{
			name:        "hostile and token-like metadata",
			runID:       "20260429-174000-hostile",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				t.Setenv("JJ_RUN_ARTIFACT_STATE_SECRET", secret)
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{
					"run_id":"`+runID+`",
					"status":"complete",
					"artifacts":{"manifest":"manifest.json","codex_summary":"`+secret+`","token=`+secret+`":"raw artifact body token=`+secret+`"}
				}`)
			},
			want:      []string{"Manifest summary", "Codex summary", "guarded artifact", "guarded"},
			forbidden: []string{secret, "sk-proj", "token=", "raw artifact body"},
		},
		{
			name:        "internally inconsistent metadata",
			runID:       "20260429-175000-inconsistent",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed","summary_path":"validation/summary.md"}}`)
			},
			want: []string{"Evaluation", "validation/summary.md", "not listed", "Manifest summary"},
		},
		{
			name:        "unavailable artifact metadata",
			runID:       "20260429-175500-unavailable",
			status:      http.StatusOK,
			wantSection: true,
			setup: func(t *testing.T, dir, runID string) {
				writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{"manifest":"manifest.json","codex_summary":"codex/summary.md"}}`)
				if err := os.MkdirAll(filepath.Join(dir, ".jj/runs", runID, "codex", "summary.md"), 0o755); err != nil {
					t.Fatalf("mkdir artifact directory: %v", err)
				}
			},
			want: []string{"Manifest summary", "Codex summary", "codex/summary.md", "unavailable"},
		},
		{
			name:   "denied run root",
			runID:  "20260429-176000-denied",
			status: http.StatusForbidden,
			setup: func(t *testing.T, dir, runID string) {
				outside := t.TempDir()
				writeFile(t, outside, "manifest.json", `{"run_id":"`+runID+`","status":"complete","artifacts":{"manifest":"manifest.json","codex_summary":"codex/summary.md"}}`)
				writeFile(t, outside, "secret.txt", secret)
				if err := os.MkdirAll(filepath.Join(dir, ".jj/runs"), 0o755); err != nil {
					t.Fatalf("mkdir runs: %v", err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, ".jj/runs", runID)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			want:      []string{"run id is not allowed"},
			forbidden: []string{secret, "Run Artifacts"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir, tc.runID)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/runs/"+tc.runID, nil)
			newTestServer(t, dir, "").Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != tc.status {
				t.Fatalf("detail status = %d, want %d body=%s", rec.Code, tc.status, body)
			}
			target := body
			if tc.wantSection {
				target = runDetailRunArtifactsSection(t, body)
			}
			for _, want := range tc.want {
				if !strings.Contains(target, want) {
					t.Fatalf("%s run artifact state missing %q:\n%s", tc.name, want, target)
				}
			}
			for _, leaked := range append(tc.forbidden, security.RedactionMarker, "[omitted]", "raw command text", "raw environment", "raw diff body") {
				if strings.Contains(target, leaked) {
					t.Fatalf("%s run artifact state leaked %q:\n%s", tc.name, leaked, target)
				}
			}
		})
	}
}

func TestRunAuditExportShowsSanitizedRunSummary(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260428-140000-audit"
	secret := "sk-proj-auditsecret1234567890"
	artifactBody := "audit artifact body should not be embedded"
	t.Setenv("JJ_RUN_AUDIT_SECRET", secret)
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"started_at": "2026-04-28T14:00:00Z",
		"finished_at": "2026-04-28T14:00:03Z",
		"duration_ms": 3456,
		"dry_run": true,
		"planner_provider": "openai",
		"task_proposal_mode": "feature",
		"resolved_task_proposal_mode": "feature",
		"selected_task_id": "TASK-0024",
		"planner": {"provider": "openai", "model": "gpt-audit"},
		"workspace": {"spec_path": ".jj/spec.json", "task_path": ".jj/tasks.json", "spec_written": true, "task_written": true},
		"artifacts": {
			"manifest": "manifest.json",
			"snapshot_spec_after": "snapshots/spec.after.json",
			"snapshot_tasks_after": "snapshots/tasks.after.json",
			"validation_summary": "validation/summary.md",
			"validation_results": "validation/results.json",
			"validation_stdout": "validation/001-validate.stdout.txt",
			"validation_stderr": "validation/001-validate.stderr.txt",
			"codex_summary": "codex/summary.md",
			"codex_events": "codex/events.jsonl",
			"codex_exit": "codex/exit.json"
		},
		"validation": {
			"ran": true,
			"status": "passed",
			"evidence_status": "recorded",
			"summary": "validate passed",
			"results_path": "validation/results.json",
			"summary_path": "validation/summary.md",
			"command_count": 1,
			"passed_count": 1,
			"commands": [{
				"label": "validate",
				"name": "validate.sh",
				"command": "OPENAI_API_KEY=%s ./scripts/validate.sh",
				"provider": "local",
				"cwd": %q,
				"run_id": %q,
				"argv": ["./scripts/validate.sh", "--token", "%s"],
				"exit_code": 0,
				"duration_ms": 1200,
				"status": "passed",
				"stdout_path": "validation/001-validate.stdout.txt",
				"stderr_path": "validation/001-validate.stderr.txt"
			}]
		},
		"codex": {
			"ran": true,
			"status": "success",
			"model": "gpt-codex-audit",
			"exit_code": 0,
			"duration_ms": 2200,
			"events_path": "codex/events.jsonl",
			"summary_path": "codex/summary.md",
			"exit_path": "codex/exit.json"
		},
		"security": {
			"redaction_applied": true,
			"workspace_guardrails_applied": true,
			"redaction_count": 6,
			"diagnostics": {
				"version": "1",
				"redacted": true,
				"secret_material_present": true,
				"root_labels": ["workspace", "run_artifacts", "token=%s"],
				"guarded_roots": [
					{"label": "workspace", "path": "[workspace]"},
					{"label": "run_artifacts", "path": ".jj/runs"},
					{"label": "unsafe", "path": %q}
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
	}`, runID, secret, dir, runID, secret, secret, filepath.Join(dir, "outside-"+secret), secret, secret))
	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/spec.after.json", `{"title":"SPEC"}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/tasks.after.json", `{"tasks":[{"id":"TASK-0024"}]}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/summary.md", artifactBody+" "+secret+"\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/results.json", `{"status":"passed"}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stdout.txt", "ok\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stderr.txt", "\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/summary.md", "codex summary "+secret+"\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/events.jsonl", `{"type":"done"}`+"\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/exit.json", fmt.Sprintf(`{"provider":"codex","name":"codex","model":"gpt-codex-audit","cwd":%q,"run_id":%q,"argv":["codex","--api-key=%s","exec"],"status":"success","exit_code":0,"duration_ms":2200}`, dir, runID, secret))
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID, nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `href="/runs/audit?run=`+runID+`"`) {
		t.Fatalf("detail did not link audit export: status=%d body=%s", rec.Code, rec.Body.String())
	}

	export, body := getRunAuditExport(t, server, "/runs/audit?run="+runID, http.StatusOK)
	if export.SchemaVersion != "jj.audit.v1" || export.State != "available" || export.RunID != runID || export.Status != "complete" {
		t.Fatalf("unexpected export overview: %#v\n%s", export, body)
	}
	if !export.DryRun || export.StartedAt == "" || export.FinishedAt == "" || export.Duration != "3.456s" {
		t.Fatalf("export missing timing/dry-run fields: %#v", export)
	}
	if export.Planner.Provider != "openai" || export.Planner.Model != "gpt-audit" || export.Planner.SelectedTaskID != "TASK-0024" {
		t.Fatalf("export missing planner metadata: %#v", export.Planner)
	}
	if len(export.GeneratedDocs) == 0 || len(export.Artifacts) == 0 || export.Evaluation.Status != "passed" || export.Evaluation.Results == nil || export.Evaluation.SummaryArtifact == nil {
		t.Fatalf("export missing doc/artifact/evaluation metadata: %#v", export)
	}
	if !export.Codex.Ran || export.Codex.Status != "success" || export.Codex.Model != "gpt-codex-audit" || export.Codex.Exit == nil {
		t.Fatalf("export missing codex metadata: %#v", export.Codex)
	}
	if len(export.Commands) < 2 || export.Commands[0].Note != "raw command text not shown" {
		t.Fatalf("export missing sanitized command metadata: %#v", export.Commands)
	}
	if !export.Security.Available || export.Security.RedactionCount != 6 || export.Security.DeniedPathCount != 2 || !export.Security.CommandMetadataSanitized || export.Security.RawCommandTextPersisted || export.Security.RawEnvironmentPersisted {
		t.Fatalf("export missing security diagnostics: %#v", export.Security)
	}
	if export.Security.DeniedPathCategoryCounts["outside_workspace"] != 1 || export.Security.DeniedPathCategoryCounts["path_denied"] != 1 {
		t.Fatalf("export did not sanitize denied path category counts: %#v", export.Security.DeniedPathCategoryCounts)
	}
	if len(export.NextActions) == 0 {
		t.Fatalf("export should include safe next-action hints: %#v", export)
	}
	for _, leaked := range []string{
		secret,
		"OPENAI_API_KEY",
		artifactBody,
		dir,
		filepath.ToSlash(dir),
		security.RedactionMarker,
		"[REDACTED]",
		"[omitted]",
		"{removed}",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("audit export leaked %q:\n%s", leaked, body)
		}
	}

	alias, _ := getRunAuditExport(t, server, "/runs/"+runID+"/audit.json", http.StatusOK)
	if alias.RunID != runID || alias.State != "available" {
		t.Fatalf("guarded path audit export mismatch: %#v", alias)
	}
}

func TestRunAuditExportRendersUnavailableManifestStates(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-auditbad1234567890"
	writeFile(t, dir, ".jj/runs/20260428-141000-badjson/manifest.json", `{"run_id":"20260428-141000-badjson","status":"`+secret+`",`)
	writeFile(t, dir, ".jj/runs/20260428-142000-partial/manifest.json", `{"run_id":"20260428-142000-partial","status":"success"}`)
	writeFile(t, dir, ".jj/runs/20260428-143000-legacy/manifest.json", `{"run_id":"20260428-143000-legacy","status":"success","started_at":"2026-04-28T14:30:00Z","artifacts":{"manifest":"manifest.json"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs/20260428-144000-missing"), 0o755); err != nil {
		t.Fatalf("mkdir missing manifest run: %v", err)
	}
	server := newTestServer(t, dir, "")

	probes := []struct {
		name          string
		target        string
		status        int
		state         string
		manifestState string
		security      string
	}{
		{name: "missing manifest", target: "/runs/audit?run=20260428-144000-missing", status: http.StatusOK, state: "unavailable", manifestState: "manifest unavailable", security: "security diagnostics unavailable"},
		{name: "malformed manifest", target: "/runs/audit?run=20260428-141000-badjson", status: http.StatusOK, state: "unavailable", manifestState: "manifest is malformed", security: "security diagnostics unavailable"},
		{name: "partial manifest", target: "/runs/audit?run=20260428-142000-partial", status: http.StatusOK, state: "unavailable", manifestState: "manifest is incomplete: missing artifacts", security: "security diagnostics unavailable"},
		{name: "older manifest", target: "/runs/audit?run=20260428-143000-legacy", status: http.StatusOK, state: "available", manifestState: "manifest available", security: "security diagnostics unavailable"},
		{name: "missing run", target: "/runs/audit?run=20260428-149999-notfound", status: http.StatusNotFound, state: "unavailable", manifestState: "run unavailable", security: "security diagnostics unavailable"},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			export, body := getRunAuditExport(t, server, probe.target, probe.status)
			if export.State != probe.state || export.ManifestState != probe.manifestState || export.Security.Summary != probe.security {
				t.Fatalf("unexpected unavailable export: %#v\n%s", export, body)
			}
			for _, leaked := range []string{secret, security.RedactionMarker, dir, filepath.ToSlash(dir)} {
				if strings.Contains(body, leaked) {
					t.Fatalf("unavailable audit export leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func TestRunAuditExportDeniesUnsafeRunInputs(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	secret := "run-audit-outside-secret"
	target := filepath.Join(outside, "target")
	writeFile(t, target, "manifest.json", `{"run_id":"20260428-145000-link","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, target, "secret.txt", secret)
	if err := os.Symlink(target, filepath.Join(dir, ".jj/runs/20260428-145000-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")
	absolute := filepath.Join(outside, "target-"+secret)

	probes := []struct {
		name   string
		target string
		status int
		state  string
	}{
		{name: "missing query", target: "/runs/audit", status: http.StatusBadRequest, state: "unavailable"},
		{name: "duplicate query", target: "/runs/audit?run=20260425-120000-bbbbbb&run=20260425-110000-aaaaaa", status: http.StatusForbidden, state: "denied"},
		{name: "relative traversal query", target: "/runs/audit?run=..%2f" + url.QueryEscape(secret), status: http.StatusForbidden, state: "denied"},
		{name: "absolute query", target: "/runs/audit?run=" + url.QueryEscape(absolute), status: http.StatusForbidden, state: "denied"},
		{name: "encoded slash query", target: "/runs/audit?run=20260428-145000-link%2f..%2fother", status: http.StatusForbidden, state: "denied"},
		{name: "encoded route traversal", target: "/runs/%2e%2e/audit", status: http.StatusForbidden, state: "denied"},
		{name: "symlink run root", target: "/runs/audit?run=20260428-145000-link", status: http.StatusForbidden, state: "denied"},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			export, body := getRunAuditExport(t, server, probe.target, probe.status)
			if export.State != probe.state {
				t.Fatalf("audit export state = %q, want %q: %#v\n%s", export.State, probe.state, export, body)
			}
			for _, leaked := range []string{secret, "../", absolute, filepath.ToSlash(absolute), outside, filepath.ToSlash(outside), dir, filepath.ToSlash(dir), security.RedactionMarker} {
				if strings.Contains(body, leaked) {
					t.Fatalf("unsafe audit export leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func TestRunInspectionUnavailableStatesAreConsistentAcrossGuardedSurfaces(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-inspectionstate1234567890"
	cases := []struct {
		id       string
		manifest string
		want     string
	}{
		{
			id:       "20260428-151000-missingstatus",
			manifest: `{"run_id":"20260428-151000-missingstatus","started_at":"2026-04-28T15:10:00Z","artifacts":{"manifest":"manifest.json"},"errors":["token=` + secret + `"]}`,
			want:     "manifest is incomplete: missing status",
		},
		{
			id:       "20260428-152000-missingrunid",
			manifest: `{"status":"complete","started_at":"2026-04-28T15:20:00Z","artifacts":{"manifest":"manifest.json"},"errors":["token=` + secret + `"]}`,
			want:     "manifest is incomplete: missing run_id",
		},
		{
			id:       "20260428-153000-mismatch",
			manifest: `{"run_id":"sk-proj-mismatchsecret1234567890","status":"complete","started_at":"2026-04-28T15:30:00Z","artifacts":{"manifest":"manifest.json"},"errors":["token=` + secret + `"]}`,
			want:     "manifest is incomplete: run_id mismatch",
		},
	}
	for _, tc := range cases {
		writeFile(t, dir, ".jj/runs/"+tc.id+"/manifest.json", tc.manifest)
	}
	server := newTestServer(t, dir, "")
	validID := "20260425-120000-bbbbbb"

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			targets := []string{
				"/runs/" + tc.id,
				"/runs",
				"/runs/compare?left=" + tc.id + "&right=" + validID,
			}
			for _, target := range targets {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, target, nil)
				server.Handler().ServeHTTP(rec, req)
				body := rec.Body.String()
				if rec.Code != http.StatusOK {
					t.Fatalf("%s status = %d body=%s", target, rec.Code, body)
				}
				if !strings.Contains(body, tc.want) {
					t.Fatalf("%s missing shared unavailable state %q:\n%s", target, tc.want, body)
				}
				for _, leaked := range []string{secret, "sk-proj-mismatchsecret1234567890", dir, filepath.ToSlash(dir)} {
					if strings.Contains(body, leaked) {
						t.Fatalf("%s leaked %q:\n%s", target, leaked, body)
					}
				}
			}

			export, body := getRunAuditExport(t, server, "/runs/audit?run="+tc.id, http.StatusOK)
			if export.State != "unavailable" || export.ManifestState != tc.want {
				t.Fatalf("audit export state mismatch: %#v\n%s", export, body)
			}
			for _, leaked := range []string{secret, "sk-proj-mismatchsecret1234567890", dir, filepath.ToSlash(dir)} {
				if strings.Contains(body, leaked) {
					t.Fatalf("audit export leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func TestRunDetailRendersSafeStatesForMalformedMissingAndLegacyManifests(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-detailbad1234567890"
	writeFile(t, dir, ".jj/runs/20260428-100000-badjson/manifest.json", `{"run_id":"20260428-100000-badjson","status":"`+secret+`",`)
	writeFile(t, dir, ".jj/runs/20260428-101000-incomplete/manifest.json", `{"run_id":"20260428-101000-incomplete","status":"success"}`)
	writeFile(t, dir, ".jj/runs/20260428-102000-legacy/manifest.json", `{"run_id":"20260428-102000-legacy","status":"success","started_at":"2026-04-28T10:20:00Z","artifacts":{"manifest":"manifest.json"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs/20260428-103000-missing"), 0o755); err != nil {
		t.Fatalf("mkdir missing manifest: %v", err)
	}
	server := newTestServer(t, dir, "")

	probes := []struct {
		target string
		want   string
	}{
		{"/runs/20260428-100000-badjson", "manifest is malformed"},
		{"/runs/20260428-101000-incomplete", "manifest is incomplete: missing artifacts"},
		{"/runs/20260428-102000-legacy", "security diagnostics unavailable"},
		{"/runs/20260428-103000-missing", "manifest unavailable"},
	}
	for _, probe := range probes {
		t.Run(probe.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, probe.target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("detail status = %d body=%s", rec.Code, body)
			}
			if !strings.Contains(body, probe.want) {
				t.Fatalf("detail missing %q:\n%s", probe.want, body)
			}
			for _, leaked := range []string{secret, security.RedactionMarker, dir, filepath.ToSlash(dir)} {
				if strings.Contains(body, leaked) {
					t.Fatalf("safe state leaked %q:\n%s", leaked, body)
				}
			}
		})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/20260428-109999-notfound", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "run unavailable") {
		t.Fatalf("missing run status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRunDetailRejectsUnsafeRunIDsAndSymlinkEscapes(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	secret := "run-detail-outside-secret"
	target := filepath.Join(outside, "target")
	writeFile(t, target, "manifest.json", `{"run_id":"20260428-110000-link","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, target, "secret.txt", secret)
	if err := os.Symlink(target, filepath.Join(dir, ".jj/runs/20260428-110000-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")

	probes := []struct {
		name   string
		target string
		status int
	}{
		{name: "relative traversal", target: "/runs/../README.md", status: http.StatusForbidden},
		{name: "encoded traversal", target: "/runs/%2e%2e/README.md", status: http.StatusForbidden},
		{name: "encoded slash traversal", target: "/runs/20260428-110000-link%2f..%2fother", status: http.StatusForbidden},
		{name: "absolute query", target: "/run?id=" + url.QueryEscape(filepath.Join(outside, "target")), status: http.StatusForbidden},
		{name: "symlink run root", target: "/runs/20260428-110000-link", status: http.StatusForbidden},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, probe.target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != probe.status {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, probe.status, body)
			}
			for _, leaked := range []string{secret, outside, filepath.ToSlash(outside), dir, filepath.ToSlash(dir)} {
				if strings.Contains(body, leaked) {
					t.Fatalf("unsafe run detail leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func TestRunCompareShowsSanitizedSideBySideMetadata(t *testing.T) {
	dir := newTestWorkspace(t)
	leftID := "20260428-120000-compare-left"
	rightID := "20260428-121000-compare-right"
	secret := "sk-proj-comparesecret1234567890"
	t.Setenv("JJ_RUN_COMPARE_SECRET", secret)
	writeFile(t, dir, ".jj/runs/"+leftID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"started_at": "2026-04-28T12:00:00Z",
		"finished_at": "2026-04-28T12:00:03Z",
		"duration_ms": 3456,
		"dry_run": false,
		"planner_provider": "openai",
		"task_proposal_mode": "feature",
		"resolved_task_proposal_mode": "feature",
		"selected_task_id": "TASK-0003",
		"planner": {"provider": "openai", "model": "gpt-test"},
		"workspace": {"spec_path": ".jj/spec.json", "task_path": ".jj/tasks.json", "spec_written": true, "task_written": true},
		"artifacts": {
			"manifest": "manifest.json",
			"snapshot_spec_after": "snapshots/spec.after.json",
			"snapshot_tasks_after": "snapshots/tasks.after.json",
			"validation_summary": "validation/summary.md",
			"validation_results": "validation/results.json",
			"validation_stdout": "validation/001-validate.stdout.txt",
			"validation_stderr": "validation/001-validate.stderr.txt",
			"codex_summary": "codex/summary.md",
			"codex_events": "codex/events.jsonl",
			"codex_exit": "codex/exit.json"
		},
		"validation": {
			"ran": true,
			"status": "passed",
			"evidence_status": "recorded",
			"summary": "validate passed",
			"results_path": "validation/results.json",
			"summary_path": "validation/summary.md",
			"command_count": 1,
			"passed_count": 1,
			"commands": [{
				"label": "validate",
				"name": "validate.sh",
				"command": "OPENAI_API_KEY=%s ./scripts/validate.sh",
				"provider": "local",
				"cwd": "[workspace]",
				"run_id": %q,
				"argv": ["./scripts/validate.sh"],
				"exit_code": 0,
				"duration_ms": 1200,
				"status": "passed",
				"stdout_path": "validation/001-validate.stdout.txt",
				"stderr_path": "validation/001-validate.stderr.txt"
			}]
		},
		"codex": {
			"ran": true,
			"status": "success",
			"model": "gpt-codex-test",
			"exit_code": 0,
			"duration_ms": 2200,
			"events_path": "codex/events.jsonl",
			"summary_path": "codex/summary.md",
			"exit_path": "codex/exit.json"
		},
		"security": {
			"redaction_applied": true,
			"workspace_guardrails_applied": true,
			"redaction_count": 4,
			"diagnostics": {
				"version": "1",
				"redacted": true,
				"root_labels": ["workspace", "run_artifacts"],
				"denied_path_count": 1,
				"denied_path_categories": ["outside_workspace"],
				"denied_path_category_counts": {"outside_workspace": 1},
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
	}`, leftID, secret, leftID))
	writeFile(t, dir, ".jj/runs/"+leftID+"/snapshots/spec.after.json", `{"title":"SPEC"}`)
	writeFile(t, dir, ".jj/runs/"+leftID+"/snapshots/tasks.after.json", `{"tasks":[{"id":"TASK-0003"}]}`)
	writeFile(t, dir, ".jj/runs/"+leftID+"/validation/summary.md", "validation summary with "+secret+"\n")
	writeFile(t, dir, ".jj/runs/"+leftID+"/validation/results.json", `{"status":"passed"}`)
	writeFile(t, dir, ".jj/runs/"+leftID+"/validation/001-validate.stdout.txt", "ok\n")
	writeFile(t, dir, ".jj/runs/"+leftID+"/validation/001-validate.stderr.txt", "\n")
	writeFile(t, dir, ".jj/runs/"+leftID+"/codex/summary.md", "codex summary "+secret+"\n")
	writeFile(t, dir, ".jj/runs/"+leftID+"/codex/events.jsonl", `{"type":"done"}`+"\n")
	writeFile(t, dir, ".jj/runs/"+leftID+"/codex/exit.json", fmt.Sprintf(`{"argv":["codex","--api-key=%s","exec"]}`, secret))
	writeFile(t, dir, ".jj/runs/"+rightID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "failed",
		"started_at": "2026-04-28T12:10:00Z",
		"finished_at": "2026-04-28T12:10:01Z",
		"dry_run": true,
		"planner_provider": "codex",
		"planner": {"provider": "codex", "model": "gpt-right"},
		"artifacts": {"manifest": "manifest.json", "validation_summary": "validation/summary.md"},
		"validation": {"ran": true, "status": "failed", "evidence_status": "recorded", "summary_path": "validation/summary.md"},
		"codex": {"skipped": true, "status": "skipped"}
	}`, rightID))
	writeFile(t, dir, ".jj/runs/"+rightID+"/validation/summary.md", "failed\n")
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/compare?left="+leftID+"&right="+rightID, nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("compare status = %d body=%s", rec.Code, body)
	}
	for _, want := range []string{
		"Left Run",
		"Right Run",
		leftID,
		rightID,
		"status complete",
		"status failed",
		"dry-run false",
		"dry-run true",
		"provider openai",
		"provider codex",
		"selected task TASK-0003",
		"Generated State And Docs",
		"snapshots/spec.after.json",
		"Evaluation",
		"status passed",
		"Validation summary",
		"Codex",
		"gpt-codex-test",
		"Codex command metadata",
		"Command Metadata",
		"./scripts/validate.sh",
		"raw command text not shown",
		"metadata from manifest",
		"security redactions 4",
		"denied paths 1",
		"dry-run parity equivalent",
		"Artifact Availability",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("compare missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `href="/artifact?run=`+leftID) {
		t.Fatalf("compare did not use guarded artifact links:\n%s", body)
	}
	for _, leaked := range []string{
		secret,
		"OPENAI_API_KEY=",
		"--api-key",
		security.RedactionMarker,
		"[omitted]",
		"{removed}",
		dir,
		filepath.ToSlash(dir),
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("compare leaked %q:\n%s", leaked, body)
		}
	}
}

func TestRunCompareHandlesInvalidMissingIdenticalAndUnsafeQueries(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")
	validID := "20260425-120000-bbbbbb"
	otherID := "20260425-110000-aaaaaa"
	secret := "sk-proj-comparequery1234567890"
	absolute := filepath.Join(dir, "outside-"+secret)

	probes := []struct {
		name   string
		target string
		want   []string
	}{
		{
			name:   "missing left",
			target: "/runs/compare?right=" + validID,
			want:   []string{"Left Run", "run id is required", "Right Run", validID},
		},
		{
			name:   "invalid traversal",
			target: "/runs/compare?left=..%2f" + url.QueryEscape(secret) + "&right=" + validID,
			want:   []string{"run id is not allowed", validID},
		},
		{
			name:   "absolute path",
			target: "/runs/compare?left=" + url.QueryEscape(absolute) + "&right=" + validID,
			want:   []string{"run id is not allowed", validID},
		},
		{
			name:   "duplicate right",
			target: "/runs/compare?left=" + validID + "&right=" + validID + "&right=" + otherID,
			want:   []string{"exactly one run id is required"},
		},
		{
			name:   "identical",
			target: "/runs/compare?left=" + validID + "&right=" + validID,
			want:   []string{"Comparison requires two different run IDs.", "identical run IDs are not compared"},
		},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, probe.target, nil)
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("compare status = %d body=%s", rec.Code, body)
			}
			for _, want := range probe.want {
				if !strings.Contains(body, want) {
					t.Fatalf("compare missing %q:\n%s", want, body)
				}
			}
			for _, leaked := range []string{secret, "../", absolute, filepath.ToSlash(absolute), dir, filepath.ToSlash(dir), security.RedactionMarker} {
				if strings.Contains(body, leaked) {
					t.Fatalf("invalid compare leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func TestRunCompareRendersMalformedMissingPartialAndLegacySafely(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "sk-proj-comparebad1234567890"
	writeFile(t, dir, ".jj/runs/20260428-122000-badjson/manifest.json", `{"run_id":"20260428-122000-badjson","status":"`+secret+`",`)
	writeFile(t, dir, ".jj/runs/20260428-123000-incomplete/manifest.json", `{"run_id":"20260428-123000-incomplete","status":"success"}`)
	writeFile(t, dir, ".jj/runs/20260428-124000-legacy/manifest.json", `{"run_id":"20260428-124000-legacy","status":"success","started_at":"2026-04-28T12:40:00Z","artifacts":{"manifest":"manifest.json"}}`)
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs/20260428-125000-missing"), 0o755); err != nil {
		t.Fatalf("mkdir missing manifest: %v", err)
	}
	server := newTestServer(t, dir, "")

	for _, target := range []string{
		"/runs/compare?left=20260428-122000-badjson&right=20260428-125000-missing",
		"/runs/compare?left=20260428-123000-incomplete&right=20260428-124000-legacy",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("compare status = %d body=%s", rec.Code, body)
		}
		for _, leaked := range []string{secret, security.RedactionMarker, dir, filepath.ToSlash(dir)} {
			if strings.Contains(body, leaked) {
				t.Fatalf("compare safe state leaked %q:\n%s", leaked, body)
			}
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs/compare?left=20260428-122000-badjson&right=20260428-125000-missing", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"manifest is malformed", "manifest unavailable"} {
		if !strings.Contains(body, want) {
			t.Fatalf("compare missing %q:\n%s", want, body)
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/runs/compare?left=20260428-123000-incomplete&right=20260428-124000-legacy", nil)
	server.Handler().ServeHTTP(rec, req)
	body = rec.Body.String()
	for _, want := range []string{"manifest is incomplete: missing artifacts", "20260428-124000-legacy", "security diagnostics unavailable", "diagnostics unknown"} {
		if !strings.Contains(body, want) {
			t.Fatalf("compare missing %q:\n%s", want, body)
		}
	}
}

func TestRunCompareRejectsSymlinkRunRootWithoutLeaks(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	secret := "run-compare-outside-secret"
	target := filepath.Join(outside, "target")
	writeFile(t, target, "manifest.json", `{"run_id":"20260428-130000-link","status":"complete","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, target, "secret.txt", secret)
	if err := os.Symlink(target, filepath.Join(dir, ".jj/runs/20260428-130000-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")

	for _, targetURL := range []string{
		"/runs/compare?left=20260428-130000-link&right=20260425-120000-bbbbbb",
		"/runs/compare?left=20260428-130000-link%2f..%2fother&right=20260425-120000-bbbbbb",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, targetURL, nil)
		server.Handler().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("compare status = %d body=%s", rec.Code, body)
		}
		if !strings.Contains(body, "denied") || !strings.Contains(body, "run id is not allowed") {
			t.Fatalf("compare did not deny unsafe run input:\n%s", body)
		}
		for _, leaked := range []string{secret, outside, filepath.ToSlash(outside), dir, filepath.ToSlash(dir)} {
			if strings.Contains(body, leaked) {
				t.Fatalf("unsafe compare leaked %q:\n%s", leaked, body)
			}
		}
	}
}

func TestRunHistoryProvidesGuardedCompareLinks(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d body=%s", rec.Code, body)
	}
	want := `/runs/compare?left=20260425-120000-bbbbbb&amp;right=20260425-110000-aaaaaa`
	if !strings.Contains(body, want) || !strings.Contains(body, ">compare</a>") {
		t.Fatalf("history missing guarded compare link %q:\n%s", want, body)
	}
	for _, leaked := range []string{`/runs/compare?left=..`, `href="/run?id=`} {
		if strings.Contains(body, leaked) {
			t.Fatalf("history compare links included unsafe legacy target %q:\n%s", leaked, body)
		}
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
		"/artifact?run=20260425-120000-bbbbbb&path=snapshots/tasks.after.json%00",
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

func TestServeRejectsSecretLookingRunIDsWithoutReflection(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")
	for _, runID := range []string{
		"sk-proj-serverunid1234567890",
		"AbCdEfGhIjKlMnOpQrStUvWxYz12345678901234",
	} {
		probes := []struct {
			method string
			target string
			body   string
		}{
			{method: http.MethodGet, target: "/runs/" + runID},
			{method: http.MethodGet, target: "/runs/" + runID + "/manifest"},
			{method: http.MethodGet, target: "/artifact?run=" + runID + "&path=snapshots/tasks.after.json"},
			{method: http.MethodGet, target: "/run/status?id=" + runID},
			{method: http.MethodGet, target: "/runs/audit?run=" + runID},
			{method: http.MethodGet, target: "/runs/compare?left=" + runID + "&right=20260425-120000-bbbbbb"},
			{method: http.MethodPost, target: "/run/start", body: runStartPromptForm(runID, "Build from prompt.\n", true, false, false, 0)},
		}
		for _, probe := range probes {
			t.Run(probe.method+" "+probe.target, func(t *testing.T) {
				reqBody := strings.NewReader(probe.body)
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(probe.method, probe.target, reqBody)
				if probe.body != "" {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				server.Handler().ServeHTTP(rec, req)
				body := rec.Body.String()
				if rec.Code < 400 && !strings.Contains(probe.target, "/runs/compare") {
					t.Fatalf("expected rejection, got %d body=%s", rec.Code, body)
				}
				if strings.Contains(probe.target, "/runs/compare") && (!strings.Contains(body, "denied") || !strings.Contains(body, "run id is not allowed")) {
					t.Fatalf("expected compare denial, got %d body=%s", rec.Code, body)
				}
				for _, leaked := range []string{runID, "sk-proj", "AbCdEf", security.RedactionMarker, dir, filepath.ToSlash(dir)} {
					if strings.Contains(body, leaked) {
						t.Fatalf("secret-looking run id rejection leaked %q:\n%s", leaked, body)
					}
				}
			})
		}
	}
}

func TestArtifactHTTPStackRejectsUnsafePathsWithoutLeaks(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "unsafe-secret-token-1234567890"
	t.Setenv("JJ_UNSAFE_PATH_TOKEN", secret)
	server := newTestServer(t, dir, "")
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	absLeak := filepath.Join(dir, "outside-"+secret+".md")
	probes := []struct {
		name  string
		query string
	}{
		{name: "raw traversal", query: "run=20260425-120000-bbbbbb&path=docs/../manifest.json"},
		{name: "encoded slash traversal", query: "run=20260425-120000-bbbbbb&path=docs%2f..%2fmanifest.json"},
		{name: "encoded dot traversal", query: "run=20260425-120000-bbbbbb&path=docs/%2e%2e/manifest.json"},
		{name: "hidden traversal", query: "run=20260425-120000-bbbbbb&path=.secret/../manifest.json"},
		{name: "absolute path", query: "run=20260425-120000-bbbbbb&path=" + url.QueryEscape(absLeak)},
		{name: "windows drive", query: "run=20260425-120000-bbbbbb&path=C:/" + secret + ".md"},
		{name: "unc path", query: "run=20260425-120000-bbbbbb&path=//server/share/" + secret + ".md"},
		{name: "backslash traversal", query: "run=20260425-120000-bbbbbb&path=docs%5c..%5cmanifest.json"},
		{name: "nul byte", query: "run=20260425-120000-bbbbbb&path=snapshots/tasks.after.json%00"},
		{name: "hidden segment", query: "run=20260425-120000-bbbbbb&path=docs/.secret"},
		{name: "hidden artifact", query: "run=20260425-120000-bbbbbb&path=codex/.env"},
		{name: "secret unlisted path", query: "run=20260425-120000-bbbbbb&path=docs/" + secret + ".md"},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			resp, err := http.Get(httpServer.URL + "/artifact?" + probe.query)
			if err != nil {
				t.Fatalf("get probe: %v", err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			body := string(data)
			if resp.StatusCode < 400 {
				t.Fatalf("expected rejection, got %d body=%s", resp.StatusCode, body)
			}
			for _, leaked := range []string{dir, filepath.ToSlash(dir), absLeak, filepath.ToSlash(absLeak), secret} {
				if strings.Contains(body, leaked) {
					t.Fatalf("%s response leaked %q:\n%s", probe.name, leaked, body)
				}
			}
		})
	}

	resp, err := http.Get(httpServer.URL + "/artifact?run=20260425-120000-bbbbbb&path=snapshots/tasks.after.json")
	if err != nil {
		t.Fatalf("get valid artifact: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read valid artifact: %v", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Do the task") {
		t.Fatalf("valid artifact did not serve: status=%d body=%s", resp.StatusCode, body)
	}

	resp, err = http.Get(httpServer.URL + "/doc?path=.jj/tasks.json")
	if err != nil {
		t.Fatalf("get public doc: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read public doc: %v", err)
	}
	body = string(bodyBytes)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "TASK-0001") {
		t.Fatalf("public doc did not serve: status=%d body=%s", resp.StatusCode, body)
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

func TestArtifactSymlinkTraversalRejected(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	outsideTask := filepath.Join(outside, "tasks.after.json")
	if err := os.WriteFile(outsideTask, []byte(`{"secret":"Outside Task"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write outside task: %v", err)
	}
	linkPath := filepath.Join(dir, ".jj", "runs", "20260425-120000-bbbbbb", "snapshots", "tasks.after.json")
	if err := os.Remove(linkPath); err != nil {
		t.Fatalf("remove task artifact: %v", err)
	}
	if err := os.Symlink(outsideTask, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifact?run=20260425-120000-bbbbbb&path=snapshots/tasks.after.json", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code < 400 {
		t.Fatalf("expected symlink artifact rejection, got %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, "Outside Task") || strings.Contains(body, outside) {
		t.Fatalf("symlink rejection leaked outside data:\n%s", body)
	}
}

func TestArtifactInternalRunRootSymlinkRejected(t *testing.T) {
	dir := newTestWorkspace(t)
	target := filepath.Join(dir, "run-target")
	runID := "20260425-190000-link"
	if err := os.MkdirAll(filepath.Join(target, "validation"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "manifest.json"), []byte(fmt.Sprintf(`{"run_id":%q,"status":"complete","started_at":"2026-04-25T19:00:00Z","artifacts":{"manifest":"manifest.json","validation_summary":"validation/summary.md"}}`, runID)), 0o644); err != nil {
		t.Fatalf("write target manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "validation", "summary.md"), []byte("should not serve\n"), 0o644); err != nil {
		t.Fatalf("write target summary: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".jj", "runs", runID)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := newTestServer(t, dir, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifact?run="+runID+"&path=validation/summary.md", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code < 400 {
		t.Fatalf("expected symlinked run root rejection, got %d body=%s", rec.Code, body)
	}
	if strings.Contains(body, "should not serve") || strings.Contains(body, target) {
		t.Fatalf("symlinked run root rejection leaked target data:\n%s", body)
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
	for !strings.Contains(out.String(), "jj: serving dashboard at http://") {
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

func TestWebRunPromptStartsWithoutPlanPath(t *testing.T) {
	dir := newTestWorkspace(t)
	executor := &loopFakeExecutor{
		results:     []string{runpkg.StatusComplete},
		validations: []string{"passed"},
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartPromptForm("prompt-run", "Build from the browser prompt.\n", true, false, false, 0)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	waitForRunStatus(t, server, "prompt-run", runpkg.StatusComplete)
	call := executor.callFor("prompt-run")
	if call.PlanText != "Build from the browser prompt.\n" || call.PlanPath != "" || call.PlanInputName != runpkg.DefaultWebPromptInput {
		t.Fatalf("expected prompt-backed run config, got %#v", call)
	}
	if call.TaskProposalMode != runpkg.TaskProposalModeAuto {
		t.Fatalf("expected default task proposal mode auto, got %#v", call)
	}
}

func TestWebRunStatusUsesSafeDisplayPaths(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServerWithExecutor(t, Config{
		CWD:  dir,
		Addr: "127.0.0.1:7331",
		RunExecutor: func(_ context.Context, cfg runpkg.Config) (*runpkg.Result, error) {
			runDir := filepath.Join(cfg.CWD, ".jj", "runs", cfg.RunID)
			fmt.Fprintf(cfg.Stdout, "jj: creating run directory %s\nrun_dir=%s\nreview=jj serve --cwd %s\n", runDir, runDir, cfg.CWD)
			manifest := fmt.Sprintf(`{"run_id":%q,"status":"complete","started_at":"2026-04-25T00:00:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed"},"commit":{"ran":false,"status":"skipped"}}`, cfg.RunID)
			if err := writeFakeRunFile(runDir, "manifest.json", manifest); err != nil {
				return nil, err
			}
			return &runpkg.Result{RunID: cfg.RunID, RunDir: runDir}, nil
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartPromptForm("path-safe-run", "Build from prompt.\n", true, false, false, 0)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	status := waitForRunStatus(t, server, "path-safe-run", runpkg.StatusSuccess)
	if status.CurrentTurn.RunDir != ".jj/runs/path-safe-run" || status.RunDir != ".jj/runs/path-safe-run" {
		t.Fatalf("expected relative display run dir, got %#v", status)
	}
	body := getRunStatusBody(t, server, "path-safe-run")
	for _, leaked := range []string{dir, filepath.ToSlash(dir), filepath.Join(dir, ".jj", "runs", "path-safe-run"), filepath.ToSlash(filepath.Join(dir, ".jj", "runs", "path-safe-run"))} {
		if strings.Contains(body, leaked) {
			t.Fatalf("web run status leaked %q:\n%s", leaked, body)
		}
	}
	if !strings.Contains(body, "[path]") {
		t.Fatalf("web run logs should retain path redaction evidence:\n%s", body)
	}
}

func TestWebRunStartPassesTaskProposalMode(t *testing.T) {
	dir := newTestWorkspace(t)
	executor := &loopFakeExecutor{
		results:     []string{runpkg.StatusComplete},
		validations: []string{"passed"},
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	values := url.Values{}
	values.Set("plan_prompt", "Harden the dashboard.\n")
	values.Set("run_id", "mode-run")
	values.Set("planning_agents", "1")
	values.Set("task_proposal_mode", "security")
	values.Set("dry_run", "true")
	values.Set("allow_no_git", "true")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	waitForRunStatus(t, server, "mode-run", runpkg.StatusComplete)
	call := executor.callFor("mode-run")
	if call.TaskProposalMode != runpkg.TaskProposalModeSecurity || !call.TaskProposalModeExplicit {
		t.Fatalf("expected security proposal mode config, got %#v", call)
	}
}

func TestWebRunStartPassesRepositoryOptions(t *testing.T) {
	dir := newTestWorkspace(t)
	executor := &loopFakeExecutor{
		results:     []string{runpkg.StatusComplete},
		validations: []string{"passed"},
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	values := url.Values{}
	values.Set("plan_prompt", "Build in a GitHub workspace.\n")
	values.Set("run_id", "repo-web-run")
	values.Set("planning_agents", "1")
	values.Set("task_proposal_mode", "feature")
	values.Set("repo", "https://github.com/acme/app.git")
	values.Set("repo_dir", "/tmp/acme-app")
	values.Set("base_branch", "main")
	values.Set("work_branch", "jj/web-run")
	values.Set("push", "true")
	values.Set("push_mode", "branch")
	values.Set("github_token_env", "MY_GITHUB_TOKEN")
	values.Set("allow_dirty", "true")
	values.Set("dry_run", "true")
	values.Set("allow_no_git", "true")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	waitForRunStatus(t, server, "repo-web-run", runpkg.StatusComplete)
	call := executor.callFor("repo-web-run")
	if call.RepoURL != "https://github.com/acme/app.git" || call.RepoDir != "/tmp/acme-app" || call.BaseBranch != "main" || call.WorkBranch != "jj/web-run" {
		t.Fatalf("expected repository options, got %#v", call)
	}
	if !call.RepoURLExplicit || !call.RepoDirExplicit || !call.BaseBranchExplicit || !call.WorkBranchExplicit {
		t.Fatalf("expected repository explicit markers, got %#v", call)
	}
	if !call.Push || call.PushMode != "branch" || call.GitHubTokenEnv != "MY_GITHUB_TOKEN" || !call.RepoAllowDirty {
		t.Fatalf("expected push/token/dirty options, got %#v", call)
	}
	if !call.PushExplicit || !call.PushModeExplicit || !call.GitHubTokenEnvExplicit || !call.RepoAllowDirtyExplicit {
		t.Fatalf("expected repository option explicit markers, got %#v", call)
	}
}

func TestWebRunStartRejectsInvalidTaskProposalMode(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: (&loopFakeExecutor{}).Run,
	})

	values := url.Values{}
	values.Set("plan_prompt", "Build from prompt.\n")
	values.Set("run_id", "bad-mode")
	values.Set("planning_agents", "1")
	values.Set("task_proposal_mode", "fast")
	values.Set("dry_run", "true")
	values.Set("allow_no_git", "true")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid task proposal mode") {
		t.Fatalf("expected invalid mode rejection, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebRunStartRejectsEmptyPromptAndPlanPath(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: (&loopFakeExecutor{}).Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartPromptForm("empty-input", " \n", true, false, false, 0)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "plan path or prompt is required") {
		t.Fatalf("expected empty prompt and plan path rejection, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebRunStartRejectsUnsafePlanPathWithoutLeaks(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: (&loopFakeExecutor{}).Run,
	})

	for _, tc := range []struct {
		name     string
		planPath string
		want     string
		leaks    []string
	}{
		{
			name:     "backslash",
			planPath: `docs\unsafe-secret-token-1234567890.md`,
			want:     "plan path is not allowed",
			leaks:    []string{`docs\unsafe-secret-token-1234567890.md`, "unsafe-secret-token-1234567890"},
		},
		{
			name:     "missing",
			planPath: "missing.md",
			want:     "plan path is not readable",
			leaks:    []string{dir, filepath.ToSlash(dir), "missing.md"},
		},
		{
			name:     "secret-looking",
			planPath: "sk-proj-webplansecret1234567890.md",
			want:     "plan path is not allowed",
			leaks:    []string{"sk-proj-webplansecret1234567890"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := url.Values{}
			values.Set("plan_path", tc.planPath)
			values.Set("run_id", "bad-plan-path-"+tc.name)
			values.Set("dry_run", "true")
			values.Set("allow_no_git", "true")

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			server.Handler().ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusBadRequest || !strings.Contains(body, tc.want) {
				t.Fatalf("expected plan path rejection %q, got %d body=%s", tc.want, rec.Code, body)
			}
			for _, leaked := range tc.leaks {
				if strings.Contains(body, leaked) {
					t.Fatalf("unsafe plan path rejection leaked %q:\n%s", leaked, body)
				}
			}
		})
	}
}

func TestWebRunPromptAutoContinuesWithOriginalPrompt(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	executor := &loopFakeExecutor{
		results:     []string{"needs_work", runpkg.StatusSuccess, runpkg.StatusSuccess},
		validations: []string{"skipped", "passed", "passed"},
	}
	server := newTestServerWithExecutor(t, Config{
		CWD:         dir,
		Addr:        "127.0.0.1:7331",
		RunExecutor: executor.Run,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartPromptForm("prompt-loop", "Keep improving from this browser prompt.\n", false, true, true, 3)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	status := waitForRunStatus(t, server, "prompt-loop", runpkg.StatusSuccess)
	if len(status.Turns) != 3 || status.CurrentTurn.RunID != "prompt-loop-t03" || status.Phase != "max_turns" {
		t.Fatalf("expected three turns and max-turn stop at t03, got %#v", status)
	}
	second := executor.callFor("prompt-loop-t02")
	if second.PlanText != "Keep improving from this browser prompt.\n" {
		t.Fatalf("second turn should reuse original prompt, got %#v", second)
	}
	if !strings.Contains(second.AdditionalPlanContext, "Previous Manifest") {
		t.Fatalf("second turn did not receive continuation context: %q", second.AdditionalPlanContext)
	}
}

func TestWebRunAutoContinuesAfterPassUntilMaxTurns(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	executor := &loopFakeExecutor{
		results:     []string{"needs_work", runpkg.StatusSuccess, runpkg.StatusSuccess},
		validations: []string{"skipped", "passed", "passed"},
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

	status := waitForRunStatus(t, server, "loop-pass", runpkg.StatusSuccess)
	if len(status.Turns) != 3 || status.CurrentTurn.RunID != "loop-pass-t03" || status.Phase != "max_turns" {
		t.Fatalf("expected three turns and max-turn stop at t03, got %#v", status)
	}
	if !strings.Contains(executor.contextFor("loop-pass-t02"), "Previous Manifest") {
		t.Fatalf("second turn did not receive continuation context: %q", executor.contextFor("loop-pass-t02"))
	}
}

func TestWebRunFinishStopsAfterCurrentTurn(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	release := make(chan struct{})
	executor := &loopFakeExecutor{
		results:     []string{"needs_work", runpkg.StatusSuccess},
		validations: []string{"skipped", "passed"},
		block:       release,
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

	status := waitForRunStatus(t, server, "loop-finish", "needs_work")
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
		results:     []string{runpkg.StatusSuccess},
		validations: []string{"passed"},
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

func TestWebRunIgnoresReportedRunDirOutsideWorkspace(t *testing.T) {
	dir := newTestWorkspace(t)
	outside := t.TempDir()
	server := newTestServerWithExecutor(t, Config{
		CWD:  dir,
		Addr: "127.0.0.1:7331",
		RunExecutor: func(_ context.Context, cfg runpkg.Config) (*runpkg.Result, error) {
			runDir := filepath.Join(cfg.CWD, ".jj", "runs", cfg.RunID)
			manifest := fmt.Sprintf(`{"run_id":%q,"status":"complete","started_at":"2026-04-25T00:00:00Z","artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed"},"commit":{"ran":false,"status":"skipped"}}`, cfg.RunID)
			if err := writeFakeRunFile(runDir, "manifest.json", manifest); err != nil {
				return nil, err
			}
			return &runpkg.Result{RunID: cfg.RunID, RunDir: outside}, nil
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/run/start", strings.NewReader(runStartPromptForm("unsafe-run-dir", "Build from prompt.\n", true, false, false, 0)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	status := waitForRunStatus(t, server, "unsafe-run-dir", runpkg.StatusComplete)
	if filepath.Clean(status.CurrentTurn.RunDir) == filepath.Clean(outside) {
		t.Fatalf("outside run dir was trusted in web status: %#v", status)
	}
	if _, err := os.Stat(filepath.Join(outside, "web-run.log")); !os.IsNotExist(err) {
		t.Fatalf("outside run dir should not receive web-run.log, err=%v", err)
	}
	body := getRunStatusBody(t, server, "unsafe-run-dir")
	if strings.Contains(body, outside) || strings.Contains(body, filepath.ToSlash(outside)) {
		t.Fatalf("status response leaked outside run dir:\n%s", body)
	}
}

func TestWebRunAutoContinueAllowsDirtyWorkspace(t *testing.T) {
	dir := newCleanGitWorkspace(t)
	writeFile(t, dir, "dirty.txt", "dirty\n")
	executor := &loopFakeExecutor{
		results:     []string{runpkg.StatusSuccess},
		validations: []string{"passed"},
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
	status := waitForRunStatus(t, server, "dirty-loop", runpkg.StatusSuccess)
	if len(status.Turns) != 3 || status.Phase != "max_turns" {
		t.Fatalf("expected dirty workspace loop to continue until max turns, got %#v", status)
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

func getRunAuditExport(t *testing.T, server *Server, target string, wantStatus int) (runAuditExport, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != wantStatus {
		t.Fatalf("audit export status for %s = %d, want %d body=%s", target, rec.Code, wantStatus, body)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("audit export content type = %q body=%s", got, body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("audit export Cache-Control = %q, want no-store", got)
	}
	var export runAuditExport
	if err := json.Unmarshal([]byte(body), &export); err != nil {
		t.Fatalf("decode audit export: %v\n%s", err, body)
	}
	return export, body
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

func htmlSection(body, heading, nextHeading string) string {
	start := strings.Index(body, "<h2>"+heading+"</h2>")
	if start < 0 {
		return body
	}
	section := body[start:]
	if nextHeading == "" {
		return section
	}
	if end := strings.Index(section, "<h2>"+nextHeading+"</h2>"); end >= 0 {
		return section[:end]
	}
	return section
}

func dashboardNextActionSection(t *testing.T, server *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", rec.Code, body)
	}
	return htmlSection(body, "Next Action", "Project Docs")
}

func dashboardEvaluationFindingsSection(t *testing.T, server *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", rec.Code, body)
	}
	return htmlSection(body, "Evaluation Findings", "Recent Runs")
}

func dashboardValidationStatusSection(t *testing.T, body string) string {
	t.Helper()
	if !strings.Contains(body, "<h2>Validation Status</h2>") {
		t.Fatalf("dashboard missing Validation Status section:\n%s", body)
	}
	return htmlSection(body, "Validation Status", "Evaluation Findings")
}

func dashboardActiveRunSection(t *testing.T, body string) string {
	t.Helper()
	if !strings.Contains(body, "<h2>Active Run</h2>") {
		t.Fatalf("dashboard missing Active Run section:\n%s", body)
	}
	return htmlSection(body, "Active Run", "State Files")
}

func assertDashboardRunActions(t *testing.T, section, runID string) {
	t.Helper()
	want := `<a href="/runs/` + runID + `">Run detail</a> · <a href="/runs/audit?run=` + runID + `">Audit export</a>`
	if !strings.Contains(section, want) {
		t.Fatalf("dashboard run-summary actions missing %q:\n%s", want, section)
	}
}

func runDetailValidationEvidenceSection(t *testing.T, body string) string {
	t.Helper()
	if !strings.Contains(body, "<h2>Validation Evidence</h2>") {
		t.Fatalf("run detail missing Validation Evidence section:\n%s", body)
	}
	return htmlSection(body, "Validation Evidence", "Codex")
}

func runDetailComparePreviousSection(t *testing.T, body string) string {
	t.Helper()
	if !strings.Contains(body, "<h2>Compare Previous</h2>") {
		t.Fatalf("run detail missing Compare Previous section:\n%s", body)
	}
	return htmlSection(body, "Compare Previous", "Command Metadata")
}

func runDetailRunArtifactsSection(t *testing.T, body string) string {
	t.Helper()
	if !strings.Contains(body, "<h2>Run Artifacts</h2>") {
		t.Fatalf("run detail missing Run Artifacts section:\n%s", body)
	}
	return htmlSection(body, "Run Artifacts", "Next Actions")
}

type loopFakeExecutor struct {
	mu          sync.Mutex
	calls       []runpkg.Config
	results     []string
	validations []string
	block       <-chan struct{}
	blocked     bool
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
	validation := "passed"
	if callIndex < len(f.validations) && f.validations[callIndex] != "" {
		validation = f.validations[callIndex]
	}
	runDir := filepath.Join(cfg.CWD, ".jj", "runs", cfg.RunID)
	if err := writeFakeRunFile(runDir, "snapshots/spec.after.json", `{"version":1,"title":"SPEC","summary":"summary"}`); err != nil {
		return nil, err
	}
	if err := writeFakeRunFile(runDir, "snapshots/tasks.after.json", `{"version":1,"tasks":[{"id":"TASK-0001","title":"Task","mode":"feature","status":"done"}]}`); err != nil {
		return nil, err
	}
	_ = writeFakeRunFile(filepath.Join(cfg.CWD, ".jj"), "spec.json", `{"version":1,"title":"SPEC","summary":"summary"}`)
	_ = writeFakeRunFile(filepath.Join(cfg.CWD, ".jj"), "tasks.json", `{"version":1,"tasks":[{"id":"TASK-0001","title":"Task","mode":"feature","status":"done"}]}`)
	if err := writeFakeRunFile(runDir, "validation/summary.md", "validation "+validation+"\n"); err != nil {
		return nil, err
	}
	if err := writeFakeRunFile(runDir, "git/diff-summary.txt", "## git diff --stat\nfake.go\n"); err != nil {
		return nil, err
	}
	if err := writeFakeRunFile(runDir, "codex/summary.md", "Changed files: fake.go\n"); err != nil {
		return nil, err
	}
	manifest := fmt.Sprintf(`{"run_id":%q,"status":%q,"started_at":"2026-04-25T00:00:00Z","finished_at":"2026-04-25T00:00:01Z","artifacts":{"manifest":"manifest.json","snapshot_spec_after":"snapshots/spec.after.json","snapshot_tasks_after":"snapshots/tasks.after.json","git_diff_summary":"git/diff-summary.txt","codex_summary":"codex/summary.md","validation_summary":"validation/summary.md"},"validation":{"status":%q,"summary_path":"validation/summary.md"},"commit":{"ran":false,"status":"skipped"}}`, cfg.RunID, status, validation)
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
	values := "plan_path=plan.md&cwd=&run_id=" + runID + "&planning_agents=1"
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

func runStartPromptForm(runID, prompt string, dryRun, confirm, autoContinue bool, maxTurns int) string {
	values := url.Values{}
	values.Set("plan_prompt", prompt)
	values.Set("cwd", "")
	values.Set("run_id", runID)
	values.Set("planning_agents", "1")
	values.Set("allow_no_git", "true")
	if dryRun {
		values.Set("dry_run", "true")
	}
	if confirm {
		values.Set("confirm_full_run", "true")
	}
	if autoContinue {
		values.Set("auto_continue", "true")
	}
	if maxTurns > 0 {
		values.Set("max_turns", fmt.Sprintf("%d", maxTurns))
	}
	return values.Encode()
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
	writeFile(t, dir, "docs/SPEC.md", "# Spec Doc\n")
	writeFile(t, dir, "docs/TASK.md", "# Task Doc\n")
	writeFile(t, dir, ".jj/spec.json", `{"version":1,"title":"SPEC","summary":"Do the spec."}`)
	writeFile(t, dir, ".jj/tasks.json", `{"version":1,"active_task_id":null,"tasks":[{"id":"TASK-0001","title":"Secure artifacts","mode":"security","status":"queued"}]}`)
	writeFile(t, dir, "docs/guide.md", "# Guide\n")
	writeFile(t, dir, "playground/plan.md", "# Plan\n")
	writeFile(t, dir, ".git/ignored.md", "# ignored\n")
	writeFile(t, dir, ".jj/runs/20260425-110000-aaaaaa/manifest.json", `{"run_id":"20260425-110000-aaaaaa","status":"success","started_at":"2026-04-25T11:00:00Z","artifacts":{"manifest":"manifest.json"}}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/manifest.json", `{"run_id":"20260425-120000-bbbbbb","status":"failed","started_at":"2026-04-25T12:00:00Z","task_proposal_mode":"auto","resolved_task_proposal_mode":"security","selected_task_id":"TASK-0001","repository":{"enabled":true,"provider":"github","repo_url":"https://user:ghp_dashboardsecret1234567890@github.com/acme/app.git","sanitized_repo_url":"https://github.com/acme/app.git","repo_dir":"/tmp/acme-app","base_branch":"main","work_branch":"jj/run-20260425-120000-bbbbbb","push_enabled":true,"push_mode":"branch","pushed":true,"push_status":"pushed","pushed_ref":"origin/jj/run-20260425-120000-bbbbbb"},"artifacts":{"manifest":"manifest.json","snapshot_spec_after":"snapshots/spec.after.json","snapshot_tasks_after":"snapshots/tasks.after.json","validation_summary":"validation/summary.md"},"validation":{"status":"failed","summary_path":"validation/summary.md"},"risks":["review required"]}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/snapshots/spec.after.json", `{"version":1,"title":"SPEC","summary":"Do the spec."}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/snapshots/tasks.after.json", `{"version":1,"tasks":[{"id":"TASK-0001","title":"Do the task.","mode":"security","status":"queued"}]}`)
	writeFile(t, dir, ".jj/runs/20260425-120000-bbbbbb/validation/summary.md", "failed\n")
	return dir
}

func writeRunManifest(t *testing.T, root, runID, status, startedAt string) {
	t.Helper()
	started := ""
	if strings.TrimSpace(startedAt) != "" {
		started = fmt.Sprintf(`,"started_at":%q`, startedAt)
	}
	writeFile(t, root, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{"run_id":%q,"status":%q%s,"artifacts":{"manifest":"manifest.json"},"validation":{"status":"passed","evidence_status":"recorded"}}`, runID, status, started))
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
