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
		{name: "dashboard", target: "/", status: http.StatusOK, want: []string{fullID, "security redactions 6", "Evaluation Findings"}},
		{name: "history", target: "/runs", status: http.StatusOK, want: []string{fullID, dryID, "compare"}},
		{name: "history filtered dry", target: "/runs?dry_run=true&q=release", status: http.StatusOK, want: []string{dryID, "Filtered run history."}},
		{name: "detail dry", target: "/runs/" + dryID, status: http.StatusOK, want: []string{dryID, "dry-run true", "security redactions 6", "dry-run parity equivalent", "git diff redactions 3"}},
		{name: "detail full", target: "/runs/" + fullID, status: http.StatusOK, want: []string{fullID, "dry-run false", "Codex command metadata", "Command Metadata"}},
		{name: "legacy run query", target: "/run?id=" + fullID, status: http.StatusOK, want: []string{fullID, "manifest available"}},
		{name: "compare", target: "/runs/compare?left=" + fullID + "&right=" + dryID, status: http.StatusOK, want: []string{"Left Run", "Right Run", fullID, dryID, "security redactions 6"}},
		{name: "audit full", target: "/runs/audit?run=" + fullID, status: http.StatusOK, want: []string{`"state":"available"`, `"command_metadata_sanitized":true`, `"dry_run_parity_status":"equivalent"`, `"git_diff_redaction_applied":true`}},
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
		"handleIndex":             "discoverRuns",
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
	indexCalls := serveFunctionCalls(t, funcs, "handleIndex")
	if !indexCalls["latestRunSummaryFromRuns"] {
		t.Fatalf("handleIndex must build latest-run data through the sanitized summary helper; calls=%v", sortedCallNames(indexCalls))
	}
	latestRunActionCalls := serveFunctionCalls(t, funcs, "dashboardLatestRunActions")
	for _, requiredCall := range []string{"dashboardRunAction", "dashboardRunActionLinks"} {
		if !latestRunActionCalls[requiredCall] {
			t.Fatalf("dashboardLatestRunActions must centralize Latest Run action construction through %s; calls=%v", requiredCall, sortedCallNames(latestRunActionCalls))
		}
	}
	if !indexCalls["recentRunsSummaryFromRuns"] {
		t.Fatalf("handleIndex must build recent-runs data through the sanitized summary helper; calls=%v", sortedCallNames(indexCalls))
	}
	if !indexCalls["dashboardRecentRuns"] {
		t.Fatalf("handleIndex must build dashboard Recent Runs presentation through the centralized helper; calls=%v", sortedCallNames(indexCalls))
	}
	recentRunsCalls := serveFunctionCalls(t, funcs, "dashboardRecentRuns")
	for _, requiredCall := range []string{"dashboardRecentRunItems", "dashboardRecentRunsFallback", "dashboardRecentRunHistoryAction"} {
		if !recentRunsCalls[requiredCall] {
			t.Fatalf("dashboardRecentRuns must centralize Recent Runs presentation through %s; calls=%v", requiredCall, sortedCallNames(recentRunsCalls))
		}
	}
	recentRunsItemCalls := serveFunctionCalls(t, funcs, "dashboardRecentRunItems")
	if !recentRunsItemCalls["dashboardRecentRunItemView"] {
		t.Fatalf("dashboardRecentRunItems must build Recent Runs items through the item view helper; calls=%v", sortedCallNames(recentRunsItemCalls))
	}
	recentRunItemViewCalls := serveFunctionCalls(t, funcs, "dashboardRecentRunItemView")
	for _, requiredCall := range []string{"dashboardRecentRunStateLine", "dashboardRecentRunProviderLine"} {
		if !recentRunItemViewCalls[requiredCall] {
			t.Fatalf("dashboardRecentRunItemView must centralize %s; calls=%v", requiredCall, sortedCallNames(recentRunItemViewCalls))
		}
	}
	recentRunHistoryActionCalls := serveFunctionCalls(t, funcs, "dashboardRecentRunHistoryAction")
	for _, requiredCall := range []string{"dashboardRunAction", "dashboardRunActionLinks"} {
		if !recentRunHistoryActionCalls[requiredCall] {
			t.Fatalf("dashboardRecentRunHistoryAction must guard Recent Runs history links through %s; calls=%v", requiredCall, sortedCallNames(recentRunHistoryActionCalls))
		}
	}
	if !indexCalls["evaluationFindingsSummaryFromRuns"] {
		t.Fatalf("handleIndex must build evaluation-findings data through the sanitized summary helper; calls=%v", sortedCallNames(indexCalls))
	}
	if !indexCalls["dashboardEvaluationFindings"] {
		t.Fatalf("handleIndex must build dashboard Evaluation Findings presentation through the centralized helper; calls=%v", sortedCallNames(indexCalls))
	}
	dashboardFindingsCalls := serveFunctionCalls(t, funcs, "dashboardEvaluationFindings")
	for _, requiredCall := range []string{"dashboardEvaluationFindingItems", "dashboardEvaluationFindingsSummaryLine", "dashboardEvaluationFindingsActions", "dashboardEvaluationFindingsFallback", "dashboardEvaluationFindingsHistoryAction"} {
		if !dashboardFindingsCalls[requiredCall] {
			t.Fatalf("dashboardEvaluationFindings must centralize Evaluation Findings presentation through %s; calls=%v", requiredCall, sortedCallNames(dashboardFindingsCalls))
		}
	}
	dashboardFindingItemsCalls := serveFunctionCalls(t, funcs, "dashboardEvaluationFindingItems")
	if !dashboardFindingItemsCalls["dashboardEvaluationFindingItemView"] {
		t.Fatalf("dashboardEvaluationFindingItems must build Evaluation Findings items through the item view helper; calls=%v", sortedCallNames(dashboardFindingItemsCalls))
	}
	dashboardFindingsActionCalls := serveFunctionCalls(t, funcs, "dashboardEvaluationFindingsActions")
	for _, requiredCall := range []string{"dashboardRunAction", "dashboardRunActionLinks"} {
		if !dashboardFindingsActionCalls[requiredCall] {
			t.Fatalf("dashboardEvaluationFindingsActions must guard Evaluation Findings action links through %s; calls=%v", requiredCall, sortedCallNames(dashboardFindingsActionCalls))
		}
	}
	dashboardFindingsHistoryActionCalls := serveFunctionCalls(t, funcs, "dashboardEvaluationFindingsHistoryAction")
	for _, requiredCall := range []string{"dashboardRunAction", "dashboardRunActionLinks"} {
		if !dashboardFindingsHistoryActionCalls[requiredCall] {
			t.Fatalf("dashboardEvaluationFindingsHistoryAction must guard Evaluation Findings history links through %s; calls=%v", requiredCall, sortedCallNames(dashboardFindingsHistoryActionCalls))
		}
	}
	if !indexCalls["dashboardTaskSummary"] {
		t.Fatalf("handleIndex must build dashboard TASK summary data through the sanitized summary helper; calls=%v", sortedCallNames(indexCalls))
	}
	taskSummaryCalls := serveFunctionCalls(t, funcs, "dashboardTaskSummary")
	for _, requiredCall := range []string{"dashboardTaskSummaryDecisionFor", "dashboardTaskSummaryViewForDecision"} {
		if !taskSummaryCalls[requiredCall] {
			t.Fatalf("dashboardTaskSummary must centralize %s; calls=%v", requiredCall, sortedCallNames(taskSummaryCalls))
		}
	}
	taskSummaryViewCalls := serveFunctionCalls(t, funcs, "dashboardTaskSummaryViewForDecision")
	for _, requiredCall := range []string{"dashboardTaskSummaryMessage", "dashboardTaskSummaryNextForDecision", "dashboardTaskSummaryEmptyMessage"} {
		if !taskSummaryViewCalls[requiredCall] {
			t.Fatalf("dashboardTaskSummaryViewForDecision must centralize %s; calls=%v", requiredCall, sortedCallNames(taskSummaryViewCalls))
		}
	}
	taskSummaryDecisionCalls := serveFunctionCalls(t, funcs, "dashboardTaskSummaryDecisionFor")
	if !taskSummaryDecisionCalls["dashboardTaskSummaryState"] {
		t.Fatalf("dashboardTaskSummaryDecisionFor must centralize TASK summary state decisions; calls=%v", sortedCallNames(taskSummaryDecisionCalls))
	}
	taskSummaryNextCalls := serveFunctionCalls(t, funcs, "dashboardTaskSummaryNextForDecision")
	if !taskSummaryNextCalls["dashboardTaskSummaryNext"] {
		t.Fatalf("dashboardTaskSummaryNextForDecision must route next-task sanitization through dashboardTaskSummaryNext; calls=%v", sortedCallNames(taskSummaryNextCalls))
	}
	evaluationFindingsCalls := serveFunctionCalls(t, funcs, "evaluationFindingsSummaryForRun")
	for _, requiredCall := range []string{"evaluationFindingsVisibleSummary", "evaluationFindingsUnavailableState", "evaluationFindingsStateForRun", "evaluationFindingItems"} {
		if !evaluationFindingsCalls[requiredCall] {
			t.Fatalf("evaluationFindingsSummaryForRun must centralize %s; calls=%v", requiredCall, sortedCallNames(evaluationFindingsCalls))
		}
	}
	visibleFindingsCalls := serveFunctionCalls(t, funcs, "evaluationFindingsVisibleSummary")
	for _, requiredCall := range []string{"evaluationFindingsGuardedLinks", "applyState"} {
		if !visibleFindingsCalls[requiredCall] {
			t.Fatalf("evaluationFindingsVisibleSummary must centralize %s; calls=%v", requiredCall, sortedCallNames(visibleFindingsCalls))
		}
	}
	sanitizeFindingsCalls := serveFunctionCalls(t, funcs, "sanitizeEvaluationFindingsSummary")
	if !sanitizeFindingsCalls["evaluationFindingsGuardedLinks"] {
		t.Fatalf("sanitizeEvaluationFindingsSummary must reuse guarded Evaluation Findings links; calls=%v", sortedCallNames(sanitizeFindingsCalls))
	}
	for _, fn := range []string{"evaluationFindingsUnavailableState", "evaluationFindingsStateForRun"} {
		calls := serveFunctionCalls(t, funcs, fn)
		if !calls["evaluationFindingsDecision"] && !calls["evaluationFindingsDecisionWithMetadata"] {
			t.Fatalf("%s must use centralized Evaluation Findings state decisions; calls=%v", fn, sortedCallNames(calls))
		}
	}
	decisionCalls := serveFunctionCalls(t, funcs, "evaluationFindingsDecision")
	if !decisionCalls["evaluationFindingsMessage"] {
		t.Fatalf("evaluationFindingsDecision must centralize Evaluation Findings messages; calls=%v", sortedCallNames(decisionCalls))
	}
	if !indexCalls["validationStatusSummaryFromRuns"] {
		t.Fatalf("handleIndex must build validation-status data through the sanitized summary helper; calls=%v", sortedCallNames(indexCalls))
	}
	validationStatusCalls := serveFunctionCalls(t, funcs, "validationStatusSummaryFromRuns")
	if !validationStatusCalls["validationStatusLatestSummary"] || !validationStatusCalls["validationStatusUnavailableSummary"] {
		t.Fatalf("validationStatusSummaryFromRuns must use centralized state summary helpers; calls=%v", sortedCallNames(validationStatusCalls))
	}
	for _, fn := range []string{"validationStatusItemFromRun", "validationStatusUnavailableItemFromRun"} {
		calls := serveFunctionCalls(t, funcs, fn)
		if !calls["validationStatusVisibleItem"] {
			t.Fatalf("%s must build dashboard validation-status items through the centralized visible-item helper; calls=%v", fn, sortedCallNames(calls))
		}
	}
	validationStatusItemCalls := serveFunctionCalls(t, funcs, "validationStatusVisibleItem")
	for _, requiredCall := range []string{"validationStatusSafeCountsLabel", "validationStatusTimestampLabel", "validationStatusActions"} {
		if !validationStatusItemCalls[requiredCall] {
			t.Fatalf("validationStatusVisibleItem must centralize %s; calls=%v", requiredCall, sortedCallNames(validationStatusItemCalls))
		}
	}
	if !indexCalls["activeRunsSummaryFromRuns"] {
		t.Fatalf("handleIndex must build active-run data through the sanitized summary helper; calls=%v", sortedCallNames(indexCalls))
	}
	activeRunsStateCalls := serveFunctionCalls(t, funcs, "activeRunsStateSummary")
	for _, requiredCall := range []string{"activeRunsStateLabel", "activeRunsStateMessage"} {
		if !activeRunsStateCalls[requiredCall] {
			t.Fatalf("activeRunsStateSummary must centralize active-run state data through %s; calls=%v", requiredCall, sortedCallNames(activeRunsStateCalls))
		}
	}
	activeRunsCalls := serveFunctionCalls(t, funcs, "activeRunsSummaryFromRuns")
	for _, requiredCall := range []string{"activeRunItemsFromRuns", "activeRunsSummaryFromItems"} {
		if !activeRunsCalls[requiredCall] {
			t.Fatalf("activeRunsSummaryFromRuns must centralize active-run construction through %s; calls=%v", requiredCall, sortedCallNames(activeRunsCalls))
		}
	}
	activeRunItemCalls := serveFunctionCalls(t, funcs, "activeRunItemFromRun")
	for _, requiredCall := range []string{"activeRunDisplayDataForRun", "activeRunVisibleItem"} {
		if !activeRunItemCalls[requiredCall] {
			t.Fatalf("activeRunItemFromRun must build dashboard active-run items through %s; calls=%v", requiredCall, sortedCallNames(activeRunItemCalls))
		}
	}
	activeRunVisibleCalls := serveFunctionCalls(t, funcs, "activeRunVisibleItem")
	for _, requiredCall := range []string{"activeRunSafeStatusToken", "activeRunProviderOrResultLabel", "activeRunEvaluationLabel", "activeRunTimestampLabel", "activeRunActions"} {
		if !activeRunVisibleCalls[requiredCall] {
			t.Fatalf("activeRunVisibleItem must centralize %s; calls=%v", requiredCall, sortedCallNames(activeRunVisibleCalls))
		}
	}
	activeRunStatusCalls := serveFunctionCalls(t, funcs, "activeRunSafeStatusToken")
	if !activeRunStatusCalls["activeRunStatusToken"] {
		t.Fatalf("activeRunSafeStatusToken must use the active-run status allowlist; calls=%v", sortedCallNames(activeRunStatusCalls))
	}
	for _, fn := range []string{"activeRunProviderOrResultLabel", "activeRunEvaluationLabel", "activeRunTimestampLabel"} {
		calls := serveFunctionCalls(t, funcs, fn)
		if !calls["activeRunSafeDisplayText"] {
			t.Fatalf("%s must use the active-run safe display helper; calls=%v", fn, sortedCallNames(calls))
		}
	}
	if !indexCalls["nextActionSummaryFromSummaries"] {
		t.Fatalf("handleIndex must build next-action data through the sanitized summary helper; calls=%v", sortedCallNames(indexCalls))
	}
	nextActionCalls := serveFunctionCalls(t, funcs, "nextActionSummaryFromSummaries")
	for _, requiredCall := range []string{"staticNextActionKindFromSummaries", "staticNextActionSummary"} {
		if !nextActionCalls[requiredCall] {
			t.Fatalf("nextActionSummaryFromSummaries must centralize static Next Action decisions through %s; calls=%v", requiredCall, sortedCallNames(nextActionCalls))
		}
	}
	staticNextActionKindCalls := serveFunctionCalls(t, funcs, "staticNextActionKindFromSummaries")
	if !staticNextActionKindCalls["taskQueueUnavailableNextActionKind"] {
		t.Fatalf("staticNextActionKindFromSummaries must centralize TASK-unavailable Next Action states; calls=%v", sortedCallNames(staticNextActionKindCalls))
	}
	staticNextActionCalls := serveFunctionCalls(t, funcs, "staticNextActionSummary")
	for _, requiredCall := range []string{"nextActionStaticSpecFor", "nextActionLinksForKinds"} {
		if !staticNextActionCalls[requiredCall] {
			t.Fatalf("staticNextActionSummary must build labels and guarded links through %s; calls=%v", requiredCall, sortedCallNames(staticNextActionCalls))
		}
	}
	nextActionLinkCalls := serveFunctionCalls(t, funcs, "nextActionLinksForKinds")
	for _, requiredCall := range []string{"nextActionLinkForKind", "nextActionLinks"} {
		if !nextActionLinkCalls[requiredCall] {
			t.Fatalf("nextActionLinksForKinds must route fixed Next Action links through %s; calls=%v", requiredCall, sortedCallNames(nextActionLinkCalls))
		}
	}
	detailCalls := serveFunctionCalls(t, funcs, "runDetailFromInspection")
	if !detailCalls["validationEvidenceFromRun"] {
		t.Fatalf("runDetailFromInspection must build validation evidence through the sanitized run DTO helper; calls=%v", sortedCallNames(detailCalls))
	}
	evidenceCalls := serveFunctionCalls(t, funcs, "validationEvidenceFromRun")
	if !evidenceCalls["validationEvidenceVisibleSummaryForRun"] {
		t.Fatalf("validationEvidenceFromRun must build visible summaries through the centralized presentation helper; calls=%v", sortedCallNames(evidenceCalls))
	}
	visibleEvidenceCalls := serveFunctionCalls(t, funcs, "validationEvidenceVisibleSummaryForRun")
	if !visibleEvidenceCalls["validationEvidenceVisibleSummary"] {
		t.Fatalf("validationEvidenceVisibleSummaryForRun must use the shared visible-summary constructor; calls=%v", sortedCallNames(visibleEvidenceCalls))
	}
	if !detailCalls["runDetailArtifactInventoryPresentation"] {
		t.Fatalf("runDetailFromInspection must build run artifacts through the centralized presentation helper; calls=%v", sortedCallNames(detailCalls))
	}
	presentationCalls := serveFunctionCalls(t, funcs, "runDetailArtifactInventoryPresentation")
	for _, requiredCall := range []string{"runArtifactInventoryFromRun", "runDetailArtifactInventoryDecision"} {
		if !presentationCalls[requiredCall] {
			t.Fatalf("runDetailArtifactInventoryPresentation must centralize run artifact construction through %s; calls=%v", requiredCall, sortedCallNames(presentationCalls))
		}
	}
	artifactDecisionCalls := serveFunctionCalls(t, funcs, "runDetailArtifactInventoryDecision")
	if !artifactDecisionCalls["runDetailArtifactInventoryFallbackNote"] {
		t.Fatalf("runDetailArtifactInventoryDecision must centralize fallback notes; calls=%v", sortedCallNames(artifactDecisionCalls))
	}
	artifactCalls := serveFunctionCalls(t, funcs, "runArtifactInventoryFromRun")
	if !artifactCalls["runArtifactInventoryItem"] {
		t.Fatalf("runArtifactInventoryFromRun must build run-detail artifact items through the shared item helper; calls=%v", sortedCallNames(artifactCalls))
	}
	artifactItemCalls := serveFunctionCalls(t, funcs, "runArtifactInventoryItem")
	if !artifactItemCalls["runArtifactInventoryActionURL"] {
		t.Fatalf("runArtifactInventoryItem must centralize guarded action construction; calls=%v", sortedCallNames(artifactItemCalls))
	}

	for _, fn := range []string{
		"handleIndex",
		"handleRunsIndex",
		"latestRunSummaryFromRuns",
		"recentRunsSummaryFromRuns",
		"dashboardRecentRuns",
		"dashboardRecentRunItems",
		"dashboardRecentRunItemView",
		"dashboardRecentRunsFallback",
		"dashboardRecentRunStateLine",
		"dashboardRecentRunProviderLine",
		"dashboardRecentRunHistoryAction",
		"recentRunItemFromRun",
		"evaluationFindingsSummaryFromRuns",
		"evaluationFindingsSummaryForRun",
		"evaluationFindingsVisibleSummary",
		"evaluationFindingsGuardedLinks",
		"evaluationFindingsUnavailableState",
		"evaluationFindingsStateForRun",
		"evaluationFindingsDecision",
		"evaluationFindingsDecisionWithMetadata",
		"evaluationFindingItems",
		"sanitizeEvaluationFindingsSummary",
		"dashboardEvaluationFindings",
		"dashboardEvaluationFindingItems",
		"dashboardEvaluationFindingItemView",
		"dashboardEvaluationFindingsStateLine",
		"dashboardEvaluationFindingsSummaryLine",
		"dashboardEvaluationFindingsShowAllClear",
		"dashboardEvaluationFindingsActions",
		"dashboardEvaluationFindingsFallback",
		"dashboardEvaluationFindingsHistoryAction",
		"activeRunsStateSummary",
		"activeRunsStateLabel",
		"activeRunsStateMessage",
		"activeRunsSummaryFromRuns",
		"activeRunItemsFromRuns",
		"activeRunsSummaryFromItems",
		"activeRunItemFromRun",
		"activeRunDisplayDataForRun",
		"activeRunVisibleItem",
		"activeRunSafeStatusToken",
		"activeRunProviderOrResultLabel",
		"activeRunEvaluationLabel",
		"activeRunTimestampLabel",
		"activeRunSafeDisplayText",
		"activeRunActions",
		"validationStatusSummaryFromRuns",
		"validationStatusItemFromRun",
		"validationStatusUnavailableItemFromRun",
		"validationStatusVisibleItem",
		"validationStatusSafeCountsLabel",
		"validationStatusTimestampLabel",
		"validationStatusActions",
		"validationStatusMetadataForRun",
		"dashboardLatestRunActions",
		"dashboardTaskSummary",
		"dashboardTaskSummaryViewForDecision",
		"dashboardTaskSummaryDecisionFor",
		"dashboardTaskSummaryState",
		"dashboardTaskSummaryMessage",
		"dashboardTaskSummaryNextForDecision",
		"dashboardTaskSummaryNext",
		"dashboardTaskSummaryEmptyMessage",
		"nextActionSummaryFromSummaries",
		"staticNextActionKindFromSummaries",
		"taskQueueUnavailableNextActionKind",
		"staticNextActionSummary",
		"nextActionStaticSpecFor",
		"nextActionLinksForKinds",
		"nextActionLinkForKind",
		"handleRunCompare",
		"loadRunCompareSide",
		"handleRunAudit",
		"loadRunAuditExport",
		"renderRunDetail",
		"runCompareSideFromInspection",
		"runHistoryLinkFromInspection",
		"runDetailFromInspection",
		"validationEvidenceFromRun",
		"validationEvidenceVisibleSummaryForRun",
		"validationEvidenceVisibleSummary",
		"runDetailArtifactInventoryPresentation",
		"runArtifactInventoryFromRun",
		"runArtifactInventoryItem",
		"runArtifactInventoryActionURL",
		"runDetailArtifactInventoryDecision",
		"runDetailArtifactInventoryFallbackNote",
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

func TestSecurityReleaseGateTamperedRunMetadataDTOsStaySanitized(t *testing.T) {
	dir := newTestWorkspace(t)
	secret := "tampered-run-metadata-secret-value"
	apiKey := "sk-proj-tamperedmetadata1234567890"
	privateKey := "-----BEGIN PRIVATE KEY-----\ntampered-private-key-body\n-----END PRIVATE KEY-----"
	rawCommand := "OPENAI_API_KEY=" + apiKey + " ./scripts/validate.sh --token " + secret
	rawEnv := "JJ_TAMPERED_SECRET=" + secret
	rawArtifactBody := "raw artifact body should not render"
	rawDiffBody := "raw diff body should not render"
	denialPayload := "attacker-controlled-denial-payload"
	t.Setenv("JJ_TAMPERED_SECRET", secret)

	validID := "20260428-221000-tamper-valid"
	tamperedID := "20260428-222000-tampered"
	writeSecurityReleaseGateRun(t, dir, validID, false, "complete")
	writeSecurityReleaseGateRun(t, dir, tamperedID, false, "complete")
	mutateSecurityReleaseGateManifest(t, dir, tamperedID, func(manifest map[string]any) {
		manifest["status"] = "complete " + denialPayload
		manifest["started_at"] = "../" + denialPayload
		manifest["finished_at"] = "2026-04-28T22:20:02Z%2f" + denialPayload
		manifest["planner_provider"] = denialPayload
		manifest["task_proposal_mode"] = "security%2f" + denialPayload
		manifest["resolved_task_proposal_mode"] = denialPayload
		manifest["selected_task_id"] = "TASK-0032/" + denialPayload
		manifest["errors"] = []any{rawCommand, rawEnv, privateKey, denialPayload}
		manifest["risks"] = []any{"../" + denialPayload, rawDiffBody}

		planner := manifestMap(t, manifest, "planner")
		planner["provider"] = denialPayload
		planner["model"] = "gpt-" + denialPayload

		artifacts := manifestMap(t, manifest, "artifacts")
		artifacts["validation_summary"] = "validation/%2fsummary.md"
		artifacts["validation_results"] = "../validation/results.json"
		artifacts["git_diff"] = "git/../diff.patch"
		artifacts["tampered"] = rawArtifactBody

		gitMeta := manifestMap(t, manifest, "git")
		gitMeta["diff_path"] = "../git/diff.patch"
		gitMeta["diff_stat_path"] = "git/%2fdiff.stat.txt"
		gitMeta["diff_summary_path"] = rawDiffBody
		gitMeta["diff_redaction_categories"] = []any{denialPayload, apiKey, "private_key"}
		gitMeta["diff_redaction_category_counts"] = map[string]any{denialPayload: float64(1), apiKey: float64(1)}
		gitMeta["diff_artifact_labels"] = []any{"git_diff", "../outside", rawDiffBody}

		validation := manifestMap(t, manifest, "validation")
		validation["status"] = "passed%2f" + denialPayload
		validation["evidence_status"] = denialPayload
		validation["reason"] = rawArtifactBody + " " + rawCommand
		validation["summary"] = privateKey + "\n" + rawArtifactBody + "\n" + denialPayload + "\n[omitted]"
		validation["results_path"] = "../validation/results.json"
		validation["summary_path"] = "validation/%2fsummary.md"
		validation["command_count"] = float64(-7)
		validation["passed_count"] = float64(-2)
		validation["failed_count"] = float64(-3)
		commands, ok := validation["commands"].([]any)
		if !ok || len(commands) == 0 {
			t.Fatalf("validation commands missing from fixture: %#v", validation["commands"])
		}
		command, ok := commands[0].(map[string]any)
		if !ok {
			t.Fatalf("validation command has unexpected shape: %#v", commands[0])
		}
		command["label"] = denialPayload
		command["name"] = rawCommand
		command["command"] = rawCommand
		command["cwd"] = filepath.Join(dir, "outside-"+secret)
		command["argv"] = []any{"OPENAI_API_KEY=" + apiKey, "--token", secret, "./scripts/validate.sh", "../escape", "%2fencoded", "safe-arg"}
		command["stdout_path"] = "../validation/stdout.txt"
		command["stderr_path"] = "validation/%2fstderr.txt"
		command["error"] = rawEnv

		codex := manifestMap(t, manifest, "codex")
		codex["model"] = denialPayload
		codex["error"] = privateKey
		codex["events_path"] = "codex/%2fevents.jsonl"
		codex["summary_path"] = "../codex/summary.md"
		codex["exit_path"] = "codex/%2e%2e/exit.json"

		securityMeta := manifestMap(t, manifest, "security")
		diagnostics := manifestMap(t, securityMeta, "diagnostics")
		diagnostics["root_labels"] = []any{"workspace", denialPayload, apiKey}
		diagnostics["denied_path_categories"] = []any{denialPayload, rawEnv}
		diagnostics["denied_path_category_counts"] = map[string]any{denialPayload: float64(2), rawEnv: float64(1)}
		diagnostics["failure_categories"] = []any{privateKey, rawArtifactBody}
		diagnostics["failure_category_counts"] = map[string]any{privateKey: float64(1), rawArtifactBody: float64(1)}
		diagnostics["command_cwd_label"] = filepath.Join(dir, "outside-"+secret)
		diagnostics["command_sanitization_status"] = rawCommand
		diagnostics["dry_run_parity_status"] = denialPayload
		diagnostics["git_diff_redaction_categories"] = []any{rawDiffBody, denialPayload}
		diagnostics["git_diff_redaction_category_counts"] = map[string]any{rawDiffBody: float64(1), denialPayload: float64(1)}
		diagnostics["git_diff_artifact_labels"] = []any{"git_diff", "../outside", rawDiffBody}
	})
	writeFile(t, dir, ".jj/runs/"+tamperedID+"/codex/exit.json", fmt.Sprintf(`{"provider":"codex","name":"%s","model":"%s","cwd":"%s","run_id":%q,"argv":["codex","--api-key=%s","exec","%s"],"status":"success","exit_code":0,"duration_ms":2200}`, denialPayload, denialPayload, filepath.Join(dir, "outside-"+secret), tamperedID, apiKey, rawDiffBody))

	server := newTestServer(t, dir, "")
	forbidden := []string{
		secret,
		apiKey,
		"OPENAI_API_KEY",
		"JJ_TAMPERED_SECRET",
		"tampered-private-key-body",
		"-----BEGIN",
		"-----END",
		"private key",
		rawCommand,
		rawEnv,
		rawArtifactBody,
		rawDiffBody,
		denialPayload,
		"../",
		"%2f",
		"%2e",
		"[omitted]",
		security.RedactionMarker,
		dir,
		filepath.ToSlash(dir),
	}
	probes := []struct {
		name   string
		target string
		want   []string
	}{
		{name: "detail", target: "/runs/" + tamperedID, want: []string{tamperedID, "manifest available", "unsafe value removed", "sensitive argument removed", "guarded artifact"}},
		{name: "history", target: "/runs", want: []string{tamperedID, "unsafe value removed"}},
		{name: "compare", target: "/runs/compare?left=" + validID + "&right=" + tamperedID, want: []string{validID, tamperedID, "Right Run", "unsafe value removed"}},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			body := securityReleaseGateServe(t, server, probe.target, http.StatusOK)
			for _, want := range probe.want {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing %q:\n%s", probe.target, want, body)
				}
			}
			assertSecurityReleaseGateClean(t, probe.target, body, forbidden)
		})
	}

	export, body := getRunAuditExport(t, server, "/runs/audit?run="+tamperedID, http.StatusOK)
	if export.State != "available" || export.ManifestState != "manifest available" {
		t.Fatalf("tampered audit export should remain structurally available with sanitized DTO fields: %#v\n%s", export, body)
	}
	if export.Evaluation.CommandCount != 1 || export.Evaluation.PassedCount != 0 || export.Evaluation.FailedCount != 0 {
		t.Fatalf("tampered validation counts were not clamped through DTO: %#v\n%s", export.Evaluation, body)
	}
	assertSecurityReleaseGateClean(t, "/runs/audit?run="+tamperedID, body, forbidden)
}

func mutateSecurityReleaseGateManifest(t *testing.T, dir, runID string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, ".jj", "runs", runID, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest for mutation: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest for mutation: %v", err)
	}
	mutate(manifest)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode mutated manifest: %v", err)
	}
	writeFile(t, dir, ".jj/runs/"+runID+"/manifest.json", string(encoded)+"\n")
}

func manifestMap(t *testing.T, manifest map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := manifest[key].(map[string]any)
	if !ok {
		t.Fatalf("manifest field %q has unexpected shape: %#v", key, manifest[key])
	}
	return value
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
			"git_diff": "git/diff.patch",
			"git_diff_stat": "git/diff.stat.txt",
			"git_status": "git/status.txt",
			"git_status_after": "git/status.after.txt",
			"git_diff_summary": "git/diff-summary.txt"
		},
		"git": {
			"available": true,
			"is_repo": true,
			"dirty_after": true,
			"dirty": true,
			"status_path": "git/status.txt",
			"status_after_path": "git/status.after.txt",
			"diff_path": "git/diff.patch",
			"diff_stat_path": "git/diff.stat.txt",
			"diff_summary_path": "git/diff-summary.txt",
			"diff_redaction_applied": true,
			"diff_redaction_count": 3,
			"diff_redaction_categories": ["absolute_path", "openai_key", "private_key"],
			"diff_redaction_category_counts": {"absolute_path": 1, "openai_key": 1, "private_key": 1},
			"diff_artifact_labels": ["git_diff", "git_diff_stat", "git_diff_summary", "git_status", "git_status_after"]
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
				"dry_run_parity_status": "equivalent",
				"git_diff_artifacts_available": true,
				"git_diff_redaction_applied": true,
				"git_diff_redaction_count": 3,
				"git_diff_redaction_categories": ["absolute_path", "openai_key", "private_key"],
				"git_diff_redaction_category_counts": {"absolute_path": 1, "openai_key": 1, "private_key": 1},
				"git_diff_artifact_labels": ["git_diff", "git_diff_stat", "git_diff_summary", "git_status", "git_status_after"]
			}
		}
	}`, runID, status, dryRun, !dryRun, !dryRun, codexArtifacts, runID, codexRan, codexSkipped, codexStatus))

	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/spec.after.json", `{"version":1,"title":"Security release gate","summary":"sanitized"}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/snapshots/tasks.after.json", `{"version":1,"tasks":[{"id":"TASK-0027","title":"release gate","mode":"security","status":"in_progress"}]}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/summary.md", "validation passed\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/results.json", `{"status":"passed","passed_count":1,"failed_count":0}`)
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stdout.txt", "category=security\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/validation/001-validate.stderr.txt", "\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/git/diff.patch", "diff redacted before persistence\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/git/diff.stat.txt", "diff stat redacted\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/git/status.txt", "M safe.txt\n")
	writeFile(t, dir, ".jj/runs/"+runID+"/git/status.after.txt", "M safe.txt\n")
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
		fullSecurity.DryRunParityStatus != "equivalent" ||
		drySecurity.GitDiffArtifactsAvailable != fullSecurity.GitDiffArtifactsAvailable ||
		drySecurity.GitDiffRedactionApplied != fullSecurity.GitDiffRedactionApplied ||
		drySecurity.GitDiffRedactionCount != fullSecurity.GitDiffRedactionCount {
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

func TestSecurityReleaseGateDuplicateQueryInputsDeniedWithoutReflection(t *testing.T) {
	dir := newTestWorkspace(t)
	server := newTestServer(t, dir, "")
	validID := "20260425-120000-bbbbbb"
	secret := "sk-proj-duplicatequery1234567890"
	attackerPath := "validation/../" + secret + ".md"

	probes := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "legacy run detail duplicate id",
			target: "/run?id=" + validID + "&id=" + secret,
			want:   "run id is not allowed",
		},
		{
			name:   "web run progress duplicate id",
			target: "/run/progress?id=" + validID + "&id=" + secret,
			want:   "run id is not allowed",
		},
		{
			name:   "web run status duplicate id",
			target: "/run/status?id=" + validID + "&id=" + secret,
			want:   "run id is not allowed",
		},
		{
			name:   "doc duplicate path",
			target: "/doc?path=README.md&path=" + url.QueryEscape(attackerPath),
			want:   "path is not allowed",
		},
		{
			name:   "artifact duplicate run",
			target: "/artifact?run=" + validID + "&run=" + secret + "&path=validation/summary.md",
			want:   "run id is not allowed",
		},
		{
			name:   "artifact duplicate path",
			target: "/artifact?run=" + validID + "&path=validation/summary.md&path=" + url.QueryEscape(attackerPath),
			want:   "artifact path is not allowed",
		},
	}

	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			body := securityReleaseGateServe(t, server, probe.target, http.StatusForbidden)
			if !strings.Contains(body, probe.want) {
				t.Fatalf("%s missing %q:\n%s", probe.target, probe.want, body)
			}
			assertSecurityReleaseGateClean(t, probe.target, body, []string{
				secret,
				attackerPath,
				url.QueryEscape(attackerPath),
				dir,
				filepath.ToSlash(dir),
				security.RedactionMarker,
			})
		})
	}
}
