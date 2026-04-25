# TASK
## Objective
Implement `jj` as a Go CLI that turns a Markdown plan into reproducible planning, implementation, evaluation, and dashboard artifacts. The implementation must preserve the document-first workflow: plan to SPEC/TASK, SPEC/TASK to Codex implementation, implementation to evidence and evaluation, and dashboard to review.

## Constraints
- Use existing repository conventions and package layout where present.
- Keep `--cwd` workspace resolution separate from relative plan path resolution.
- Do not require `OPENAI_API_KEY`; Codex CLI fallback planning must work when no key is present.
- Do not call real OpenAI or real Codex in normal unit tests; use injected planners, fake implementers, and fake evaluators.
- Do not write workspace `docs/SPEC.md` or `docs/TASK.md` during `--dry-run`.
- Do not expose secrets in `.jjrc`-derived output, manifests, logs, generated docs, Codex events, evaluation output, or dashboard HTML.
- Keep manifest status transitions explicit for success, skipped steps, partial failure, and failure.
- `jj serve` is local-only and dashboard-first; it is not a marketing page or generic file browser.

## Implementation Steps
1. Establish CLI command structure.
- Add or update `cmd/jj` as the executable entrypoint.
- Implement `jj run <plan.md>` and `jj serve`.
- Add run flags for `--cwd`, `--run-id`, `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, `--task-doc`, `--allow-no-git`, and `--dry-run`.
- Add serve address or port flags.
- Implement config precedence: defaults, `.jjrc`, environment variables, CLI flags.

2. Implement workspace and config foundations.
- Validate that the plan exists, is readable, is Markdown, and is non-empty.
- Resolve `--cwd` as the target workspace while keeping relative plan paths based on the caller working directory.
- Create `.jj/runs/<run-id>/` safely and deterministically enough for tests.
- Add secret redaction helpers for env/config/manifest/log/rendered output.

3. Implement git capture.
- Detect git repository by default.
- Fail outside git unless `--allow-no-git` is set.
- Capture baseline commit, branch, dirty state, and status when available.
- Capture post-run status and full diff for non-dry-run.
- Record git metadata and no-git mode in `manifest.json`.

4. Implement planner pipeline.
- Define planner request/result types and a planner interface.
- Add injected planner support for tests and internal orchestration.
- Add OpenAI planner selection when `OPENAI_API_KEY` is present.
- Add Codex CLI fallback planner selection when no API key is present.
- Generate or preserve product/spec, implementation/tasking, and QA/evaluation drafts.
- Merge drafts into final `docs/SPEC.md` and `docs/TASK.md`.
- Persist raw planning outputs and final docs under the run directory.

5. Implement artifact and manifest writer.
- Copy the input plan to `.jj/runs/<run-id>/input.md`.
- Write run-local `docs/SPEC.md` and `docs/TASK.md` for every successful planning run.
- Represent skipped implementation, git, or evaluation steps explicitly.
- Write and update `manifest.json` with run id, timestamps, cwd, plan path, dry-run flag, no-git flag, planner provider, models, artifact paths, git metadata, Codex result, evaluation result, status, skipped steps, and errors.
- Redact sensitive values before writing artifacts or rendering them.

6. Implement dry-run behavior.
- Ensure `jj run plan.md --dry-run` writes only run-scoped planning artifacts, optional evaluation where possible, and manifest data.
- Ensure dry-run does not write workspace `docs/SPEC.md` or workspace `docs/TASK.md`.
- Ensure dry-run does not invoke Codex implementation.
- Assert dry-run does not create unintended git diffs in the workspace.

7. Implement non-dry-run behavior.
- Write generated SPEC/TASK docs to configured workspace doc paths before implementation.
- Invoke Codex CLI using generated docs as the implementation prompt.
- Capture Codex events and summary under the run directory.
- Capture post-run git status and diff.
- Run evaluation and write `docs/EVAL.md` to run artifacts and workspace docs when evaluation is possible.
- Update manifest consistently on success, Codex failure, evaluation failure, and partial completion.

8. Implement dashboard server.
- Make `jj serve --cwd .` serve a dashboard at `/`.
- Show current TASK state, active or latest run, recent runs, latest statuses, evaluation summary, failures/risks, and next actions.
- Add routes or links for README, `plan.md`, project docs, run artifacts, SPEC, TASK, EVAL, git diff, events, and manifest JSON.
- Escape HTML while rendering Markdown and artifacts.
- Apply redaction before displaying config, logs, manifests, generated docs, or event content.
- Handle empty runs, corrupt manifests, missing artifacts, and failed runs without crashing.

9. Implement evaluation.
- Generate `docs/EVAL.md` for non-dry-run and for dry-run where meaningful.
- Evaluate original plan coverage, SPEC/TASK coverage, implementation or dry-run evidence, tests run, failed criteria, skipped steps, unresolved risks, and recommended next action.
- Ensure evaluation output and manifest evaluation fields do not overstate success when Codex or tests were skipped or failed.

## Files and Packages to Inspect
- `cmd/jj`: CLI entrypoint and command wiring.
- `internal/cli`: flag parsing, config loading, command orchestration.
- `internal/config`: defaults, env vars, `.jjrc` parsing, secret redaction.
- `internal/workspace`: cwd handling, plan paths, docs paths, `.jj` paths, filesystem helpers.
- `internal/gitutil`: repo detection, baseline metadata, status, diff capture.
- `internal/runstore`: run id creation, artifact paths, manifest read/write.
- `internal/planner`: planner interface, merge logic, injected/OpenAI/Codex providers.
- `internal/codex`: Codex CLI implementation runner and event capture.
- `internal/eval`: evaluation result generation and `docs/EVAL.md` writing.
- `internal/server`: local HTTP dashboard and artifact browser.
- `internal/markdown`: Markdown validation and safe rendering helpers.
- `docs/`, `.jj/`, `.jjrc`, and existing tests for current conventions.

## Required Changes
- Add or complete CLI command handling for `run` and `serve`.
- Add config loading and precedence across defaults, `.jjrc`, env, and flags.
- Add secret redaction across persisted and rendered output.
- Add run store creation, artifact writing, and manifest schema updates.
- Add planner provider selection and fallback order.
- Add final SPEC/TASK merge behavior from product, implementation, and QA drafts.
- Add dry-run isolation from workspace docs and Codex execution.
- Add non-dry-run Codex invocation, evidence capture, git capture, and evaluation writing.
- Add dashboard-first server root page with artifact navigation and degraded-run handling.
- Add tests covering CLI validation, provider fallback, dry-run, no-git mode, manifest consistency, redaction, evaluation, and dashboard rendering.

## Testing Requirements
- Unit test missing plan, empty plan, directory path, unreadable file, invalid extension, valid Markdown, and `--cwd` path handling.
- Unit test config precedence, `.jjrc` loading, env var handling, and redaction helpers.
- Unit test planner provider selection for injected planner, OpenAI with API key, and Codex CLI fallback without API key.
- Unit test manifest required fields, artifact path consistency, skipped-step markers, status transitions, and no secret leakage.
- Integration test `jj run plan.md --dry-run` in a temp git repo and assert run-local artifacts exist while workspace docs are unchanged.
- Integration test no-git behavior: default failure and `--allow-no-git` success with manifest metadata.
- Integration test non-dry-run orchestration using fake planner, fake implementer, and fake evaluator.
- HTTP tests for dashboard root, empty runs, successful runs, failed runs, corrupt manifest fixtures, missing files, artifact links, and redacted output.
- Security regression tests that inject fake key/token/password values into env, `.jjrc`, fake planner output, fake Codex logs, and manifests, then assert raw secrets are absent everywhere.
- Run `gofmt` on changed Go files.
- Run `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`.

## Manual Verification
- Run `jj run plan.md --dry-run` and inspect `.jj/runs/<run-id>/input.md`, planning outputs, `docs/SPEC.md`, `docs/TASK.md`, and `manifest.json`.
- Confirm dry-run did not create or modify workspace `docs/SPEC.md` or `docs/TASK.md`.
- Run without `OPENAI_API_KEY` and confirm the manifest records Codex CLI fallback planning.
- Run in a non-git temp directory and confirm failure without `--allow-no-git` and success with it.
- Run a non-dry-run with fakes or a controlled Codex binary and confirm Codex events, summary, git status, git diff, evaluation, and manifest updates.
- Start `jj serve --cwd .` and confirm the root page is the dashboard with TASK state, recent runs, evaluation state, risks, failures, next actions, and links to artifacts.
- Check served pages and artifacts for absence of raw secret values.

## Done Criteria
- The required SPEC/TASK generation, provider fallback, dry-run behavior, non-dry-run evidence capture, evaluation, manifest, and dashboard flows are implemented.
- Manifest status accurately reflects completed, skipped, failed, and partial steps.
- Dashboard root is task/run-status focused and tolerates missing or corrupt artifacts.
- Secret redaction is applied consistently before persistence and rendering.
- Tests cover the major contracts and failure modes.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
