#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(CDPATH= cd -- "${script_dir}/.." && pwd -P)"

require_smoke="${JJ_REQUIRE_CODEX_SMOKE:-}"
keep_tmp="${JJ_KEEP_CODEX_SMOKE:-}"
codex_bin="${JJ_CODEX_BINARY:-${JJ_CODEX_BIN:-codex}}"

skip() {
	printf 'SKIP Codex fallback smoke: %s\n' "$*"
}

fail() {
	printf 'FAIL Codex fallback smoke: %s\n' "$*" >&2
	exit 1
}

resolve_codex() {
	local bin="$1"
	if [[ "$bin" == */* ]]; then
		if [[ -x "$bin" && ! -d "$bin" ]]; then
			printf '%s\n' "$bin"
			return 0
		fi
		return 1
	fi
	command -v "$bin"
}

codex_path=""
if ! codex_path="$(resolve_codex "$codex_bin" 2>/dev/null)"; then
	message="Codex CLI executable '$codex_bin' was not found or is not executable; set JJ_CODEX_BINARY=/path/to/codex to run this optional smoke check"
	if [[ "$require_smoke" == "1" ]]; then
		fail "$message"
	fi
	skip "$message"
	exit 0
fi

tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/jj-codex-smoke.XXXXXX")"
workspace="$tmp_root/workspace"
bin_dir="$tmp_root/bin"
jj_bin="$bin_dir/jj"
run_id="codex-fallback-smoke"
run_log="$tmp_root/jj-run.log"
manifest="$workspace/.jj/runs/$run_id/manifest.json"
spec_artifact="$workspace/.jj/runs/$run_id/snapshots/spec.after.json"
task_artifact="$workspace/.jj/runs/$run_id/snapshots/tasks.after.json"
project_status_before=""
project_status_after=""
project_git_available=0

cleanup() {
	if [[ "$keep_tmp" == "1" ]]; then
		printf 'kept_temp=%s\n' "$tmp_root"
	else
		rm -rf "$tmp_root"
	fi
}
trap cleanup EXIT

if git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	project_git_available=1
	project_status_before="$(git -C "$repo_root" status --short --untracked-files=all)"
fi

mkdir -p "$bin_dir" "$workspace"

if [[ -z "${GOCACHE:-}" ]]; then
	export GOCACHE="$tmp_root/go-build-cache"
fi
if [[ -z "${GOMODCACHE:-}" ]]; then
	export GOMODCACHE="$tmp_root/go-mod-cache"
fi
mkdir -p "$GOCACHE" "$GOMODCACHE"

if ! (
	cd "$repo_root"
	go build -mod=readonly -o "$jj_bin" ./cmd/jj
) ; then
	fail "build failed while preparing temporary jj binary; set JJ_KEEP_CODEX_SMOKE=1 to retain $tmp_root"
fi

git -C "$workspace" init -q || fail "git init failed in temporary workspace"
git -C "$workspace" config user.email "jj-smoke@example.invalid" || fail "git config failed in temporary workspace"
git -C "$workspace" config user.name "jj smoke" || fail "git config failed in temporary workspace"
cat > "$workspace/plan.md" <<'PLAN'
# Smoke Plan

Verify that jj can plan through the Codex CLI fallback in dry-run mode.
PLAN
git -C "$workspace" add plan.md || fail "git add failed in temporary workspace"
git -C "$workspace" commit -q -m "smoke plan" || fail "git commit failed in temporary workspace"

if ! (
	cd "$workspace"
	OPENAI_API_KEY= JJ_CODEX_BINARY="$codex_path" "$jj_bin" run plan.md \
		--cwd "$workspace" \
		--run-id "$run_id" \
		--planning-agents 1 \
		--dry-run \
		--codex-bin "$codex_path"
) >"$run_log" 2>&1; then
	printf 'jj dry-run output follows:\n' >&2
	sed -E \
		-e 's/sk-[A-Za-z0-9_*.-]{12,}/[jj-omitted]/g' \
		-e 's/Bearer [A-Za-z0-9._~+\/=-]{12,}/Bearer [jj-omitted]/g' \
		"$run_log" >&2 || true
	fail "jj dry-run failed; set JJ_KEEP_CODEX_SMOKE=1 to retain $tmp_root"
fi

checker="$tmp_root/check_smoke.go"
cat > "$checker" <<'GO'
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type manifest struct {
	Status          string `json:"status"`
	DryRun          bool   `json:"dry_run"`
	PlannerProvider string `json:"planner_provider"`
	Planner         struct {
		Provider string `json:"provider"`
	} `json:"planner"`
	Config struct {
		OpenAIKeySet bool   `json:"openai_api_key_present"`
		CodexBin     string `json:"codex_bin"`
	} `json:"config"`
	Workspace struct {
		SpecWritten bool `json:"spec_written"`
		TaskWritten bool `json:"task_written"`
	} `json:"workspace"`
	Codex struct {
		Ran     bool   `json:"ran"`
		Skipped bool   `json:"skipped"`
		Status  string `json:"status"`
	} `json:"codex"`
	Artifacts map[string]string `json:"artifacts"`
}

func main() {
	if len(os.Args) != 6 {
		failf("usage: check_smoke <manifest> <spec> <task> <workspace> <run-log>")
	}
	manifestPath, specPath, taskPath, workspace, runLog := os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5]
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		failf("read manifest: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		failf("decode manifest: %v", err)
	}
	require(m.Status == "dry_run_complete", "manifest status = %q, want dry_run_complete", m.Status)
	require(m.DryRun, "manifest dry_run is false")
	require(m.PlannerProvider == "codex", "planner_provider = %q, want codex", m.PlannerProvider)
	require(m.Planner.Provider == "codex", "planner.provider = %q, want codex", m.Planner.Provider)
	require(!m.Config.OpenAIKeySet, "manifest reports OpenAI API key present")
	require(!m.Workspace.SpecWritten && !m.Workspace.TaskWritten, "dry-run recorded workspace JSON state writes: %+v", m.Workspace)
	require(!m.Codex.Ran && m.Codex.Skipped && m.Codex.Status == "skipped", "implementation Codex was not skipped: %+v", m.Codex)
	require(m.Artifacts["snapshot_spec_after"] == "snapshots/spec.after.json", "spec snapshot path = %q", m.Artifacts["snapshot_spec_after"])
	require(m.Artifacts["snapshot_tasks_after"] == "snapshots/tasks.after.json", "task snapshot path = %q", m.Artifacts["snapshot_tasks_after"])
	requireNonEmpty(specPath, "run-local SPEC state")
	requireNonEmpty(taskPath, "run-local task state")
	for _, rel := range []string{"docs/SPEC.md", "docs/TASK.md", "docs/EVAL.md"} {
		if _, err := os.Stat(filepath.Join(workspace, rel)); err == nil {
			failf("dry-run unexpectedly wrote workspace %s", rel)
		} else if !os.IsNotExist(err) {
			failf("stat workspace %s: %v", rel, err)
		}
	}
	for _, rel := range []string{".jj/spec.json", ".jj/tasks.json"} {
		if _, err := os.Stat(filepath.Join(workspace, rel)); err == nil {
			failf("dry-run unexpectedly wrote workspace %s", rel)
		} else if !os.IsNotExist(err) {
			failf("stat workspace %s: %v", rel, err)
		}
	}
	runDir := filepath.Dir(filepath.Dir(specPath))
	if err := scanForSecretShapes(runDir); err != nil {
		failf("%v", err)
	}
	if err := scanFileForSecretShapes(runLog); err != nil {
		failf("%v", err)
	}
}

func require(ok bool, format string, args ...any) {
	if !ok {
		failf(format, args...)
	}
}

func requireNonEmpty(path, label string) {
	data, err := os.ReadFile(path)
	if err != nil {
		failf("read %s: %v", label, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		failf("%s is empty", label)
	}
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-(?:proj-|svcacct-)?[A-Za-z0-9_*.-]{12,}`),
	regexp.MustCompile(`(?i)Bearer [A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`OPENAI_API_KEY=[^\t\r\n "'` + "`" + `]+`),
}

func scanForSecretShapes(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		return scanFileForSecretShapes(path)
	})
}

func scanFileForSecretShapes(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	for _, pattern := range secretPatterns {
		if pattern.MatchString(text) {
			return fmt.Errorf("%s contains unredacted secret-shaped text matching %s", path, pattern.String())
		}
	}
	return nil
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "smoke verification failed: "+format+"\n", args...)
	os.Exit(1)
}
GO

if ! go run "$checker" "$manifest" "$spec_artifact" "$task_artifact" "$workspace" "$run_log"; then
	fail "manifest/artifact verification failed; set JJ_KEEP_CODEX_SMOKE=1 to retain $tmp_root"
fi

if [[ "$project_git_available" == "1" ]]; then
	project_status_after="$(git -C "$repo_root" status --short --untracked-files=all)"
	if [[ "$project_status_before" != "$project_status_after" ]]; then
		fail "project workspace git status changed during smoke run"
	fi
fi

printf 'PASS Codex fallback smoke\n'
printf 'workspace=%s\n' "$workspace"
printf 'manifest=%s\n' "$manifest"
