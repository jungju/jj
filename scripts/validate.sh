#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(CDPATH= cd -- "${script_dir}/.." && pwd -P)"

if [ -z "${GOCACHE:-}" ]; then
	GOCACHE="${TMPDIR:-/tmp}/jj-go-build-cache"
	export GOCACHE
fi

if [ -z "${GOMODCACHE:-}" ]; then
	GOMODCACHE="${TMPDIR:-/tmp}/jj-go-mod-cache"
	export GOMODCACHE
fi

if ! mkdir -p "$GOCACHE" "$GOMODCACHE" >/dev/null 2>&1; then
	printf 'release_validation status=failed total=0 failed=1 cache_configured=false\n'
	printf 'step label=cache category=environment passed=false exit_code=1\n'
	exit 1
fi

cd "$repo_root" >/dev/null 2>&1 || {
	printf 'release_validation status=failed total=0 failed=1 cache_configured=true\n'
	printf 'step label=workspace category=environment passed=false exit_code=1\n'
	exit 1
}

total=0
failed=0

run_step() {
	local label="$1"
	local category="$2"
	shift 2
	total=$((total + 1))
	printf 'step label=%s category=%s running=true\n' "$label" "$category"
	set +e
	"$@" >/dev/null 2>&1
	local exit_code=$?
	set -e
	if [ "$exit_code" -eq 0 ]; then
		printf 'step label=%s category=%s passed=true exit_code=0\n' "$label" "$category"
		return
	fi
	failed=$((failed + 1))
	printf 'step label=%s category=%s passed=false exit_code=%d\n' "$label" "$category" "$exit_code"
}

printf 'release_validation status=running total=6 cache_configured=true\n'
run_step "security_serve" "security_boundary" env OPENAI_API_KEY= go test ./internal/serve
run_step "security_run" "security_boundary" env OPENAI_API_KEY= go test ./internal/run
run_step "test_all" "test" env OPENAI_API_KEY= go test ./...
run_step "vet_all" "vet" go vet ./...
run_step "build_cli" "build" go build -o jj ./cmd/jj
run_step "diff_check" "diff" git diff --check

if [ "$failed" -ne 0 ]; then
	printf 'release_validation status=failed total=%d failed=%d cache_configured=true\n' "$total" "$failed"
	exit 1
fi

printf 'release_validation status=passed total=%d failed=0 cache_configured=true\n' "$total"
