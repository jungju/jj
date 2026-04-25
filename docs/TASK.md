# TASK
## Objective
Implement the next smallest useful change for the Go-based `jj` CLI: convert the latest PARTIAL evaluation gaps into repeatable verification evidence while preserving the existing document-first workflow, fail-closed artifact serving, redaction behavior, and no-default-commit git policy.

## Constraints
- Follow the repository's existing package layout and style.
- Keep command wiring thin; place orchestration and business logic in internal packages.
- Prefer extending existing tests and helpers over adding broad new product surfaces.
- Do not require live OpenAI or live Codex calls in automated tests.
- Use injected planners, fake Codex binaries, fake runners, and temporary workspaces.
- Never write workspace docs or code in dry-run mode.
- Resolve relative plan paths from the invocation directory, not from `--cwd`.
- Do not revert, overwrite unrelated user changes, clean the git workspace, stage files, or create commits.
- Record git evidence without trying to separate or undo pre-existing dirty state.
- Apply secret redaction before persisting or rendering logs, manifests, raw planner output, Codex output, errors, Markdown, verification artifacts, and HTML.
- Serve only local public workspace docs and manifest-listed run artifact paths.
- Block traversal before path cleanup or normalization.
- Keep artifact serving fail-closed for malformed, incomplete, missing, legacy, or untrusted manifests.
- Keep all generated manifest artifact paths relative to the run directory.
- Do not loosen artifact allowlisting to make legacy manifests work.
- Do not add a commit feature in this iteration.
- Add a user-visible `jj verify` command only if it fits cleanly; otherwise implement verification as focused deterministic tests and optional run-local artifacts.

## Implementation Steps
1. Inspect existing code and tests in `cmd/jj`, `internal/run`, `internal/serve`, manifest structs, config handling, redaction helpers, git helpers, planner selection, Codex integration, and evaluation code.
2. Identify which latest PARTIAL gaps are already covered and which need stronger reproducible evidence.
3. Reuse existing fake planner, fake Codex, temp workspace, temp git repo, manifest, redaction, and HTTP test helpers where available.
4. Add or tighten deterministic Codex fallback coverage: unset `OPENAI_API_KEY`, configure a temporary fake Codex executable or runner, run a dry-run or planning-only flow, and assert `manifest.json` records planner provider `codex`.
5. Ensure the fake Codex executable emits output that matches the fallback parser contract closely enough to prove provider selection and parsing without invoking the real Codex binary.
6. Add or tighten no-default-commit regression coverage: run a non-dry-run in a fresh temporary git repo with fake planner and fake Codex, record HEAD before and after, assert HEAD is unchanged, assert dirty state is preserved when present, and assert newly generated manifest commit metadata is absent, skipped, or `ran:false`.
7. Confirm no implementation path calls `git add`, `git commit`, `git reset`, `git checkout`, `git stash`, `git clean`, or another history-mutating git command.
8. Add or extend serve tests using `httptest.Server` or the actual registered handler stack with normal `net/http` request parsing.
9. Cover traversal and unsafe artifact requests including `docs/../manifest.json`, encoded slash traversal, encoded dot-dot traversal, `.secret/../manifest.json`, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL byte encodings, hidden segments, and hidden run artifacts.
10. Assert unsafe artifact requests return non-2xx responses and do not leak absolute filesystem paths or raw fake secrets.
11. Assert malformed, incomplete, unreadable, and legacy manifests render as degraded or unavailable dashboard rows without trusted artifact links.
12. Assert a legacy manifest with historical `commit.status=success` is treated as historical/degraded and does not imply current default auto-commit behavior.
13. Assert valid manifest-listed artifacts and explicit public workspace docs still serve successfully through the same HTTP stack.
14. Add a concise compatibility note in the dashboard invalid-run row or docs explaining that legacy runs without a trusted top-level `artifacts` map cannot expose artifact links for safety.
15. Add or extend a redaction regression that injects fake secrets through plan text, planner output, Codex summary/events, config or `.jjrc`-like values, manifest-visible strings, docs, served Markdown, and error paths where practical.
16. Scan only deterministic text run artifacts and representative HTTP response bodies for raw fake secret strings to avoid binary or build-output noise.
17. If any raw fake secret appears, route that write or render path through the central redaction helper immediately before persistence or HTML output.
18. If verification artifacts are produced, write redacted run-relative files such as `verification/summary.md`, `verification/results.json`, and optional `verification/http-probes.jsonl`, then list them in the manifest artifact map.
19. Keep edits scoped to `internal/run`, `internal/serve`, docs, and existing tests unless the codebase clearly has a shared helper package that should own the behavior.
20. Run focused tests first, then full test, vet, and build verification.

## Files and Packages to Inspect
- `cmd/jj` for CLI entrypoint and command wiring.
- `internal/run/run.go` for run orchestration and phase order.
- `internal/run/git.go` for git helpers and mutation risks.
- `internal/run/run_test.go` for run pipeline, manifest, dry-run, non-dry-run, no-commit, fallback, dirty workspace, no-git, and redaction tests.
- `internal/serve/serve.go` for dashboard and artifact route handling.
- `internal/serve/web_run.go` for dashboard run state and manifest loading, if present.
- `internal/serve/serve_test.go` for HTTP dashboard, traversal, redaction, and malformed manifest tests.
- Manifest structs and serialization helpers.
- Existing internal packages for config, workspace, artifacts, planner, Codex integration, evaluation, redaction, and server behavior.
- `go.mod` and `go.sum` for available dependencies and module structure.
- `README.md`, `plan.md`, `docs/`, and any existing `.jjrc` examples.

If no equivalent packages exist, use this target structure only as a guide:
- `internal/config`
- `internal/workspace`
- `internal/gitutil`
- `internal/artifact`
- `internal/planner`
- `internal/codex`
- `internal/eval`
- `internal/redact`
- `internal/run`
- `internal/server`

## Required Changes
- Keep default calls to `git add`, `git commit`, or commit helpers removed or disabled during `jj run`.
- Ensure default non-dry-run does not change `git rev-parse HEAD`.
- Ensure pre-existing dirty files are not staged, committed, reverted, or otherwise modified by commit logic.
- Ensure non-dry-run still writes workspace SPEC/TASK before Codex implementation.
- Ensure non-dry-run still captures Codex events, summary, exit status, errors, git status before/after, diff patch, diff stat, EVAL, and manifest evaluation result.
- Ensure workspace and run-local `docs/EVAL.md` are generated for non-dry-run.
- Ensure manifest generation includes accurate dirty metadata, artifact paths, planner provider/model, Codex result, evaluation result, errors, redaction marker, and no successful default commit metadata.
- Keep CLI flags for `jj run`: `--cwd`, `--run-id`, `--dry-run`, `--allow-no-git`, `--planner-agents` or compatible `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, and `--task-doc`.
- Keep CLI flags for `jj serve`: `--cwd` and `--addr`.
- Add or refine run-summary loading so malformed manifests cannot crash the dashboard.
- Render malformed, unreadable, incomplete, or legacy manifests as sanitized invalid or unavailable run entries.
- Add concise dashboard or docs guidance that legacy manifests without a trusted artifact map cannot expose artifact links by design.
- Generate artifact links only from manifest-listed artifacts or explicit public workspace document allowlist entries.
- Fail closed for artifact requests against malformed, missing, or untrusted manifests.
- Preserve safe Markdown rendering and secret redaction for served content.
- Add or preserve raw decoded artifact path validation before normalization.
- Reject traversal, encoded traversal, hidden segments, hidden artifacts, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL bytes, empty paths, and paths outside allowed roots.
- Still serve valid manifest-listed artifacts and explicit public workspace docs.
- Add deterministic Codex fallback coverage with `OPENAI_API_KEY` unset and no injected planner.
- Add redaction coverage that scans persisted text run artifacts and representative HTTP responses.
- Add or update no-default-commit coverage that compares HEAD before and after non-dry-run and checks manifest commit metadata.

## Testing Requirements
- Unit test input validation for missing, empty, directory, non-Markdown, and valid Markdown plans.
- Unit test `--cwd` with relative plan paths to prove the plan path is resolved from invocation directory.
- Unit test config precedence: CLI flags, env vars, `.jjrc`, defaults.
- Unit test secret redaction for OpenAI keys, Bearer [redacted], authorization headers, and secret-like `.jjrc` values.
- Unit test planner provider selection: injected first, OpenAI when key exists, Codex fallback when no key exists.
- Deterministic Codex fallback test with `OPENAI_API_KEY` unset and a fake Codex binary or runner; assert `planner.provider=codex` in `manifest.json`.
- Unit test manifest serialization, status fields, and relative artifact paths.
- Unit test run id collision handling.
- Unit test malformed planner output so it fails without writing successful empty SPEC/TASK.
- Integration test dry-run with fake planner: run artifacts are created, workspace docs/code are untouched, and implementation Codex is not called.
- Integration test non-dry-run in a temporary git repo with fake planner and fake Codex runner: workspace docs, Codex artifacts, git status/diff, EVAL, and manifest are created.
- Regression test non-dry-run with fake planner and fake Codex does not create a git commit and leaves HEAD unchanged.
- Regression test dirty workspace before run remains dirty, is recorded as `dirty_before=true`, and is not committed.
- Manifest test asserts default non-dry-run has no successful commit metadata and still has git availability, dirty flags, status paths, diff path, and diff stat path.
- Integration test no-git workspace: fail without `--allow-no-git`, succeed with it and record git unavailable.
- Failure-path tests for planner failure, Codex failure, and evaluation failure: manifest status and partial artifacts remain.
- HTTP handler tests for dashboard with no docs, no runs, successful run, failed run, missing evaluation, malformed manifest, incomplete manifest, legacy commit-success manifest, missing artifact references, and mixed valid/invalid runs.
- HTTP security tests through the real handler stack rejecting raw and encoded traversal, hidden paths, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL bytes, hidden segments, and hidden run artifacts.
- HTTP handler test proving a valid manifest-listed artifact still serves successfully.
- HTTP handler test proving a malformed-manifest run cannot serve unlisted artifacts.
- Markdown rendering tests that raw script content is escaped or removed.
- Security regression test injecting fake secrets through env, `.jjrc`, plan, planner output, Codex output, errors, manifests, docs, and served pages; assert selected artifacts and HTTP responses do not contain raw secrets.
- Run focused tests: `GOCACHE=/tmp/jj-go-build-cache go test ./internal/serve ./internal/run`.
- Run final verification: `GOCACHE=/tmp/jj-go-build-cache go test ./...`, `GOCACHE=/tmp/jj-go-build-cache go vet ./...`, and `GOCACHE=/tmp/jj-go-build-cache GOMODCACHE=/tmp/jj-go-mod-cache go build -o jj ./cmd/jj`.

## Manual Verification
- Run `jj run plan.md --dry-run --run-id <id>` with `OPENAI_API_KEY` unset and a known fake or local Codex binary, then inspect `.jj/runs/<id>/manifest.json` for planner provider `codex`.
- Start `jj serve --cwd . --addr 127.0.0.1:<port>` and confirm `/` shows the dashboard rather than a raw file listing.
- Probe representative traversal URLs through the running server and confirm non-2xx responses without raw paths or secrets.
- Open a valid manifest-listed artifact and a public workspace doc from the dashboard and confirm they render.
- Inspect a malformed or legacy run row and confirm it is marked unavailable or degraded with no trusted artifact links.
- Run a non-dry-run with fake Codex in a temporary git repo and confirm HEAD is unchanged and no commit metadata reports success.
- Scan the generated run directory and representative HTTP responses for the fake secret strings used in tests.

## Done Criteria
- Codex fallback with `OPENAI_API_KEY` unset is covered by deterministic fake-Codex testing and records provider `codex`.
- Real HTTP handler traversal probes fail closed and do not leak raw paths or secrets.
- Valid manifest-listed artifacts and explicit public workspace docs still serve.
- Malformed, incomplete, unreadable, and legacy manifests render without breaking the dashboard and cannot authorize artifact access.
- Legacy commit-success metadata is clearly historical or degraded and does not imply current auto-commit behavior.
- Persisted text artifacts and representative served responses redact injected fake secrets.
- Non-dry-run with fake Codex leaves HEAD unchanged, preserves dirty state, and has no successful default commit metadata.
- Optional verification artifacts, if added, are redacted, run-relative, and manifest-listed.
- `go test ./internal/serve ./internal/run`, `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
