# TASK
## Objective
Implement or refine the Go-based `jj` CLI so one Markdown plan drives a document-first AI coding workflow: planning documents, run artifacts, optional Codex implementation, evaluation evidence, and a dashboard-first local UI.

The finished behavior must match the SPEC: `jj run` creates reproducible `.jj/runs/<run-id>/` artifacts, dry-run stays read-only outside the run directory, non-dry-run writes workspace docs before implementation, provider fallback works without `OPENAI_API_KEY`, and `jj serve` opens on a status dashboard.

## Constraints
- Follow the repository's existing package layout and style where present.
- Keep command wiring thin; place orchestration and business logic in internal packages.
- Do not rely on live OpenAI or Codex calls in automated tests; use injected planners and fake Codex runners.
- Never write workspace docs or code in dry-run mode.
- Resolve relative plan paths from the invocation directory, not from `--cwd`.
- Do not revert, overwrite unrelated user changes, or clean the git workspace.
- Record git evidence without trying to separate or undo pre-existing dirty state.
- Apply secret redaction before persisting or rendering logs, manifests, raw planner output, Codex output, errors, and HTML.
- Serve only local workspace/docs/run artifact paths and block traversal.
- Keep all generated manifest artifact paths relative to the run directory.

## Implementation Steps
1. Inspect the existing codebase: identify CLI framework, module layout, tests, current `jj run`, current `jj serve`, config handling, artifact code, and docs.
2. Add or refine typed configuration for run and serve commands, including defaults, `.jjrc`, environment variables, and CLI flags with precedence `flags > env > .jjrc > defaults`.
3. Implement centralized secret redaction for API keys, Bearer [redacted], Authorization headers, `sk-...` style keys, and secret-like config values.
4. Implement workspace and plan path handling: validate `--cwd`, resolve positional plan paths from caller directory, reject missing/empty/directory/non-Markdown plans, and preserve this behavior in tests.
5. Implement git helpers for repo detection, branch, HEAD, dirty state, status before/after, diff, diff stat, and explicit no-git metadata for `--allow-no-git`.
6. Implement the artifact store: safe run directory creation, run id collision detection, subdirectory creation, atomic text/JSON/Markdown writes, and relative artifact path helpers.
7. Define manifest structs and status transitions for dry-run success, success, partial failure, and failed runs. Ensure failed phases still write possible partial artifacts and a final manifest.
8. Define planner interfaces and draft structs. Support injected planner, OpenAI planner, and Codex CLI fallback planner in the required priority order.
9. Persist raw planning outputs for product/spec, implementation/tasking, QA/evaluation, plus merged planning output.
10. Implement draft merge into final SPEC and TASK Markdown using the required document section structure.
11. Implement the `jj run` orchestrator with explicit phases: validation, run setup, git baseline, planning, merge, run-local docs, optional workspace docs, optional Codex, git final capture, evaluation, manifest finalization.
12. Implement dry-run behavior so it writes only run-local planning artifacts and manifest, skips workspace docs, skips Codex, and records evaluation as skipped or not run.
13. Implement non-dry-run behavior so it writes workspace `docs/SPEC.md` and `docs/TASK.md` before Codex implementation, then captures Codex artifacts, git evidence, and `docs/EVAL.md` in both run artifacts and workspace.
14. Implement Codex runner behind an interface. Make binary path and model configurable. Capture events, stdout/stderr or summary, exit code, duration, and errors without leaking secrets.
15. Implement evaluation generation from plan/SPEC/TASK, Codex result, git diff summary, tests when available, risks, and next actions. Emit evaluation status `pass`, `warn`, `fail`, or `not_run`.
16. Implement `jj serve --cwd .` with a local-only HTTP server, dashboard route at `/`, safe Markdown rendering, redaction before rendering, and artifact routes constrained to allowed paths.
17. Build dashboard view models for current TASK status, active/recent runs, evaluation status, failures/risks, next actions, and links to README, plan, docs, run manifests, Codex summaries, and git diffs.
18. Add graceful serve behavior for missing docs, no runs, failed runs, malformed manifests, and redacted secret-like content.

## Files and Packages to Inspect
- `cmd/jj` for CLI entrypoint and command wiring.
- Existing internal packages for run orchestration, config, git, artifacts, planner, Codex integration, evaluation, and server behavior.
- `go.mod` and `go.sum` for available dependencies and module structure.
- Existing tests under `internal/...` and command tests.
- `README.md`, `plan.md`, `docs/`, and any existing `.jjrc` examples.
- Existing web templates, static files, Markdown rendering helpers, or HTTP handlers.

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
- Add CLI flags for `jj run`: `--cwd`, `--run-id`, `--dry-run`, `--allow-no-git`, `--planner-agents` or compatible `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, and `--task-doc`.
- Add CLI flags for `jj serve`: `--cwd` and `--addr`.
- Implement non-empty Markdown plan validation.
- Implement correct `--cwd` versus plan path resolution semantics.
- Implement git-required-by-default behavior and `--allow-no-git` fallback metadata.
- Implement run artifact layout under `.jj/runs/<run-id>/`.
- Implement manifest generation with config, git metadata, planner provider/model, Codex result, evaluation result, errors, redaction marker, and relative artifact paths.
- Implement planner provider selection and persistence of planning JSON.
- Implement final SPEC/TASK merge and write run-local docs for all successful planning runs.
- Ensure dry-run never writes workspace docs or invokes Codex.
- Ensure non-dry-run writes workspace docs before invoking Codex.
- Capture Codex events, summary, exit status, and errors.
- Capture git status before/after, diff patch, and diff stat.
- Generate `docs/EVAL.md` with compliance, test result, diff summary, risks, and next actions.
- Implement dashboard-first root page for `jj serve`.
- Implement safe Markdown rendering, secret redaction, and path traversal protection for served artifacts.

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
- Integration test no-git workspace: fail without `--allow-no-git`, succeed with it and record git unavailable.
- Integration test dirty git workspace: record `dirty_before` and preserve existing changes in evidence.
- Failure-path tests for planner failure, Codex failure, and evaluation failure: manifest status and partial artifacts remain.
- HTTP handler tests for dashboard with no docs, successful run, failed run, missing evaluation, and malformed manifest.
- HTTP security tests rejecting `../`, absolute paths, and hidden secret file access.
- Markdown rendering tests that raw script content is escaped or removed.
- Security regression test injecting fake secrets through env, `.jjrc`, plan, planner output, Codex output, and errors; assert artifacts and HTTP responses do not contain raw secrets.

## Manual Verification
- Run `jj run plan.md --dry-run --run-id <id>` and inspect `.jj/runs/<id>/input.md`, planning artifacts, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`.
- Confirm dry-run did not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files.
- Run with `OPENAI_API_KEY` unset and confirm manifest records Codex CLI fallback planner.
- Run in a non-git temp workspace without `--allow-no-git` and confirm it fails clearly.
- Run in a non-git temp workspace with `--allow-no-git` and confirm manifest records git unavailable.
- Run non-dry-run with fake or controlled Codex and confirm workspace SPEC/TASK, Codex artifacts, git diff artifacts, EVAL, and manifest are present.
- Start `jj serve --cwd .` and open `/`; confirm the first page is the dashboard with TASK state, recent run status, evaluation state, risks/failures, next actions, and links.
- Try artifact traversal paths and confirm they are rejected.
- Inspect manifest, artifacts, and served HTML for fake secret strings and confirm only redacted placeholders appear.
- Run final commands: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`.

## Done Criteria
- Required SPEC and TASK documents are generated with the expected sections.
- `jj run plan.md --dry-run` creates only run-local planning artifacts and manifest.
- Dry-run side effects on workspace docs/code are covered by tests.
- Provider fallback works without `OPENAI_API_KEY` and is recorded in manifest.
- Injected planner is available for deterministic tests.
- Non-dry-run records workspace docs, Codex artifacts, git evidence, evaluation output, and final manifest.
- Failure paths leave actionable errors, partial artifacts when available, and a failed or partial manifest.
- `jj serve --cwd .` root is dashboard-first and not a README or file listing.
- Dashboard and artifact serving redact secrets and block traversal.
- Automated tests cover config, path resolution, provider selection, artifact layout, manifest, redaction, run pipeline, and dashboard behavior.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
