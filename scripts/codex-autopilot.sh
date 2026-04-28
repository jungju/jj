#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat >&2 <<'USAGE'
Usage: scripts/codex-autopilot.sh [plan.md]

Runs one jj turn at a time through the latest local source:
  OPENAI_API_KEY= go run ./cmd/jj run <plan> --run-id <turn-id>

Environment:
  MAX_TURNS=                    optional turn cap; unset means run forever
  TASK_PROPOSAL_MODE=auto       jj task proposal mode
  TASK_PROPOSAL_MODE_FILE=.jj/task-proposal-mode
                                optional per-turn mode override file
  BASE_RUN_ID=autopilot-...     optional base run id
  JJ_RUN_ARGS=""                extra flags passed to jj run

By default, passed validation does not stop the loop; the script keeps
starting fresh jj turns until a failure. Set MAX_TURNS only when you want
a bounded debugging run.

Examples:
  scripts/codex-autopilot.sh
  MAX_TURNS=5 TASK_PROPOSAL_MODE=security scripts/codex-autopilot.sh plan.md
  printf 'feature\n' > .jj/task-proposal-mode
  JJ_RUN_ARGS="--allow-no-git" scripts/codex-autopilot.sh plan.md
USAGE
}

die() {
	printf 'codex-autopilot: error: %s\n' "$*" >&2
	exit 2
}

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(CDPATH= cd -- "${script_dir}/.." && pwd -P)"
caller_cwd="$(pwd -P)"

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
	usage
	exit 0
fi
if [ "$#" -gt 1 ]; then
	usage
	die "expected zero or one plan path argument"
fi

max_turns="${MAX_TURNS:-}"
max_turn_label="unbounded"
if [ -n "$max_turns" ]; then
	case "$max_turns" in
		*[!0-9]*)
			die "MAX_TURNS must be a positive integer when set"
			;;
	esac
	if [ "$max_turns" -lt 1 ]; then
		die "MAX_TURNS must be at least 1 when set"
	fi
	max_turn_label="$max_turns"
fi

if [ "$#" -eq 0 ]; then
	plan="${repo_root}/plan.md"
else
	plan="$1"
	if [[ "$plan" != /* ]]; then
		plan="${caller_cwd}/${plan}"
	fi
fi
if [ ! -f "$plan" ]; then
	die "plan file not found: $plan"
fi

task_proposal_mode="${TASK_PROPOSAL_MODE:-auto}"
task_proposal_mode_file="${TASK_PROPOSAL_MODE_FILE:-${repo_root}/.jj/task-proposal-mode}"
base_run_id="${BASE_RUN_ID:-autopilot-$(date -u +%Y%m%d-%H%M%S)}"
log_dir="${AUTOPILOT_LOG_DIR:-${repo_root}/.jj/autopilot-logs}"

mkdir -p "$log_dir"
cd "$repo_root"

extra_args=()
if [ -n "${JJ_RUN_ARGS:-}" ]; then
	# shellcheck disable=SC2206
	extra_args=(${JJ_RUN_ARGS})
fi

turn_run_id() {
	local base="$1"
	local turn="$2"
	if [ "$turn" -le 1 ]; then
		printf '%s\n' "$base"
	else
		printf '%s-t%02d\n' "$base" "$turn"
	fi
}

valid_task_proposal_mode() {
	case "$1" in
		auto|balanced|feature|security|hardening|quality|bugfix|docs)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

resolve_task_proposal_mode() {
	local mode="$task_proposal_mode"
	if [ -f "$task_proposal_mode_file" ]; then
		mode="$(sed -n '1{s/^[[:space:]]*//;s/[[:space:]]*$//;p;q;}' "$task_proposal_mode_file")"
	fi
	if [ -z "$mode" ]; then
		mode="auto"
	fi
	if ! valid_task_proposal_mode "$mode"; then
		die "invalid task proposal mode: \"$mode\"
valid modes: auto, balanced, feature, security, hardening, quality, bugfix, docs"
	fi
	printf '%s\n' "$mode"
}

extract_run_dir() {
	local log_path="$1"
	local run_id="$2"
	local from_log
	from_log="$(awk -F= '/^run_dir=/{value=$2} END{print value}' "$log_path")"
	if [ -n "$from_log" ]; then
		printf '%s\n' "$from_log"
		return 0
	fi
	if [ -d "${repo_root}/.jj/runs/${run_id}" ]; then
		printf '%s\n' "${repo_root}/.jj/runs/${run_id}"
		return 0
	fi
	return 1
}

manifest_outcome() {
	local run_dir="$1"
	python3 - "$run_dir" <<'PY'
import json
import pathlib
import sys

run_dir = pathlib.Path(sys.argv[1])
manifest_path = run_dir / "manifest.json"

try:
    manifest = json.loads(manifest_path.read_text())
except Exception as exc:
    print(f"hard_failed\tmanifest unavailable: {exc}")
    raise SystemExit(0)

status = str(manifest.get("status") or "").strip().lower()
validation = manifest.get("validation") or {}
validation_status = str(validation.get("status") or "").strip().lower()
commit = manifest.get("commit") or {}
commit_status = str(commit.get("status") or "").strip().lower()

if validation_status == "passed":
    print("continue\tPASSED")
elif validation_status == "failed":
    print("hard_failed\tvalidation failed")
elif commit_status == "failed":
    print("hard_failed\tcommit failed")
elif status in {"failed", "cancelled"} or status.endswith("_failed"):
    reason = manifest.get("error_summary") or ""
    if not reason:
        errors = manifest.get("errors") or []
        if errors:
            reason = errors[0]
    print(f"hard_failed\t{reason or status}")
else:
    detail = validation_status or status or "continue"
    print(f"continue\t{detail}")
PY
}

turn=1
while :; do
	run_id="$(turn_run_id "$base_run_id" "$turn")"
	turn_log="${log_dir}/${run_id}.log"
	turn_task_proposal_mode="$(resolve_task_proposal_mode)"
	printf 'codex-autopilot: turn %d/%s run_id=%s mode=%s\n' "$turn" "$max_turn_label" "$run_id" "$turn_task_proposal_mode"

	cmd=(go run ./cmd/jj run "$plan" --run-id "$run_id" --task-proposal-mode "$turn_task_proposal_mode")
	if [ "${#extra_args[@]}" -gt 0 ]; then
		cmd+=("${extra_args[@]}")
	fi

	if ! env OPENAI_API_KEY= "${cmd[@]}" 2>&1 | tee "$turn_log"; then
		printf 'codex-autopilot: stopped: jj command failed on turn %d\n' "$turn" >&2
		printf 'codex-autopilot: log: %s\n' "$turn_log" >&2
		exit 1
	fi

	if ! run_dir="$(extract_run_dir "$turn_log" "$run_id")"; then
		printf 'codex-autopilot: stopped: run directory not found for %s\n' "$run_id" >&2
		printf 'codex-autopilot: log: %s\n' "$turn_log" >&2
		exit 1
	fi

	outcome="$(manifest_outcome "$run_dir")"
	outcome_kind="${outcome%%$'\t'*}"
	outcome_detail="${outcome#*$'\t'}"

	case "$outcome_kind" in
		hard_failed)
			printf 'codex-autopilot: stopped: %s\n' "$outcome_detail" >&2
			exit 1
			;;
	esac

	if [ -n "$max_turns" ] && [ "$turn" -eq "$max_turns" ]; then
		printf 'codex-autopilot: stopped: max turns reached\n'
		exit 0
	fi
	turn=$((turn + 1))
done
