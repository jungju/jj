# SPEC
## Overview
`jj` is a local, document-first CLI for reproducible AI coding workflows. Given one non-empty Markdown plan, it generates implementation-ready `docs/SPEC.md` and `docs/TASK.md`, optionally runs Codex implementation, captures git and execution evidence, produces evaluation output, and exposes the current state through a dashboard-first local web UI.

This iteration focuses on turning the latest PARTIAL evaluation gaps into repeatable verification evidence. `jj` must prove, through deterministic local tests or verification artifacts, that Codex fallback works without `OPENAI_API_KEY`, artifact serving remains fail-closed and path-safe through the real HTTP stack, redaction covers persisted and served surfaces, and default non-dry-run behavior does not mutate git history.

## Goals
- Generate concrete `docs/SPEC.md` and `docs/TASK.md` from one Markdown plan.
- Use product/spec, implementation/tasking, and QA/evaluation planning perspectives.
- Run without `OPENAI_API_KEY` by falling back to Codex CLI planning.
- Run Codex implementation in the target workspace for non-dry-run executions.
- Store every run under `.jj/runs/<run-id>/` for audit, comparison, and debugging.
- Capture Codex events, summaries, git status, git diff, evaluation output, and manifest metadata.
- Leave non-dry-run changes uncommitted for human review.
- Provide `jj serve` as a dashboard-first local web UI showing current TASK state, recent runs, evaluation state, risks, failures, and next actions.
- Keep secrets out of manifests, logs, planner artifacts, Codex artifacts, generated summaries, served HTML, and user-facing errors.
- Add reproducible verification coverage for previously manual or reported-only safety checks.

## Non-Goals
- `jj` is not a cloud service.
- `jj` is not a multi-user dashboard.
- `jj` is not a general-purpose DAG or workflow engine.
- `jj` does not replace git review.
- `jj` does not guarantee AI output correctness.
- `jj` does not expose arbitrary local files through the web UI.
- `jj run` does not create git commits by default.
- Adding an automatic commit feature is out of scope.
- Automated tests must not require live OpenAI API calls or a real external Codex invocation.
- Legacy manifests must not be made trusted by loosening artifact allowlisting.

## User Stories
- As an individual developer, I want to write `plan.md` once and get SPEC, TASK, implementation evidence, and evaluation artifacts without repeating context across tools.
- As a small team member, I want AI-generated changes to include requirements, task instructions, diff evidence, and evaluation notes so reviewers can understand the change.
- As an AI workflow experimenter, I want each run to be reproducible and comparable through run manifests and artifacts.
- As a user without an OpenAI API key, I want planning to still work through Codex CLI fallback.
- As a reviewer, I want `jj serve --cwd .` to open on current task and run status rather than a file listing.
- As a git user, I want `jj` to leave reviewable changes in the working tree instead of committing them automatically.
- As a dashboard user, I want old, malformed, or partial run manifests to appear as unavailable entries without breaking the page.
- As a maintainer, I want verification tests or artifacts that prove fallback, redaction, traversal rejection, and no-commit behavior without manual inspection.

## Functional Requirements
- `jj run <plan.md>` must read an existing, non-empty Markdown plan file.
- The positional plan path must be resolved relative to the shell invocation directory, not relative to `--cwd`.
- `--cwd` must select the target workspace where `.jj/runs`, workspace docs, Codex execution, git capture, and serve content are rooted.
- By default the target workspace must be a git repository.
- `--allow-no-git` must allow non-git workspaces and record `git.available=false` in the manifest.
- `--run-id` must select the run directory name and fail if that run already exists.
- If `--run-id` is omitted, `jj` must generate a unique time-based run id.
- Planner provider selection order must be injected planner, OpenAI planner when `OPENAI_API_KEY` is present, then Codex CLI fallback planner when no API key is present.
- Planning output must be structured and persisted as raw planning artifacts after redaction.
- Drafts must be merged into final run-local `docs/SPEC.md` and `docs/TASK.md` for every successful planning run.
- Dry-run must not write workspace `docs/SPEC.md`, workspace `docs/TASK.md`, workspace `docs/EVAL.md`, or code files.
- Dry-run must not invoke implementation Codex.
- Non-dry-run must write workspace SPEC and TASK before running implementation Codex.
- Non-dry-run must capture Codex events, Codex summary, Codex exit status, final git status, git diff patch, git diff stat, and evaluation output.
- Non-dry-run must not run `git add`, `git commit`, `git reset`, `git checkout`, `git stash`, `git clean`, or any other git history-mutating command.
- Pre-existing dirty workspace state must be preserved and recorded as baseline evidence.
- Failed phases must still leave produced artifacts and a failed or partial manifest when possible.
- `jj serve --cwd .` must serve a local dashboard at `/`.
- The dashboard must render when `.jj/runs` is empty.
- The dashboard must render when one or more run manifests are malformed, unreadable, incomplete, legacy-shaped, or reference missing artifacts.
- Malformed or legacy runs must be represented as invalid, unavailable, or degraded entries with concise redacted errors and no trusted artifact links.
- Missing, skipped, absent, or legacy commit metadata must not be treated as a current workflow failure.
- Artifact links must be generated only for paths that are safe and allowed by the manifest or explicit public workspace document allowlist.
- Artifact routes must fail closed when a run manifest is malformed, missing, or lacks the requested artifact path.
- The serve UI must render Markdown safely and block path traversal or access outside allowed workspace/run artifact paths.
- Verification coverage must prove Codex fallback with `OPENAI_API_KEY` unset, HTTP traversal rejection, persisted and served redaction, malformed-manifest fail-closed behavior, and default no-commit behavior.

## CLI Behavior
### `jj run <plan.md>`
Supported flags:
- `--cwd <path>`: target workspace. Defaults to current directory.
- `--run-id <id>`: explicit run id. Existing run directory is an error.
- `--dry-run`: generate planning artifacts only.
- `--allow-no-git`: permit execution outside a git repository.
- `--planner-agents <n>` or `--planning-agents <n>`: planning perspective count. Default is 3.
- `--openai-model <model>`: OpenAI planner model override.
- `--codex-model <model>`: Codex fallback and implementation model override.
- `--spec-doc <path>`: workspace spec document path. Default `docs/SPEC.md`.
- `--task-doc <path>`: workspace task document path. Default `docs/TASK.md`.

For non-dry-run executions, `jj run` writes workspace docs, runs Codex, captures evidence, generates evaluation output, and leaves resulting changes uncommitted for review. Any manifest commit section must be absent or explicitly record `ran:false` or `status:skipped`; it must never report a successful automatic commit for the default workflow.

### `jj serve`
Supported flags:
- `--cwd <path>`: target workspace. Defaults to current directory.
- `--addr <host:port>`: local bind address. Default must be local-only.

The root route `/` must render the dashboard. Artifact routes must validate the raw decoded request path before cleaning, joining, or normalization. Invalid artifact requests must return a non-2xx response without leaking filesystem paths or raw secret-like values.

A new user-visible `jj verify` command is optional. Prefer strengthening deterministic tests and run-local verification artifacts unless a narrow CLI command fits the existing command structure cleanly.

## Pipeline Behavior
1. Resolve the positional plan path from the invocation directory.
2. Resolve and validate the target workspace from `--cwd`.
3. Validate that the plan exists, is Markdown, and contains non-whitespace content.
4. Validate git unless `--allow-no-git` is set.
5. Create `.jj/runs/<run-id>/` and required subdirectories.
6. Write redacted `input.md` and related input artifacts.
7. Capture git baseline metadata when available: repo root, branch, HEAD, dirty state, and status before.
8. Select the planner provider using injected, OpenAI, then Codex CLI fallback order.
9. Run planning perspectives and persist raw planning JSON with redaction applied before persistence.
10. Merge planning drafts into final run-local `docs/SPEC.md` and `docs/TASK.md`.
11. Write or update manifest state as phases complete.
12. If dry-run, skip workspace doc writes, implementation Codex, git diff after implementation, and workspace evaluation.
13. If non-dry-run, write workspace `docs/SPEC.md` and `docs/TASK.md` before implementation.
14. Run Codex implementation using generated SPEC and TASK.
15. Generate `docs/EVAL.md` in the run artifacts and workspace for non-dry-run.
16. Capture final git status, git diff patch, and git diff stat after docs, Codex, and evaluation have completed or failed as far as possible.
17. Do not stage, commit, reset, checkout, stash, clean, or otherwise mutate git history.
18. Finish manifest with final status, errors, artifact paths, provider metadata, Codex result, git metadata, and evaluation result.
19. When serving, load run manifests independently so one malformed manifest cannot abort the dashboard.
20. For malformed or untrusted manifests, render a sanitized invalid-run summary and omit trusted artifact links.
21. Verification tests or artifacts must exercise the actual HTTP handler stack for traversal probes and scan deterministic text artifacts and representative HTTP responses for fake secret leakage.

## Artifact Layout
Each run is stored under `.jj/runs/<run-id>/`.

Required planning artifacts:
- `input.md`
- `planning/product_spec.json`
- `planning/implementation_task.json`
- `planning/qa_eval.json`
- `planning/merged.json` or equivalent merged planning artifact
- `docs/SPEC.md`
- `docs/TASK.md`
- `manifest.json`

Required non-dry-run artifacts when available:
- `docs/EVAL.md`
- `codex/events.jsonl`
- `codex/summary.md`
- `codex/exit.json`
- `git/baseline.txt` or `git/baseline.json`
- `git/status.before.txt`
- `git/status.after.txt`
- `git/diff.patch`
- `git/diff.stat.txt` or `git/diff-summary.txt`

Optional verification artifacts, when implemented, must be run-relative and redacted:
- `verification/summary.md`
- `verification/results.json`
- `verification/http-probes.jsonl`

`manifest.json` must include at least run identity and status, effective non-secret config, planner provider/model and artifacts, git availability and dirty metadata, Codex status and artifacts, evaluation status and summary, an artifact map, errors, and a redaction marker.

## Configuration
Configuration sources must be merged with deterministic precedence: CLI flags, environment variables, `.jjrc`, then defaults.

Configurable values include target workspace cwd, run id, planner agent count, OpenAI model, Codex binary path, Codex model, spec document path, task document path, eval document path, dry-run mode, no-git mode, and serve bind address.

Environment variables may configure OpenAI key/model and Codex binary/model. `.jjrc` may define project defaults. `.jjrc`, manifests, logs, verification artifacts, and served HTML must never expose actual API keys, Bearer [redacted], authorization headers, or secret-like config values.

## Error Handling
- Validation failures must identify the failed phase and reason without exposing secrets or absolute sensitive paths.
- Planner parse failures must not write successful empty SPEC/TASK documents.
- Codex and evaluation failures must leave partial artifacts and failed or partial manifest state when possible.
- Malformed manifests must degrade only the affected run summary, not the entire dashboard.
- Artifact requests against malformed, missing, or untrusted manifests must fail closed.
- Unsafe paths including traversal, encoded traversal, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL bytes, empty paths, hidden segments, and hidden artifacts must be rejected before normalization can make them look safe.

## Security and Privacy
- Apply central redaction before persisting or rendering plan input, planner output, Codex output, evaluation output, manifests, errors, Markdown, and HTML.
- Redact OpenAI keys, Bearer [redacted], authorization headers, and secret-like `.jjrc` values.
- Serve only manifest-listed run artifacts and explicit public workspace docs.
- Do not trust malformed or legacy manifests for artifact authorization.
- Do not include raw secret strings in verification artifacts or scan reports.
- Do not expose arbitrary filesystem paths or local files through error pages or artifact routes.

## Observability
- Every run must produce enough manifest and artifact evidence to understand phase status, provider selection, git baseline/final state, Codex execution, evaluation result, and next action.
- The dashboard must show current TASK status, recent run status, evaluation state, failures, risks, and next actions.
- Degraded manifest rows must explain that artifact links are unavailable for safety when a manifest is malformed, incomplete, or lacks a trusted artifact allowlist.
- Verification evidence should record which checks ran, which request or command shape was exercised, and whether raw secrets, unsafe access, or git mutation were observed.

## Acceptance Criteria
- `jj run plan.md --dry-run` creates `.jj/runs/<run-id>/input.md`, planning artifacts, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`.
- Dry-run does not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files.
- Without `OPENAI_API_KEY`, planner provider is Codex CLI fallback and is recorded as `codex` in the manifest using deterministic fake-Codex coverage.
- With `OPENAI_API_KEY`, OpenAI is selected unless an injected planner is present.
- Injected planner always has highest priority for tests and internal callers.
- Non-dry-run writes workspace SPEC/TASK before Codex implementation and records Codex artifacts, git status/diff, `docs/EVAL.md`, and manifest evaluation result.
- Non-dry-run does not create a git commit, does not stage files, and leaves `git rev-parse HEAD` unchanged.
- Pre-existing dirty changes are preserved and recorded as baseline evidence.
- Non-git workspaces fail by default and run with `--allow-no-git` while recording git unavailable metadata.
- `jj serve --cwd .` renders a dashboard for no runs, valid runs, failed runs, malformed manifests, incomplete manifests, and legacy commit-success manifests.
- Malformed or untrusted manifests cannot authorize artifact access.
- Valid manifest-listed artifacts and explicit public workspace docs still serve successfully.
- Representative raw and encoded traversal probes through the real HTTP stack return non-2xx and do not leak raw paths or fake secrets.
- Fake secrets injected through plan text, planner output, Codex output, config-like values, manifest-like values, docs, and served pages do not appear in persisted text artifacts or representative HTTP responses.
- Optional verification artifacts are redacted, run-relative, and linked only through the manifest artifact map.
- `go test ./internal/serve ./internal/run`, `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
