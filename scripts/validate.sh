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

mkdir -p "$GOCACHE" "$GOMODCACHE"

cd "$repo_root"

printf 'Using GOCACHE=%s\n' "$GOCACHE"
printf 'Using GOMODCACHE=%s\n' "$GOMODCACHE"

printf '+ OPENAI_API_KEY= go test ./...\n'
OPENAI_API_KEY= go test ./...

printf '+ go vet ./...\n'
go vet ./...

printf '+ go build -o jj ./cmd/jj\n'
go build -o jj ./cmd/jj
