# TASK
## Objective
Implement or refine the Go-based `jj` CLI so one Markdown plan drives a document-first AI coding workflow: planning documents, run artifacts, optional Codex implementation, evaluation evidence, and a dashboard-first local UI.

The immediate priority is to close the latest PARTIAL evaluation gaps while preserving prior fixes: keep default non-dry-run git behavior observational with no commits, make `jj serve` graceful with malformed or legacy manifests, keep artifact serving fail-closed and path-safe, prove Codex fallback selection without `OPENAI_API_KEY`, and add redaction coverage across persisted artifacts and served HTML.

## Constraints
- Follow the repository's existing package layout and style where present.
- Keep command wiring thin; place orchestration and business logic in internal packages.
- Do not rely on live OpenAI or live Codex calls in automated tests; use injected planners, fake Codex binaries, or fake runners.
- Never write workspace docs or code in dry-run mode.
- Resolve relative plan paths from the invocation directory, not from `--cwd`.
- Do not revert, overwrite unrelated user changes, clean the git workspace, stage files, or create commits.
- Record git evidence without trying to separate or undo pre-existing dirty state.
- Apply secret redaction before persisting or rendering logs, manifests, raw planner output, Codex output, errors, Markdown, and HTML.
- Serve only local workspace docs and manifest-listed run artifact paths, and block traversal before path cleanup.
- Keep artifact serving fail-closed for malformed or untrusted manifests.
- Keep all generated manifest artifact paths relative to the run directory.
- Do not loosen artifact allowlisting to make legacy manifests work.
- Do not add a commit feature in this iteration.

## Implementation Steps
1. Inspect the existing codebase: CLI framework, module layout, tests, current `jj run`, current `jj serve`, config handling, artifact code, manifest code, git helpers, planner selection, redaction helpers, and docs.
2. Confirm current non-dry-run git flow has no calls that stage, commit, reset, checkout, stash, clean, or otherwise mutate git history.
3. Preserve no-default-commit behavior and ensure manifest commit metadata is absent, skipped, or `ran:false` for newly generated default runs.
4. Confirm final git evidence still captures baseline, `status.before`, `status.after`, diff patch, diff stat or summary, dirty flags, HEAD, branch, and git availability.
5. Inspect `internal/serve` manifest loading, dashboard aggregation, run summary construction, artifact link generation, and artifact route authorization.
6. Add or refine a manifest parse result type that distinguishes valid runs from malformed, unreadable, incomplete, or legacy runs without aborting dashboard rendering.
7. Update dashboard rendering so malformed runs show a degraded invalid/unavailable row with redacted error text and no trusted artifact links.
8. Ensure valid runs still show status, evaluation, risks, next action, and artifact links when another run is malformed.
9. Ensure legacy commit metadata is treated as historical only; missing, skipped, absent, or old successful commit fields must not degrade current workflow health.
10. Ensure artifact route authorization fails closed when a run manifest is malformed, missing, lacks an artifact map, or does not list the requested artifact path.
11. Preserve and extend raw decoded artifact path validation before `path.Clean`, `filepath.Clean`, root joining, or normalization.
12. Reject `docs/../manifest.json`, encoded traversal, `.secret/../manifest.json`, absolute paths, Windows drive and UNC-style paths, backslash traversal, NUL bytes, empty paths, hidden segments, hidden artifacts, and paths outside allowed roots.
13. Ensure valid manifest-listed artifacts and explicit public workspace docs still serve successfully.
14. Inspect planner provider selection in `internal/run` or the relevant planner/config package.
15. Add a deterministic test that unsets `OPENAI_API_KEY`, points Codex config or PATH to a temporary fake executable, runs a dry-run or planning-only pipeline, and asserts the manifest records provider `codex`.
16. Keep the fake Codex executable minimal and matched to the fallback parser contract: emit valid planner JSON or expected Codex fallback output and exit zero.
17. Inspect redaction entry points for plan persistence, planner artifacts, Codex events and summaries, manifest writes, errors, dashboard rendering, Markdown rendering, and artifact views.
18. Add one end-to-end redaction regression that injects fake secrets through plan text, planner output, Codex output, config or `.jjrc`, manifest values, and served pages where practical.
19. Scan created run artifacts and representative HTTP responses for raw fake secret strings; assert only redacted placeholders appear.
20. If any surface bypasses redaction, route that write or render path through the central redaction helper immediately before persistence or HTML output.
21. Add or update HTTP tests using the actual `net/http` request path handling stack for raw and encoded traversal cases.
22. Re-run existing no-commit regression tests to confirm HEAD remains unchanged, dirty workspace state remains dirty, and no successful commit metadata appears.
23. Run focused tests first: `go test ./internal/serve ./internal/run`.
24. Run final verification: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`.

## Files and Packages to Inspect
- `cmd/jj` for CLI entrypoint and command wiring.
- `internal/run/run.go` for run orchestration and phase order.
- `internal/run/git.go` for git helpers and mutation risks.
- `internal/run/run_test.go` for run pipeline, manifest, dry-run, non-dry-run, no-commit, fallback, and git tests.
- `internal/serve/serve.go` for dashboard and artifact route handling.
- `internal/serve/web_run.go` for dashboard run state and manifest loading, if present.
- `internal/serve/serve_test.go` for HTTP dashboard and security tests.
- Manifest structs and serialization helpers.
- Existing internal packages for config, workspace, artifacts, planner, Codex integration, evaluation, redaction, and server behavior.
- `go.mod` and `go.sum` for available dependencies and module structure.
- `README.md`, `plan.md`, `docs/`, and any existing `.jjrc` examples.

If no equivalent packages exist, use this target structure:
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
- Ensure non-dry-run still captures Codex events, summary, exit status, and errors.
- Ensure non-dry-run still captures git status before/after, diff patch, and diff stat after evaluation generation.
- Ensure workspace and run-local `docs/EVAL.md` are still generated for non-dry-run.
- Update manifest generation with accurate dirty metadata, artifact paths, planner provider/model, Codex result, evaluation result, errors, redaction marker, and no successful default commit metadata.
- Keep CLI flags for `jj run`: `--cwd`, `--run-id`, `--dry-run`, `--allow-no-git`, `--planner-agents` or compatible `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, and `--task-doc`.
- Keep CLI flags for `jj serve`: `--cwd` and `--addr`.
- Add or refine run-summary loading so malformed manifests cannot crash the dashboard.
- Render malformed, unreadable, incomplete, or legacy manifests as sanitized invalid-run entries.
- Generate artifact links only from manifest-listed artifacts or explicit public workspace document allowlist entries.
- Fail closed for artifact requests against malformed, missing, or untrusted manifests.
- Preserve safe Markdown rendering and secret redaction for served content.
- Add raw decoded artifact path validation before normalization.
- Reject `docs/../manifest.json`, encoded traversal, `.secret/../manifest.json`, absolute paths, backslash traversal, Windows drive paths, UNC-style paths, NUL bytes, empty paths, hidden run artifacts, and paths outside allowed roots.
- Still serve valid manifest-listed artifacts and explicit public workspace docs.
- Add deterministic Codex fallback test coverage with `OPENAI_API_KEY` unset and no injected planner.
- Add redaction coverage that scans persisted run artifacts and representative HTTP responses.

## Testing Requirements
- Unit test input validation for missing, empty, directory, non-Markdown, and valid Markdown plans.
- Unit test `--cwd` with relative plan paths to prove the plan path is resolved from invocation directory.
- Unit test config precedence: CLI flags, env vars, `.jjrc`, defaults.
- Unit test secret redaction for OpenAI keys, Bearer [redacted], Authorization headers, and secret-like `.jjrc` values.
- Unit test planner provider selection: injected first, OpenAI when key exists, Codex fallback when no key exists.
- Deterministic Codex fallback test with `OPENAI_API_KEY` unset and a fake Codex binary or runner; assert `planner.provider=codex` in `manifest.json`.
- Unit test manifest serialization, status fields, and relative artifact paths.
- Unit test run id collision handling.
- Unit test malformed planner output so it fails without writing successful empty SPEC/TASK.
- Integration test dry-run with fake planner: run artifacts are created, workspace docs/code are untouched, Codex runner is not called.
- Integration test non-dry-run in a temporary git repo with fake planner and fake Codex runner: workspace docs, Codex artifacts, git status/diff, EVAL, and manifest are created.
- Regression test non-dry-run with fake planner and fake Codex does not create a git commit and leaves HEAD unchanged.
- Regression test dirty workspace before run remains dirty, is recorded as `dirty_before=true`, and is not committed.
- Manifest test asserts default non-dry-run has no successful commit metadata and still has git availability, dirty flags, status paths, diff path, and diff stat path.
- Integration test no-git workspace: fail without `--allow-no-git`, succeed with it and record git unavailable.
- Failure-path tests for planner failure, Codex failure, and evaluation failure: manifest status and partial artifacts remain.
- HTTP handler tests for dashboard with no docs, no runs, successful run, failed run, missing evaluation, malformed manifest, incomplete manifest, legacy commit-success manifest, missing artifact references, and mixed valid/invalid runs.
- HTTP security tests rejecting `docs/../manifest.json`, encoded traversal such as `docs%2f..%2fmanifest.json` and `%2e%2e`, `.secret/../manifest.json`, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL bytes, hidden segments, and hidden run artifacts.
- HTTP handler test proving a valid manifest-listed artifact still serves successfully.
- HTTP handler test proving a malformed-manifest run cannot serve unlisted artifacts.
- Markdown rendering tests that raw script content is escaped or removed.
- Security regression test injecting fake secrets through env, `.jjrc`, plan, planner output, Codex output, errors, manifests, and served pages; assert artifacts and HTTP responses do not contain raw secrets.
- Run focused tests: `go test ./internal/serve ./internal/run`.
- Run final verification: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`.

## Manual Verification
- Run `jj run plan.md --dry-run --run-id <id>` and inspect `.jj/runs/<id>/input.md`, planning artifacts, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`.
- Confirm dry-run did not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files.
- Run with `OPENAI_API_KEY` unset and confirm manifest records Codex CLI fallback planner.
- Run in a non-git temp workspace without `--allow-no-git` and confirm it fails clearly.
- Run in a non-git temp workspace with `--allow-no-git` and confirm manifest records git unavailable.
- Run non-dry-run with fake or controlled Codex and confirm workspace SPEC/TASK, Codex artifacts, git diff artifacts, EVAL, and manifest are present.
- In a git repo, record `git rev-parse HEAD`, run non-dry-run, and confirm HEAD is unchanged.
- Create a dirty file before non-dry-run and confirm it remains uncommitted while baseline and final git evidence record it.
- Create malformed, incomplete, and legacy manifest directories under `.jj/runs` and start `jj serve --cwd .`; confirm `/` still renders the dashboard.
- Confirm malformed runs appear as invalid or unavailable entries and valid runs remain usable.
- Start `jj serve --cwd .` and open `/`; confirm the first page is the dashboard with TASK state, recent run status, evaluation state, risks/failures, next actions, and links.
- Try artifact traversal paths including raw `../`, encoded traversal, hidden segments, absolute paths, Windows-style paths, NUL bytes, and backslash paths; confirm they are rejected.
- Confirm valid manifest-listed artifacts and explicit public workspace docs still serve.
- Inspect manifest, artifacts, and served HTML for fake secret strings and confirm only redacted placeholders appear.
- Run final commands: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`.

## Done Criteria
- Required SPEC and TASK documents are generated with the expected sections.
- `jj run plan.md --dry-run` creates only run-local planning artifacts and manifest.
- Dry-run side effects on workspace docs/code are covered by tests.
- Provider fallback works without `OPENAI_API_KEY` and is recorded in manifest.
- Injected planner is available for deterministic tests.
- Non-dry-run records workspace docs, Codex artifacts, git evidence, evaluation output, and final manifest.
- Default non-dry-run creates no git commit, leaves HEAD unchanged, and produces no successful commit metadata.
- Pre-existing dirty workspace state is preserved and recorded.
- `jj serve --cwd .` renders a dashboard-first root page.
- Dashboard rendering is graceful for empty runs, malformed manifests, incomplete manifests, legacy manifests, missing artifact references, and mixed valid/invalid runs.
- Artifact serving rejects traversal, encoded traversal, hidden paths, absolute paths, Windows-style paths, backslashes, NUL bytes, unlisted artifacts, and malformed-manifest artifact requests.
- Valid manifest-listed artifacts and public workspace docs still serve successfully.
- Secret redaction is verified across persisted artifacts and representative served HTML.
- The implementation preserves existing CLI flags, config precedence, artifact layout, and manifest-relative artifact paths.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
