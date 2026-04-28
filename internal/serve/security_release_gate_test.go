package serve

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jungju/jj/internal/security"
)

func TestSecurityReleaseGateGuardedArtifactsAndRunInspectionSurfaces(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "release-gate-configured-secret-value"
	apiKey := "sk-proj-releasegate1234567890"
	githubToken := "github_pat_releasegate1234567890abcdefghijklmnop"
	privateKeyBody := "release-gate-private-key-body"
	rawCommand := "OPENAI_API_KEY=" + apiKey + " ./scripts/validate.sh --token " + secret
	rawEnv := "JJ_RELEASE_GATE_SECRET=" + secret
	rawArtifactBody := "raw release gate artifact body should not be embedded"
	t.Setenv("JJ_RELEASE_GATE_SECRET", secret)

	dryID := "20260428-210000-release-dry"
	fullID := "20260428-211000-release-full"
	writeSecurityReleaseGateRun(t, dir, dryID, true, "dry_run_complete")
	writeSecurityReleaseGateRun(t, dir, fullID, false, "complete")

	forbidden := []string{
		secret,
		apiKey,
		githubToken,
		privateKeyBody,
		"-----BEGIN PRIVATE KEY-----",
		"-----END PRIVATE KEY-----",
		rawCommand,
		rawEnv,
		"OPENAI_API_KEY=",
		"JJ_RELEASE_GATE_SECRET",
		"[REDACTED]",
		"[redacted]",
		"[omitted]",
		"<hidden>",
		"{removed}",
		"Bearer [jj-omitted]",
		"bearer/[jj-omitted]",
	}
	assertSecurityReleaseGateFilesClean(t, dir, []string{dryID, fullID}, forbidden)

	badID := "20260428-212000-release-badjson"
	partialID := "20260428-213000-release-partial"
	olderID := "20260428-214000-release-older"
	missingID := "20260428-215000-release-missing"
	writeFile(t, dir, ".jj/runs/"+badID+"/manifest.json", `{"run_id":"`+badID+`","status":"`+apiKey+`",`)
	writeFile(t, dir, ".jj/runs/"+partialID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "complete",
		"errors": ["%s", "%s", "%s"],
		"validation": {"summary": %q}
	}`, partialID, rawCommand, rawEnv, githubToken, rawArtifactBody))
	writeFile(t, dir, ".jj/runs/"+olderID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": "success",
		"started_at": "2026-04-28T21:40:00Z",
		"artifacts": {"manifest": "manifest.json"}
	}`, olderID))
	if err := os.MkdirAll(filepath.Join(dir, ".jj/runs", missingID), 0o755); err != nil {
		t.Fatalf("mkdir missing run: %v", err)
	}

	outside := t.TempDir()
	outsideSecretFile := filepath.Join(outside, "outside-"+secret+".txt")
	if err := os.WriteFile(outsideSecretFile, []byte(secret+"\n"), 0o644); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}
	symlinkID := "20260428-216000-release-link"
	linkTarget := filepath.Join(outside, "run-target")
	writeFile(t, linkTarget, "manifest.json", fmt.Sprintf(`{"run_id":%q,"status":"complete","artifacts":{"manifest":"manifest.json"}}`, symlinkID))
	if err := os.Symlink(linkTarget, filepath.Join(dir, ".jj/runs", symlinkID)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	server := newTestServer(t, dir, "")
	extraForbidden := append([]string{}, forbidden...)
	extraForbidden = append(extraForbidden,
		dir,
		filepath.ToSlash(dir),
		outside,
		filepath.ToSlash(outside),
		outsideSecretFile,
		filepath.ToSlash(outsideSecretFile),
		linkTarget,
		filepath.ToSlash(linkTarget),
		rawArtifactBody,
		security.RedactionMarker,
	)

	inspectionDry := server.loadRunInspection(dryID)
	inspectionFull := server.loadRunInspection(fullID)
	for _, inspection := range []runInspection{inspectionDry, inspectionFull} {
		if inspection.State != "available" || !inspection.TrustedManifest {
			t.Fatalf("release gate run did not load through shared inspection path: %#v", inspection)
		}
		if inspection.Detail.SecuritySummary != inspection.History.SecuritySummary || inspection.AuditSecurity.Summary != inspection.Detail.SecuritySummary {
			t.Fatalf("shared DTO surfaces diverged for %s: detail=%q history=%q audit=%q", inspection.ID, inspection.Detail.SecuritySummary, inspection.History.SecuritySummary, inspection.AuditSecurity.Summary)
		}
		if inspection.Detail.Validation.Status != "passed" || inspection.Detail.Validation.FailedCount != 0 {
			t.Fatalf("validation DTO should expose safe pass counts only: %#v", inspection.Detail.Validation)
		}
	}
	assertSecurityReleaseGateParity(t, inspectionDry, inspectionFull)

	compare := server.loadRunCompare(url.Values{"left": []string{fullID}, "right": []string{dryID}})
	if len(compare.Sides) != 2 || compare.Sides[0].State != "available" || compare.Sides[1].State != "available" {
		t.Fatalf("compare did not use shared available inspections: %#v", compare)
	}
	if compare.Sides[0].SecuritySummary != inspectionFull.Detail.SecuritySummary || compare.Sides[1].SecuritySummary != inspectionDry.Detail.SecuritySummary {
		t.Fatalf("compare security summary diverged from detail DTO: %#v", compare.Sides)
	}

	routeProbes := []struct {
		name   string
		target string
		status int
		want   []string
	}{
		{name: "dashboard", target: "/", status: http.StatusOK, want: []string{fullID, "security redactions 6"}},
		{name: "history", target: "/runs", status: http.StatusOK, want: []string{fullID, dryID, "compare"}},
		{name: "history filtered dry", target: "/runs?dry_run=true&q=release", status: http.StatusOK, want: []string{dryID, "Filtered run history."}},
		{name: "detail dry", target: "/runs/" + dryID, status: http.StatusOK, want: []string{dryID, "dry-run true", "security redactions 6", "dry-run parity equivalent"}},
		{name: "detail full", target: "/runs/" + fullID, status: http.StatusOK, want: []string{fullID, "dry-run false", "Codex command metadata", "Command Metadata"}},
		{name: "legacy run query", target: "/run?id=" + fullID, status: http.StatusOK, want: []string{fullID, "manifest available"}},
		{name: "compare", target: "/runs/compare?left=" + fullID + "&right=" + dryID, status: http.StatusOK, want: []string{"Left Run", "Right Run", fullID, dryID, "security redactions 6"}},
		{name: "audit full", target: "/runs/audit?run=" + fullID, status: http.StatusOK, want: []string{`"state":"available"`, `"command_metadata_sanitized":true`, `"dry_run_parity_status":"equivalent"`}},
		{name: "audit path alias", target: "/runs/" + dryID + "/audit.json", status: http.StatusOK, want: []string{`"run_id":"` + dryID + `"`, `"state":"available"`}},
		{name: "manifest sanitized", target: "/runs/" + fullID + "/manifest", status: http.StatusOK, want: []string{`"run_id": "` + fullID + `"`, `"raw_command_text_persisted": false`}},
		{name: "validation artifact", target: "/artifact?run=" + fullID + "&path=validation/summary.md", status: http.StatusOK, want: []string{"validation passed"}},
		{name: "codex events artifact", target: "/artifact?run=" + fullID + "&path=codex/events.jsonl", status: http.StatusOK, want: []string{"done", "success"}},
		{name: "codex command artifact", target: "/artifact?run=" + fullID + "&path=codex/exit.json", status: http.StatusOK, want: []string{"provider", "codex"}},
		{name: "malformed detail", target: "/runs/" + badID, status: http.StatusOK, want: []string{"manifest is malformed"}},
		{name: "partial detail", target: "/runs/" + partialID, status: http.StatusOK, want: []string{"manifest is incomplete: missing artifacts"}},
		{name: "older detail", target: "/runs/" + olderID, status: http.StatusOK, want: []string{"security diagnostics unavailable"}},
		{name: "missing manifest detail", target: "/runs/" + missingID, status: http.StatusOK, want: []string{"manifest unavailable"}},
		{name: "missing run detail", target: "/runs/20260428-219999-release-notfound", status: http.StatusNotFound, want: []string{"run unavailable"}},
		{name: "identical compare", target: "/runs/compare?left=" + fullID + "&right=" + fullID, status: http.StatusOK, want: []string{"Comparison requires two different run IDs.", "identical run IDs are not compared"}},
		{name: "missing audit", target: "/runs/audit?run=20260428-219999-release-notfound", status: http.StatusNotFound, want: []string{`"state":"unavailable"`, `"manifest_state":"run unavailable"`}},
	}
	for _, probe := range routeProbes {
		t.Run(probe.name, func(t *testing.T) {
			body := securityReleaseGateServe(t, server, probe.target, probe.status)
			for _, want := range probe.want {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing %q:\n%s", probe.target, want, body)
				}
			}
			assertSecurityReleaseGateClean(t, probe.target, body, extraForbidden)
		})
	}

	unsafeAbsolute := filepath.Join(outside, "absolute-"+secret+".md")
	unsafeProbes := []struct {
		name   string
		target string
		status int
		want   []string
	}{
		{name: "artifact traversal", target: "/artifact?run=" + fullID + "&path=validation/../manifest.json", status: http.StatusForbidden, want: []string{"artifact path is not allowed"}},
		{name: "artifact encoded traversal", target: "/artifact?run=" + fullID + "&path=validation%2f..%2fmanifest.json", status: http.StatusForbidden, want: []string{"artifact path is not allowed"}},
		{name: "artifact absolute", target: "/artifact?run=" + fullID + "&path=" + url.QueryEscape(unsafeAbsolute), status: http.StatusForbidden, want: []string{"artifact path is not allowed"}},
		{name: "detail relative traversal", target: "/runs/..%2f" + url.QueryEscape(secret), status: http.StatusForbidden, want: []string{"request path is not allowed"}},
		{name: "detail absolute query", target: "/run?id=" + url.QueryEscape(unsafeAbsolute), status: http.StatusForbidden, want: []string{"run id is not allowed"}},
		{name: "detail symlink root", target: "/runs/" + symlinkID, status: http.StatusForbidden, want: []string{"run id is not allowed"}},
		{name: "compare encoded escape", target: "/runs/compare?left=" + fullID + "%2f..%2fother&right=" + dryID, status: http.StatusOK, want: []string{"denied", "run id is not allowed"}},
		{name: "compare duplicate right", target: "/runs/compare?left=" + fullID + "&right=" + dryID + "&right=" + olderID, status: http.StatusOK, want: []string{"exactly one run id is required"}},
		{name: "audit duplicate query", target: "/runs/audit?run=" + fullID + "&run=" + dryID, status: http.StatusForbidden, want: []string{`"state":"denied"`}},
		{name: "audit encoded escape", target: "/runs/audit?run=" + fullID + "%2f..%2fother", status: http.StatusForbidden, want: []string{`"state":"denied"`, `"run id is not allowed"`}},
		{name: "audit symlink root", target: "/runs/audit?run=" + symlinkID, status: http.StatusForbidden, want: []string{`"state":"denied"`}},
	}
	for _, runID := range []string{
		"sk-proj-releasegate-runid1234567890",
		"AbCdEfGhIjKlMnOpQrStUvWxYz12345678901234",
	} {
		unsafeProbes = append(unsafeProbes,
			struct {
				name   string
				target string
				status int
				want   []string
			}{name: "secret-looking detail", target: "/runs/" + runID, status: http.StatusForbidden, want: []string{"run id is not allowed"}},
			struct {
				name   string
				target string
				status int
				want   []string
			}{name: "secret-looking audit", target: "/runs/audit?run=" + runID, status: http.StatusForbidden, want: []string{`"state":"denied"`}},
			struct {
				name   string
				target string
				status int
				want   []string
			}{name: "secret-looking compare", target: "/runs/compare?left=" + runID + "&right=" + fullID, status: http.StatusOK, want: []string{"denied", "run id is not allowed"}},
		)
		extraForbidden = append(extraForbidden, runID)
	}
	extraForbidden = append(extraForbidden, unsafeAbsolute, filepath.ToSlash(unsafeAbsolute), "../", "%2f")

	for _, probe := range unsafeProbes {
		t.Run(probe.name, func(t *testing.T) {
			body := securityReleaseGateServe(t, server, probe.target, probe.status)
			for _, want := range probe.want {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing %q:\n%s", probe.target, want, body)
				}
			}
			assertSecurityReleaseGateClean(t, probe.target, body, extraForbidden)
		})
	}

	for _, target := range []string{"/runs/audit?run=" + fullID, "/runs/audit?run=" + dryID} {
		export, body := getRunAuditExport(t, server, target, http.StatusOK)
		if !strings.Contains(export.Security.Summary, "security redactions 6") || !export.Security.CommandMetadataSanitized || export.Security.RawCommandTextPersisted || export.Security.RawEnvironmentPersisted {
			t.Fatalf("audit export exposed unsafe diagnostics for %s: %#v\n%s", target, export.Security, body)
		}
		assertSecurityReleaseGateClean(t, target, body, extraForbidden)
	}
}

func TestSecurityReleaseGateInspectionRoutesUseSharedGuardedHelpers(t *testing.T) {
	funcs := parseServeFunctions(t)
	for fn, requiredCall := range map[string]string{
		"handleRunsIndex":         "discoverRuns",
		"discoverRuns":            "loadRunInspection",
		"handleRunCompare":        "loadRunCompare",
		"loadRunCompareSide":      "loadRunInspection",
		"handleRunAudit":          "loadRunAuditExport",
		"loadRunAuditExport":      "loadRunInspection",
		"renderRunDetail":         "loadRunInspection",
		"runDetailFromInspection": "runValidationDetail",
	} {
		calls := serveFunctionCalls(t, funcs, fn)
		if !calls[requiredCall] {
			t.Fatalf("%s must route through shared guarded helper %s; calls=%v", fn, requiredCall, sortedCallNames(calls))
		}
	}

	for _, fn := range []string{
		"handleRunsIndex",
		"handleRunCompare",
		"loadRunCompareSide",
		"handleRunAudit",
		"loadRunAuditExport",
		"renderRunDetail",
		"runCompareSideFromInspection",
		"runHistoryLinkFromInspection",
		"runDetailFromInspection",
	} {
		calls := serveFunctionCalls(t, funcs, fn)
		for _, forbidden := range []string{"readRunFile", "loadDashboardManifest", "loadRunManifestResponse", "json.Unmarshal", "os.ReadFile"} {
			if calls[forbidden] {
				t.Fatalf("%s performs handler-local raw manifest/artifact work via %s; calls=%v", fn, forbidden, sortedCallNames(calls))
			}
		}
	}

	inspectionCalls := serveFunctionCalls(t, funcs, "loadRunInspection")
	if !inspectionCalls["readRunFile"] || !inspectionCalls["json.Unmarshal"] {
		t.Fatalf("loadRunInspection should remain the shared guarded manifest DTO construction path; calls=%v", sortedCallNames(inspectionCalls))
	}
}

func writeSecurityReleaseGateRun(t *testing.T, dir, runID string, dryRun bool, status string) {
	t.Helper()
	codexRan := !dryRun
	codexSkipped := dryRun
	codexStatus := "skipped"
	if codexRan {
		codexStatus = "success"
	}
	codexArtifacts := `"codex_summary": "codex/summary.md", "codex_events": "codex/events.jsonl", "codex_exit": "codex/exit.json",`
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", fmt.Sprintf(`{
		"run_id": %q,
		"status": %q,
		"started_at": "2026-04-28T21:00:00Z",
		"finished_at": "2026-04-28T21:00:03Z",
		"duration_ms": 3456,
		"dry_run": %t,
		"planner_provider": "openai",
		"task_proposal_mode": "security",
		"resolved_task_proposal_mode": "security",
		"selected_task_id": "TASK-0027",
		"planner": {"provider": "openai", "model": "gpt-release-gate"},
		"workspace": {"spec_path": ".jj/spec.json", "task_path": ".jj/tasks.json", "spec_written": %t, "task_written": %t},
		"artifacts": {
			"manifest": "manifest.json",
			"snapshot_spec_after": "snapshots/spec.after.json",
			"snapshot_tasks_after": "snapshots/tasks.after.json",
			"validation_summary": "validation/summary.md",
			"validation_results": "validation/results.json",
			"validation_stdout": "validation/001-validate.stdout.txt",
			"validation_stderr": "validation/001-validate.stderr.txt",
			%s
			"git_diff_summary": "git/diff-summary.txt"
		},
		"validation": {
			"ran": true,
			"status": "passed",
			"evidence_status": "recorded",
			"summary": "validation passed",
			"results_path": "validation/results.json",
			"summary_path": "validation/summary.md",
			"command_count": 1,
			"passed_count": 1,
			"failed_count": 0,
			"commands": [{
				"label": "validate",
				"name": "validate.sh",
				"provider": "local",
				"cwd": "[workspace]",
				"run_id": %q,
				"argv": ["./scripts/validate.sh", "--category", "security"],
				"exit_code": 0,
				"duration_ms": 1200,
				"status": "passed",
				"stdout_path": "validation/001-validate.stdout.txt",
				"stderr_path": "validation/001-validate.stderr.txt"
			}]
		},
		"codex": {
			"ran": %t,
			"skipped": %t,
			"status": %q,
			"model": "gpt-codex-release-gate",
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
				"root_labels": ["workspace", "run_artifacts", "current_run"],
				"guarded_roots": [
					{"label": "workspace", "path": "[workspace]"},
					{"label": "run_artifacts", "path": ".jj/runs"},
					{"label": "current_run", "path": "[run]"}
				],
				"denied_path_count": 2,
				"denied_path_categories": ["outside_workspace", "symlink_path"],
				"denied_path_category_counts": {"outside_workspace": 1, "symlink_path": 1},
				"failure_categories": ["path_denied"],
				"failure_category_counts": {"path_denied": 1},
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
	}`, runID, status, dryRun, !dryRun, !dryRun, codexArtifacts, runID, codexRan, codexSkipped, codexStatus))

	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/spec.after.json", `{"version":1,"title":"Security release gate","summary":"sanitized"}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/tasks.after.json", `{"version":1,"tasks":[{"id":"TASK-0027","title":"release gate","mode":"security","status":"in_progress"}]}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/summary.md", "validation passed\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/results.json", `{"status":"passed","passed_count":1,"failed_count":0}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stdout.txt", "category=security\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stderr.txt", "\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/git/diff-summary.txt", "no unsafe diff\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/summary.md", "codex summary sanitized\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/events.jsonl", `{"type":"done","status":"success"}`+"\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/codex/exit.json", fmt.Sprintf(`{"provider":"codex","name":"codex","model":"gpt-codex-release-gate","cwd":"[workspace]","run_id":%q,"argv":["codex","exec","--output-last-message","[run]/codex/summary.md"],"status":%q,"exit_code":0,"duration_ms":2200}`, runID, codexStatus))
}

func assertSecurityReleaseGateFilesClean(t *testing.T, dir string, runIDs []string, forbidden []string) {
	t.Helper()
	for _, runID := range runIDs {
		root := filepath.Join(dir, ".jj", "runs", runID)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			assertSecurityReleaseGateClean(t, path, string(data), append(forbidden, dir, filepath.ToSlash(dir), security.RedactionMarker))
			return nil
		})
		if err != nil {
			t.Fatalf("scan persisted artifacts for %s: %v", runID, err)
		}
	}
}

func assertSecurityReleaseGateParity(t *testing.T, dry, full runInspection) {
	t.Helper()
	drySecurity := dry.AuditSecurity
	fullSecurity := full.AuditSecurity
	if dry.Detail.DryRun != true || full.Detail.DryRun != false {
		t.Fatalf("fixtures did not preserve dry-run/non-dry-run split: dry=%v full=%v", dry.Detail.DryRun, full.Detail.DryRun)
	}
	if drySecurity.Summary != fullSecurity.Summary ||
		drySecurity.RedactionApplied != fullSecurity.RedactionApplied ||
		drySecurity.WorkspaceGuardrailsApplied != fullSecurity.WorkspaceGuardrailsApplied ||
		drySecurity.DeniedPathCount != fullSecurity.DeniedPathCount ||
		drySecurity.CommandMetadataSanitized != fullSecurity.CommandMetadataSanitized ||
		drySecurity.CommandArgvSanitized != fullSecurity.CommandArgvSanitized ||
		drySecurity.DryRunParityStatus != "equivalent" ||
		fullSecurity.DryRunParityStatus != "equivalent" {
		t.Fatalf("dry-run and non-dry-run security diagnostics diverged:\ndry=%#v\nfull=%#v", drySecurity, fullSecurity)
	}
}

func securityReleaseGateServe(t *testing.T, server *Server, target string, wantStatus int) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	server.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != wantStatus {
		t.Fatalf("%s status = %d, want %d body=%s", target, rec.Code, wantStatus, body)
	}
	return body
}

func assertSecurityReleaseGateClean(t *testing.T, label, body string, forbidden []string) {
	t.Helper()
	for _, leaked := range forbidden {
		if leaked != "" && strings.Contains(body, leaked) {
			t.Fatalf("%s leaked %q:\n%s", label, leaked, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "private key") {
		t.Fatalf("%s leaked private key wording:\n%s", label, body)
	}
	if strings.Contains(body, "raw command text should not be retained") {
		t.Fatalf("%s leaked raw command diagnostic payload:\n%s", label, body)
	}
}

func parseServeFunctions(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "serve.go", nil, 0)
	if err != nil {
		t.Fatalf("parse serve.go: %v", err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok {
			funcs[fn.Name.Name] = fn
		}
	}
	return funcs
}

func serveFunctionCalls(t *testing.T, funcs map[string]*ast.FuncDecl, name string) map[string]bool {
	t.Helper()
	fn := funcs[name]
	if fn == nil || fn.Body == nil {
		t.Fatalf("function %s not found in serve.go", name)
	}
	calls := map[string]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			calls[fun.Name] = true
		case *ast.SelectorExpr:
			calls[fun.Sel.Name] = true
			if ident, ok := fun.X.(*ast.Ident); ok {
				calls[ident.Name+"."+fun.Sel.Name] = true
			}
		}
		return true
	})
	return calls
}

func sortedCallNames(calls map[string]bool) []string {
	out := make([]string, 0, len(calls))
	for call := range calls {
		out = append(out, call)
	}
	sortStrings(out)
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		value := values[i]
		j := i - 1
		for j >= 0 && values[j] > value {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = value
	}
}

func TestSecurityReleaseGateAuditJSONDoesNotEmbedArtifactBodies(t *testing.T) {
	dir := newTestWorkspace(t)
	runID := "20260428-220000-body"
	writeSecurityReleaseGateRun(t, dir, runID, false, "complete")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/summary.md", "release gate artifact body visible only through guarded artifact route\n")
	server := newTestServer(t, dir, "")

	export, body := getRunAuditExport(t, server, "/runs/audit?run="+runID, http.StatusOK)
	if export.Evaluation.SummaryArtifact == nil || export.Evaluation.SummaryArtifact.URL == "" {
		t.Fatalf("audit export should include guarded artifact link metadata: %#v\n%s", export, body)
	}
	if strings.Contains(body, "release gate artifact body visible only through guarded artifact route") {
		t.Fatalf("audit export embedded raw artifact body:\n%s", body)
	}
	if _, err := json.Marshal(export); err != nil {
		t.Fatalf("audit export should remain JSON serializable: %v", err)
	}
}
