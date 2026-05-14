package scripts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSmokeCodexFallbackMissingBinarySkipsByDefault(t *testing.T) {
	repoRoot := testRepoRoot(t)
	missing := filepath.Join(t.TempDir(), "missing-codex")
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "smoke-codex-fallback.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"JJ_CODEX_BINARY="+missing,
		"JJ_REQUIRE_CODEX_SMOKE=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke script should skip with exit 0, got %v:\n%s", err, out)
	}
	if !strings.Contains(string(out), "SKIP Codex fallback smoke") || strings.Contains(string(out), "PASS Codex fallback smoke") {
		t.Fatalf("expected clear SKIP output, got:\n%s", out)
	}
}

func TestSmokeCodexFallbackMissingBinaryFailsWhenRequired(t *testing.T) {
	repoRoot := testRepoRoot(t)
	missing := filepath.Join(t.TempDir(), "missing-codex")
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "smoke-codex-fallback.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"JJ_CODEX_BINARY="+missing,
		"JJ_REQUIRE_CODEX_SMOKE=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("smoke script should fail when required binary is missing:\n%s", out)
	}
	if !strings.Contains(string(out), "FAIL Codex fallback smoke") || !strings.Contains(string(out), "JJ_CODEX_BINARY") {
		t.Fatalf("expected actionable FAIL output, got:\n%s", out)
	}
}

func TestSmokeCodexFallbackSucceedsWithFakeCodexBinary(t *testing.T) {
	repoRoot := testRepoRoot(t)
	tmp := t.TempDir()
	fakeCodex := filepath.Join(tmp, "codex")
	if err := os.WriteFile(fakeCodex, []byte(fakeCodexScript()), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "smoke-codex-fallback.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"JJ_CODEX_BINARY="+fakeCodex,
		"JJ_REQUIRE_CODEX_SMOKE=1",
		"JJ_KEEP_CODEX_SMOKE=1",
		"OPENAI_API_KEY=sk-proj-parentenvsecret1234567890",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke script failed with fake codex: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "PASS Codex fallback smoke") {
		t.Fatalf("expected PASS output, got:\n%s", output)
	}
	keptTemp := outputValue(output, "kept_temp")
	if keptTemp != "" {
		t.Cleanup(func() { _ = os.RemoveAll(keptTemp) })
	}
	manifestPath := outputValue(output, "manifest")
	if manifestPath == "" {
		t.Fatalf("expected manifest path in output:\n%s", output)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest %s: %v\n%s", manifestPath, err, output)
	}
	var manifest struct {
		Status          string `json:"status"`
		DryRun          bool   `json:"dry_run"`
		PlannerProvider string `json:"planner_provider"`
		Config          struct {
			OpenAIKeySet bool `json:"openai_api_key_present"`
		} `json:"config"`
		Codex struct {
			Ran     bool `json:"ran"`
			Skipped bool `json:"skipped"`
		} `json:"codex"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v\n%s", err, data)
	}
	if manifest.Status != "dry_run_complete" || !manifest.DryRun || manifest.PlannerProvider != "codex" || manifest.Config.OpenAIKeySet || manifest.Codex.Ran || !manifest.Codex.Skipped {
		t.Fatalf("unexpected smoke manifest: %#v", manifest)
	}
	runDir := filepath.Dir(manifestPath)
	assertNonEmptyFile(t, filepath.Join(runDir, "snapshots", "spec.after.json"))
	assertNonEmptyFile(t, filepath.Join(runDir, "snapshots", "tasks.after.json"))
	assertNoPath(t, filepath.Join(runDir, "snapshots", "eval.json"))
	workspace := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))
	assertNoPath(t, filepath.Join(workspace, ".jj", "spec.json"))
	assertNoPath(t, filepath.Join(workspace, ".jj", "tasks.json"))
	assertNoPath(t, filepath.Join(workspace, ".jj", "eval.json"))
	assertNoPath(t, filepath.Join(workspace, "docs", "SPEC.md"))
	assertNoPath(t, filepath.Join(workspace, "docs", "TASK.md"))
	if strings.Contains(string(data), "sk-proj-parentenvsecret1234567890") || strings.Contains(output, "sk-proj-parentenvsecret1234567890") {
		t.Fatalf("smoke artifacts or output leaked parent OpenAI key:\n%s\n%s", data, output)
	}
}

func TestValidateScriptDoesNotInvokeCodexSmoke(t *testing.T) {
	repoRoot := testRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "validate.sh"))
	if err != nil {
		t.Fatalf("read validate.sh: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "smoke-codex-fallback") || strings.Contains(text, "JJ_REQUIRE_CODEX_SMOKE") {
		t.Fatalf("validate.sh should not invoke live Codex smoke verification:\n%s", text)
	}
}

func TestValidateScriptReleaseGateOutputIsSanitized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("validate.sh uses POSIX shell semantics")
	}
	repoRoot := testRepoRoot(t)
	tmp := t.TempDir()
	callLog := filepath.Join(tmp, "calls.log")
	hostileOutput := "OPENAI_API_KEY=sk-proj-releasevalidator1234567890 /tmp/unsafe-release-validator diff --git -----BEGIN PRIVATE KEY----- token=secret\n"
	fakeGo := `#!/bin/sh
printf 'go %s\n' "$*" >> "$JJ_VALIDATE_FAKE_CALL_LOG"
printf '%s' "$JJ_VALIDATE_HOSTILE_OUTPUT"
printf '%s' "$JJ_VALIDATE_HOSTILE_OUTPUT" >&2
if [ "$*" = "test ./internal/run" ]; then
	exit 7
fi
exit 0
`
	fakeGit := `#!/bin/sh
printf 'git %s\n' "$*" >> "$JJ_VALIDATE_FAKE_CALL_LOG"
printf '%s' "$JJ_VALIDATE_HOSTILE_OUTPUT"
printf '%s' "$JJ_VALIDATE_HOSTILE_OUTPUT" >&2
exit 0
`
	if err := os.WriteFile(filepath.Join(tmp, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "git"), []byte(fakeGit), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}

	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "validate.sh"))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+tmp+string(os.PathListSeparator)+os.Getenv("PATH"),
		"JJ_VALIDATE_FAKE_CALL_LOG="+callLog,
		"JJ_VALIDATE_HOSTILE_OUTPUT="+hostileOutput,
		"OPENAI_API_KEY=sk-proj-parentvalidator1234567890",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("validate.sh should fail when a release-gate step fails:\n%s", out)
	}
	output := string(out)
	for _, want := range []string{
		"release_validation status=failed total=6 failed=1 cache_configured=true",
		"step label=security_serve category=security_boundary passed=true exit_code=0",
		"step label=security_run category=security_boundary passed=false exit_code=7",
		"step label=diff_check category=diff passed=true exit_code=0",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("validate.sh output missing %q:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{
		"OPENAI_API_KEY",
		"sk-proj-releasevalidator1234567890",
		"sk-proj-parentvalidator1234567890",
		"/tmp/unsafe-release-validator",
		"diff --git",
		"-----BEGIN PRIVATE KEY-----",
		"token=secret",
		"go test",
		"go vet",
		"go build",
		"git diff",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("validate.sh leaked %q in output:\n%s", forbidden, output)
		}
	}

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read fake tool call log: %v", err)
	}
	calls := string(data)
	for _, want := range []string{
		"go test ./internal/serve\n",
		"go test ./internal/run\n",
		"go test ./...\n",
		"go vet ./...\n",
		"go build -o jj ./cmd/jj\n",
		"git diff --check\n",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("validate.sh did not run required step %q; calls:\n%s", want, calls)
		}
	}
}

func TestReleaseValidationWorkflowUsesSanitizedValidateGate(t *testing.T) {
	repoRoot := testRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "release-validation.yml"))
	if err != nil {
		t.Fatalf("read release-validation workflow: %v", err)
	}
	text := string(data)

	for _, want := range []string{
		"pull_request:",
		"push:",
		`- ".github/workflows/**"`,
		`- "**/*.go"`,
		`- "scripts/**"`,
		`- "docs/**"`,
		`- "README.md"`,
		`- ".jj/spec.json"`,
		`- ".jj/tasks.json"`,
		"persist-credentials: false",
		"cache: false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release validation workflow missing required privacy control %q", want)
		}
	}

	runCount := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "run:") {
			continue
		}
		runCount++
		if trimmed != "run: ./scripts/validate.sh" {
			t.Fatalf("release validation workflow must invoke only scripts/validate.sh, got sanitized run step %q", trimmed)
		}
	}
	if runCount != 1 {
		t.Fatalf("release validation workflow must have exactly one run step, got %d", runCount)
	}

	lowerText := strings.ToLower(text)
	for _, forbidden := range []string{
		"continue-on-error",
		"set -x",
		"bash -x",
		"sh -x",
		"actions_step_debug",
		"upload-artifact",
		"printenv",
		"env |",
		"env >",
		"cat ",
		"tee ",
		".jj/runs",
		"manifest.json",
		"diff-summary",
		"git diff",
		"secrets.",
	} {
		if strings.Contains(lowerText, forbidden) {
			t.Fatalf("release validation workflow uses forbidden privacy-sensitive construct %q", forbidden)
		}
	}
}

func TestCodexAutopilotContinuesAfterPassUntilMaxTurns(t *testing.T) {
	repoRoot := testRepoRoot(t)
	baseRunID := "autopilot-script-warning-" + strconv.FormatInt(timeNowUnixNano(), 10)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID)) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID+"-t02")) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID+"-t03")) })

	cmd := codexAutopilotCommand(t, repoRoot, baseRunID, "warning")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("autopilot should stop successfully on warnings: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "-t02") || !strings.Contains(output, "-t03") {
		t.Fatalf("autopilot should keep running after validation pass:\n%s", output)
	}
	if !strings.Contains(output, "max turns reached") {
		t.Fatalf("expected max turns stop output, got:\n%s", output)
	}
}

func TestCodexAutopilotDefaultsToUnboundedLoop(t *testing.T) {
	repoRoot := testRepoRoot(t)
	baseRunID := "autopilot-script-unbounded-" + strconv.FormatInt(timeNowUnixNano(), 10)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID)) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID+"-t02")) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID+"-t03")) })

	cmd := codexAutopilotCommandWithMaxTurns(t, repoRoot, baseRunID, "pass_then_fail", "")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("autopilot should eventually fail in fake unbounded run:\n%s", out)
	}
	output := string(out)
	if !strings.Contains(output, "turn 1/unbounded") || !strings.Contains(output, "-t02") || !strings.Contains(output, "-t03") {
		t.Fatalf("autopilot should default to unbounded turns:\n%s", output)
	}
	if !strings.Contains(output, "validation failed") {
		t.Fatalf("expected fake validation failure stop output, got:\n%s", output)
	}
}

func TestCodexAutopilotUsesEnvTaskProposalModeWhenNoModeFile(t *testing.T) {
	repoRoot := testRepoRoot(t)
	restoreAutopilotModeFile(t, repoRoot, nil)
	baseRunID := "autopilot-script-env-mode-" + strconv.FormatInt(timeNowUnixNano(), 10)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID)) })

	cmd := codexAutopilotCommandWithMaxTurns(t, repoRoot, baseRunID, "warning", "1")
	cmd.Env = append(cmd.Env, "TASK_PROPOSAL_MODE=feature")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("autopilot should accept env task proposal mode: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "mode=feature") || !strings.Contains(output, "task_proposal_mode=feature") {
		t.Fatalf("expected feature mode from env, got:\n%s", output)
	}
}

func TestCodexAutopilotModeFileOverridesEnvPerTurn(t *testing.T) {
	repoRoot := testRepoRoot(t)
	restoreAutopilotModeFile(t, repoRoot, stringPtr("quality\n"))
	baseRunID := "autopilot-script-file-mode-" + strconv.FormatInt(timeNowUnixNano(), 10)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID)) })

	cmd := codexAutopilotCommandWithMaxTurns(t, repoRoot, baseRunID, "warning", "1")
	cmd.Env = append(cmd.Env, "TASK_PROPOSAL_MODE=feature")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("autopilot should accept file task proposal mode: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "mode=quality") || !strings.Contains(output, "task_proposal_mode=quality") {
		t.Fatalf("expected quality mode from override file, got:\n%s", output)
	}
}

func TestCodexAutopilotInvalidModeFileFailsClearly(t *testing.T) {
	repoRoot := testRepoRoot(t)
	restoreAutopilotModeFile(t, repoRoot, stringPtr("fast\n"))
	baseRunID := "autopilot-script-invalid-mode-" + strconv.FormatInt(timeNowUnixNano(), 10)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID)) })

	cmd := codexAutopilotCommandWithMaxTurns(t, repoRoot, baseRunID, "warning", "1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("autopilot should reject invalid mode file:\n%s", out)
	}
	output := string(out)
	if !strings.Contains(output, `invalid task proposal mode: "fast"`) || !strings.Contains(output, "valid modes: auto, balanced, feature, security, hardening, quality, bugfix, docs") {
		t.Fatalf("expected clear invalid mode output, got:\n%s", output)
	}
}

func TestCodexAutopilotStopsOnPartialFailed(t *testing.T) {
	repoRoot := testRepoRoot(t)
	baseRunID := "autopilot-script-partial-failed-" + strconv.FormatInt(timeNowUnixNano(), 10)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID)) })
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repoRoot, ".jj", "runs", baseRunID+"-t02")) })

	cmd := codexAutopilotCommand(t, repoRoot, baseRunID, "partial_failed")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("autopilot should fail on partial_failed:\n%s", out)
	}
	output := string(out)
	if !strings.Contains(output, "validation failed") {
		t.Fatalf("expected validation failure stop output, got:\n%s", output)
	}
	if strings.Contains(output, "-t02") {
		t.Fatalf("autopilot should not start a second turn after partial_failed:\n%s", output)
	}
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

func codexAutopilotCommand(t *testing.T, repoRoot, baseRunID, fakeKind string) *exec.Cmd {
	return codexAutopilotCommandWithMaxTurns(t, repoRoot, baseRunID, fakeKind, "3")
}

func codexAutopilotCommandWithMaxTurns(t *testing.T, repoRoot, baseRunID, fakeKind, maxTurns string) *exec.Cmd {
	t.Helper()
	binDir := t.TempDir()
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte(fakeGoScript()), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "codex-autopilot.sh"), "docs/PLAN.md")
	cmd.Dir = repoRoot
	env := append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BASE_RUN_ID="+baseRunID,
		"AUTOPILOT_LOG_DIR="+t.TempDir(),
		"FAKE_GO_MANIFEST_KIND="+fakeKind,
	)
	if maxTurns != "" {
		env = append(env, "MAX_TURNS="+maxTurns)
	}
	cmd.Env = env
	return cmd
}

func restoreAutopilotModeFile(t *testing.T, repoRoot string, content *string) {
	t.Helper()
	path := filepath.Join(repoRoot, ".jj", "task-proposal-mode")
	original, err := os.ReadFile(path)
	existed := err == nil
	t.Cleanup(func() {
		if existed {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = os.WriteFile(path, original, 0o644)
			return
		}
		_ = os.Remove(path)
	})
	if content == nil {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir mode file dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(*content), 0o644); err != nil {
		t.Fatalf("write mode file: %v", err)
	}
}

func stringPtr(s string) *string {
	return &s
}

func fakeGoScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
run_id=""
task_proposal_mode=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--run-id)
			shift
			run_id="$1"
			;;
		--task-proposal-mode)
			shift
			task_proposal_mode="$1"
			;;
	esac
	shift || true
done
if [ -z "$run_id" ]; then
	echo "missing --run-id" >&2
	exit 1
fi
run_dir="$PWD/.jj/runs/$run_id"
mkdir -p "$run_dir/snapshots"
case "${FAKE_GO_MANIFEST_KIND:-warning}" in
	warning)
		cat > "$run_dir/manifest.json" <<'JSON'
{"status":"complete","validation":{"status":"passed"},"commit":{"status":"skipped"}}
JSON
		;;
	pass_then_fail)
		if [[ "$run_id" == *-t03 ]]; then
			cat > "$run_dir/manifest.json" <<'JSON'
{"status":"partial_failed","error_summary":"fake unbounded stop","validation":{"status":"failed"},"commit":{"status":"skipped"}}
JSON
		else
			cat > "$run_dir/manifest.json" <<'JSON'
{"status":"complete","validation":{"status":"passed"},"commit":{"status":"skipped"}}
JSON
		fi
		;;
	partial_failed)
		cat > "$run_dir/manifest.json" <<'JSON'
{"status":"partial_failed","error_summary":"partial failure","validation":{"status":"failed"},"commit":{"status":"skipped"}}
JSON
		;;
esac
printf 'run_id=%s\nrun_dir=%s\ntask_proposal_mode=%s\n' "$run_id" "$run_dir" "$task_proposal_mode"
`
}

func timeNowUnixNano() int64 {
	return time.Now().UnixNano()
}

func fakeCodexScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
out=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--output-last-message)
			shift
			out="$1"
			;;
	esac
	shift || true
done
stage="$(basename "$out" .last-message.txt)"
case "$stage" in
	merge)
		cat > "$out" <<'JSON'
{"spec":"# SPEC\n\nSmoke spec from fake Codex.","task":"# TASK\n\n1. Smoke task from fake Codex.","notes":["merged by fake Codex"]}
JSON
		;;
	*)
		cat > "$out" <<JSON
{"agent":"$stage","summary":"draft [jj-omitted]","spec_markdown":"# SPEC draft","task_markdown":"# TASK draft","risks":["risk"],"assumptions":["assumption"],"acceptance_criteria":["acceptance"],"test_plan":["go test ./..."]}
JSON
		;;
esac
printf '{"type":"done","token":"sk-proj-smokesecret1234567890"}\n'
`
}

func outputValue(output, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func assertNonEmptyFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatalf("%s is empty", path)
	}
}

func assertNoPath(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, stat err=%v", path, err)
	}
}
