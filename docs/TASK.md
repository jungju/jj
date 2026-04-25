# TASK
## Objective
Implement or refine the Go-based `jj` CLI so one Markdown plan drives a document-first AI coding workflow: planning documents, run artifacts, optional Codex implementation, evaluation evidence, and a dashboard-first local UI.

The immediate priority is to close the latest evaluation gaps: remove default automatic git commit behavior from non-dry-run execution and harden served artifact path validation before normalization. The finished behavior must match the SPEC: dry-run stays read-only outside the run directory, non-dry-run writes workspace docs before implementation, provider fallback works without `OPENAI_API_KEY`, git evidence is observational, and `jj serve` opens on a safe status dashboard.

## Constraints
- Follow the repository's existing package layout and style where present.
- Keep command wiring thin; place orchestration and business logic in internal packages.
- Do not rely on live OpenAI or Codex calls in automated tests; use injected planners and fake Codex runners.
- Never write workspace docs or code in dry-run mode.
- Resolve relative plan paths from the invocation directory, not from `--cwd`.
- Do not revert, overwrite unrelated user changes, clean the git workspace, stage files, or create commits.
- Record git evidence without trying to separate or undo pre-existing dirty state.
- Apply secret redaction before persisting or rendering logs, manifests, raw planner output, Codex output, errors, and HTML.
- Serve only local workspace docs and manifest-listed run artifact paths, and block traversal before path cleanup.
- Keep all generated manifest artifact paths relative to the run directory.

## Implementation Steps
1. Inspect the existing codebase: identify CLI framework, module layout, tests, current `jj run`, current `jj serve`, config handling, artifact code, manifest code, git helpers, and docs.
2. Inspect current non-dry-run git flow and identify any calls that stage, commit, reset, checkout, stash, clean, or otherwise mutate git history.
3. Remove default commit execution from the run pipeline. Do not add a new commit feature in this iteration.
4. Update manifest generation so commit metadata is absent or explicitly marked skipped or `ran:false`; never report commit success in the default workflow.
5. Ensure final git evidence still captures baseline, `status.before`, `status.after`, `diff.patch`, `diff.stat.txt`, dirty flags, HEAD, branch, and git availability.
6. Ensure final git capture happens after workspace docs, Codex execution, and evaluation generation so evidence describes the reviewable uncommitted result.
7. Update dashboard or run summary text if it currently treats lack of commit as failure.
8. Preserve existing typed configuration behavior, including defaults, `.jjrc`, environment variables, and CLI flags with precedence `flags > env > .jjrc > defaults`.
9. Preserve centralized secret redaction for API keys, Bearer [redacted], Authorization headers, `sk-...` style keys, and secret-like config values.
10. Preserve workspace and plan path handling: validate `--cwd`, resolve positional plan paths from caller directory, reject missing/empty/directory/non-Markdown plans, and keep test coverage.
11. Preserve git-required-by-default behavior and `--allow-no-git` fallback metadata.
12. Preserve artifact store behavior: safe run directory creation, run id collision detection, subdirectory creation, atomic text/JSON/Markdown writes, and relative artifact path helpers.
13. Preserve planner interfaces and provider selection order: injected planner, OpenAI planner, then Codex CLI fallback planner.
14. Preserve raw planning output persistence for product/spec, implementation/tasking, QA/evaluation, plus merged planning output.
15. Preserve final SPEC/TASK merge using the required document section structures.
16. Inspect artifact-serving route parsing and path resolution in `jj serve`.
17. Add or refine a raw artifact path validation helper near serve routing code.
18. Validate the raw URL path after URL decoding and before `path.Clean`, `filepath.Clean`, or root joining.
19. Reject `..` segments, encoded traversal, absolute Unix paths, Windows drive or UNC-style paths, backslashes, NUL bytes, empty paths, and hidden path segments.
20. Restrict served run artifacts to manifest-listed artifact paths or an explicit public workspace document allowlist.
21. Keep a final resolved-path containment check after joining to the allowed root.
22. Preserve dashboard-first root route with safe Markdown rendering, redaction before rendering, and graceful behavior for missing docs, no runs, failed runs, malformed manifests, and redacted secret-like content.
23. Add or update regression tests for no default commit behavior and traversal rejection.
24. Run `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`.

## Files and Packages to Inspect
- `cmd/jj` for CLI entrypoint and command wiring.
- `internal/run/run.go` for run orchestration and non-dry-run phase order.
- `internal/run/git.go` for git helpers and any commit/stage helpers.
- `internal/run/run_test.go` for run pipeline, manifest, dry-run, non-dry-run, and git tests.
- `internal/serve/serve.go` for dashboard and artifact route handling.
- `internal/serve/serve_test.go` for HTTP dashboard and security tests.
- `internal/serve/web_run.go` if dashboard run state is affected.
- Manifest structs and serialization helpers.
- Existing internal packages for config, workspace, artifacts, planner, Codex integration, evaluation, and server behavior.
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
- `internal/run`
- `internal/server`

## Required Changes
- Remove or disable default calls to `git add`, `git commit`, or helper methods that create commits during `jj run`.
- Ensure default non-dry-run does not change `git rev-parse HEAD`.
- Ensure pre-existing dirty files are not staged, committed, reverted, or otherwise modified by commit logic.
- Ensure non-dry-run still writes workspace SPEC/TASK before Codex implementation.
- Ensure non-dry-run still captures Codex events, summary, exit status, and errors.
- Ensure non-dry-run still captures git status before/after, diff patch, and diff stat after evaluation generation.
- Ensure workspace and run-local `docs/EVAL.md` are still generated for non-dry-run.
- Update manifest generation with accurate dirty metadata, artifact paths, planner provider/model, Codex result, evaluation result, errors, redaction marker, and no successful default commit metadata.
- Keep CLI flags for `jj run`: `--cwd`, `--run-id`, `--dry-run`, `--allow-no-git`, `--planner-agents` or compatible `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, and `--task-doc`.
- Keep CLI flags for `jj serve`: `--cwd` and `--addr`.
- Add raw decoded artifact path validation before normalization.
- Reject `docs/../manifest.json`, encoded traversal, `.secret/../manifest.json`, absolute paths, backslash traversal, Windows drive paths, NUL bytes, empty paths, hidden run artifacts, and paths outside allowed roots.
- Still serve valid manifest-listed artifacts and explicit public workspace docs.
- Preserve safe Markdown rendering and secret redaction for served content.

## Testing Requirements
- Unit test input validation for missing, empty, directory, non-Markdown, and valid Markdown plans.
- Unit test `--cwd` with relative plan paths to prove the plan path is resolved from invocation directory.
- Unit test config precedence: CLI flags, env vars, `.jjrc`, defaults.
- Unit test secret redaction for OpenAI keys, Bearer [redacted], Authorization headers, and secret-like `.jjrc` values.
- Unit test planner provider selection: injected first, OpenAI when key exists, Codex fallback when no key exists.
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
- HTTP handler tests for dashboard with no docs, successful run, failed run, missing evaluation, and malformed manifest.
- HTTP security tests rejecting `docs/../manifest.json`, encoded traversal such as `docs%2f..%2fmanifest.json` and `%2e%2e`, `.secret/../manifest.json`, absolute paths, Windows drive paths, backslash traversal, NUL bytes, hidden segments, and hidden run artifacts.
- HTTP handler test proving a valid manifest-listed artifact still serves successfully.
- Markdown rendering tests that raw script content is escaped or removed.
- Security regression test injecting fake secrets through env, `.jjrc`, plan, planner output, Codex output, and errors; assert artifacts and HTTP responses do not contain raw secrets.

## Manual Verification
- Run `jj run plan.md --dry-run --run-id <id>` and inspect `.jj/runs/<id>/input.md`, planning artifacts, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`.
- Confirm dry-run did not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files.
- Run with `OPENAI_API_KEY` unset and confirm manifest records Codex CLI fallback planner.
- Run in a non-git temp workspace without `--allow-no-git` and confirm it fails clearly.
- Run in a non-git temp workspace with `--allow-no-git` and confirm manifest records git unavailable.
- Run non-dry-run with fake or controlled Codex and confirm workspace SPEC/TASK, Codex artifacts, git diff artifacts, EVAL, and manifest are present.
- In a git repo, record `git rev-parse HEAD`, run non-dry-run, and confirm HEAD is unchanged.
- Create a dirty file before non-dry-run and confirm it remains uncommitted while baseline and final git evidence record it.
- Start `jj serve --cwd .` and open `/`; confirm the first page is the dashboard with TASK state, recent run status, evaluation state, risks/failures, next actions, and links.
- Try artifact traversal paths including raw `../`, encoded traversal, hidden segments, absolute paths, and backslash paths; confirm they are rejected.
- Inspect manifest, artifacts, and served HTML for fake secret strings and confirm only redacted placeholders appear.
- Run final commands: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`.

## Done Criteria
- Required SPEC and TASK documents are generated with the expected sections.
- `jj run plan.md --dry-run` creates only run-local planning artifacts and manifest.
- Dry-run side effects on workspace docs/code are covered by tests.
- Provider fallback works without `OPENAI_API_KEY` and is recorded in manifest.
- Injected planner is available for deterministic tests.
- Non-dry-run records workspace docs, Codex artifacts, git evidence, evaluation output, and final manifest.
- Default non-dry-run creates no git commit, leaves HEAD unchanged, and does not stage or mutate git history.
- Pre-existing dirty changes are preserved and recorded as baseline evidence.
- Failure paths leave actionable errors, partial artifacts when available, and a failed or partial manifest.
- `jj serve --cwd .` root is dashboard-first and not a README or file listing.
- Dashboard and artifact serving redact secrets and block traversal before path normalization.
- Valid manifest-listed artifacts still serve after path hardening.
- Automated tests cover config, path resolution, provider selection, artifact layout, manifest, redaction, run pipeline, no-default-commit behavior, and dashboard security.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
