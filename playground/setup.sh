#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="$ROOT/playground/workspace"

if [[ ! -d "$WORKSPACE" ]]; then
  echo "workspace not found: $WORKSPACE" >&2
  exit 1
fi

if [[ -d "$WORKSPACE/.git" ]]; then
  echo "playground workspace is already a git repository"
  git -C "$WORKSPACE" status --short
  exit 0
fi

git -C "$WORKSPACE" init
git -C "$WORKSPACE" add .
git -C "$WORKSPACE" -c user.name="jj playground" -c user.email="jj-playground@example.invalid" commit -m "Initial playground workspace"

echo "initialized playground workspace at $WORKSPACE"
