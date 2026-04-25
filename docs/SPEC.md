# SPEC
## Overview
`jj` is a Go CLI that turns one non-empty Markdown plan into implementation-ready documentation, optional Codex execution, evaluation, and auditable run artifacts. It keeps planning, implementation, test evidence, git state, and review output together under `.jj/runs/<run-id>/` so AI coding workflows are reproducible and comparable.

## Goals
- Generate concrete `docs/SPEC.md` and `docs/TASK.md` from one plan file.
- Use multiple planning perspectives: product/spec, implementation/tasking, and QA/evaluation.
- Support `OPENAI_API_KEY` planning when available and Codex CLI fallback when it is not.
- Connect generated docs to Codex implementation in non-dry-run mode.
- Capture input, planning JSON, generated docs, Codex output, git status, git diff, evaluation, and manifest data per run.
- Provide `jj serve` for local review of project docs and run artifacts without leaking secrets.
- Make document-first development explicit: every product or code change starts from the relevant plan/spec/task docs and ends with docs that still match the implementation.
- Make the web UI dashboard-first: the `jj serve` home page is the operational dashboard, not a plain file listing.

## Non-Goals
- `jj` is not a cloud service.
- `jj` is not a multi-user dashboard.
- `jj` is not a general-purpose workflow DAG engine.
- `jj` does not replace git review.
- `jj` does not guarantee AI output correctness; it preserves evidence for human review.

## User Stories
- As an individual developer, I want one plan file to produce implementation-ready docs so I do not repeat context across prompts and terminals.
- As a small team, I want AI-generated changes to include spec, task, diff, and evaluation evidence so changes are easier to audit.
- As an AI workflow experimenter, I want each run stored consistently so I can compare runs and inspect provider behavior.
- As a reviewer, I want a local web UI for generated docs, manifests, diffs, and summaries so I can inspect the full run quickly.

## Functional Requirements
- `jj run <plan.md>` reads a Markdown-like plan file that exists and is non-empty after trimming whitespace.
- The input plan path is resolved relative to the shell invocation directory, not relative to `--cwd`.
- `--cwd` selects the target workspace for git metadata, run artifacts, workspace doc writes, and Codex execution.
- By default, `jj run` requires a git repository; `--allow-no-git` permits non-git execution and records that mode.
- `--dry-run` creates planning artifacts under `.jj/runs/<run-id>/` but does not write workspace `docs/SPEC.md` or `docs/TASK.md` and does not invoke implementation Codex.
- Non-dry-run writes final docs to configured workspace paths, invokes Codex with those docs as context, captures Codex evidence, captures git status/diff, and writes `docs/EVAL.md`.
- Planner provider selection order is injected planner, OpenAI planner when `OPENAI_API_KEY` is present, then Codex CLI fallback planner.
- Planning outputs are structured JSON containing agent identity, summary, spec draft, task draft, risks, assumptions, acceptance criteria, and test plan.
- Planner merge produces canonical run copies of `docs/SPEC.md` and `docs/TASK.md`.
- `jj serve --cwd .` serves README, plan files, project docs, run manifests, generated docs, summaries, evals, and diffs through a local HTTP UI.
- The `/` route for `jj serve` is a dashboard. It shows the current `docs/TASK.md` state first, then in-progress runs, recent run status, evaluation results, failed/risky runs, and links into the underlying documents and artifacts.
- The dashboard is the default starting point for review and navigation. A user should not need to open a raw artifact list first to understand current progress.
- The dashboard includes a task/status list page that shows each current TASK item, its status, and links to the related source document or run artifact.
- The dashboard includes a block diagram view of the current workflow state. Blocks represent major phases such as planning, merge, document generation, Codex execution, git capture, evaluation, review, and turn control.
- The currently active block is visually highlighted like a lit status indicator so users can identify the live phase at a glance.
- The dashboard exposes turn controls: `Continue to Next Turn` and `Finish Turn`.
- `Continue to Next Turn` means the workflow may proceed to the next turn when the current turn is complete.
- `Finish Turn` means the current turn is marked as terminal and the system must not automatically schedule, start, or navigate to a next turn.
- A successful or partially successful non-dry-run turn ends with a commit step.
- The commit step records generated or updated docs, code changes, and traceable review evidence that should be part of the repository history.
- Failed turns do not create an automatic commit; their state is preserved through `.jj/runs/<run-id>/manifest.json` and available artifacts.
- Implementation work must be document-based. Changes to behavior, CLI flags, artifacts, prompts, or web UI must update the relevant `plan.md`, `docs/SPEC.md`, `docs/TASK.md`, README, or generated run docs as part of the same work.

## CLI Behavior
- `jj run <plan.md>` supports `--cwd <path>`, `--run-id <id>`, `--dry-run`, `--allow-no-git`, `--agents <n>`, `--openai-model <model>`, `--codex-model <model>`, `--spec-doc <path>`, and `--task-doc <path>`.
- Default `--cwd` is `.`.
- Default generated workspace docs are `docs/SPEC.md` and `docs/TASK.md`.
- Default planning perspective count is 3.
- Default run id is timestamp-based with collision-resistant suffix; explicit `--run-id` must not overwrite an existing run directory.
- Dry-run output prints the run id and artifact paths for review.
- Non-dry-run output prints run id, generated doc paths, Codex result, eval path, and suggested `jj serve --cwd .` review command.
- `jj serve --cwd <path>` starts a local HTTP server and must not serve files outside the configured workspace.
- `jj serve` opens on the dashboard view at `/`. Document lists, run detail pages, and artifact pages are secondary navigation surfaces reached from the dashboard.

## Document-First Development
- `plan.md` captures product intent and workflow direction.
- `docs/SPEC.md` captures the product and technical contract that implementations must satisfy.
- `docs/TASK.md` captures concrete implementation work, current task state, expected tests, and done criteria.
- New development work begins by updating or generating the relevant docs before code changes are made.
- Code changes are complete only when the docs describe the shipped behavior and the run artifacts provide evidence for review.
- Generated `docs/SPEC.md`, `docs/TASK.md`, and `docs/EVAL.md` are first-class development artifacts, not temporary scratch files.

## Pipeline Behavior
1. Resolve config from built-in defaults, `.jjrc`, environment, then CLI flags.
2. Resolve and validate the input plan from the original invocation directory.
3. Validate target workspace and git state unless `--allow-no-git` is set.
4. Create `.jj/runs/<run-id>/` under the target workspace without overwriting existing runs.
5. Write early artifacts: `input.md`, initial manifest, and git baseline metadata when available.
6. Select planner provider by the required fallback order.
7. Run product/spec, implementation/tasking, and QA/evaluation planning perspectives.
8. Store raw planning JSON and merged planning JSON.
9. Generate final run artifacts `docs/SPEC.md` and `docs/TASK.md`.
10. For dry-run, mark manifest `dry_run` and stop before workspace writes or Codex execution.
11. For non-dry-run, write workspace docs, invoke Codex, capture events, summary, stderr, and exit code with redaction.
12. Capture post-run git status and git diff.
13. Generate `docs/EVAL.md` with checklist, implementation summary, diff summary, test result or reason not run, risks, follow-up actions, and verdict `pass`, `partial`, or `fail`.
14. For a non-dry-run turn with result `PASS` or `PARTIAL`, run a commit step at the end of the turn.
15. Record commit metadata in the run manifest or artifacts when automatic commit support is implemented.
16. For failed turns, skip the commit step and preserve failure evidence through manifest and artifacts.
17. Update `manifest.json` with final status, artifact paths, provider metadata, git metadata, Codex result, evaluation result, commit metadata when available, and errors if any.

## Artifact Layout
Each run lives at `.jj/runs/<run-id>/` and stores:
- `input.md`: exact input plan content.
- Planning JSON artifacts: raw planner outputs and merged result.
- `docs/SPEC.md`: generated final spec.
- `docs/TASK.md`: generated final implementation task.
- `docs/EVAL.md`: evaluation document for non-dry-run or failure/partial evaluation when available.
- Codex artifacts for non-dry-run: events, summary, stderr or diagnostics, and exit status.
- Git artifacts: baseline metadata, post-run status, and diff when git is available.
- `manifest.json`: canonical machine-readable run summary.

`manifest.json` includes run id, timestamps, status, input path, stored input path, workspace cwd, resolved redacted config, git metadata, planner provider and model metadata, generated doc paths, Codex result, evaluation status/path, no-git mode, and error summary when failed.

## Configuration
Configuration precedence is CLI flags, environment variables, `.jjrc`, then built-in defaults.

Supported environment variables:
- `OPENAI_API_KEY`
- `JJ_OPENAI_MODEL`
- `JJ_CODEX_BIN`
- `JJ_CODEX_MODEL`

`.jjrc` may define project defaults for output document names, model names, agent count, dry-run defaults, no-git mode, and Codex binary. Secrets must not be stored or displayed raw. Config shown in manifests, logs, errors, and served pages must be redacted.

## Error Handling
- Missing, unreadable, non-Markdown-like, or empty plan files fail with a clear message and non-zero exit code.
- Missing target cwd fails with a clear workspace error.
- Non-git workspace fails unless `--allow-no-git` is set.
- Codex fallback planner failure or missing Codex binary is reported clearly and recorded in the manifest when a run directory exists.
- OpenAI planner errors are recorded with redacted diagnostics; fallback behavior must be explicit in manifest if implemented.
- Run directory collisions fail and never overwrite existing artifacts.
- Partial planning, Markdown generation, Codex, evaluation, or git capture failures update manifest status and preserve available artifacts.
- `jj serve` rejects path traversal and avoids exposing files outside the workspace.

## Security and Privacy
- Redaction is centralized and applied to manifests, logs, planner diagnostics, provider errors, Codex stderr, served HTML, and config display.
- Redact OpenAI API keys, bearer tokens, authorization headers, `api_key`, `token`, `password`, and similar secret-like values.
- Store key presence as metadata, never raw key values.
- Served pages render Markdown safely with escaped output and redacted content.
- `jj serve` is local read-only tooling and provides no authentication feature.

## Observability
- Every run records provider choice, model metadata, config source result, git baseline, run status, timestamps, artifact paths, and final error summary when applicable.
- Non-dry-run captures Codex events/summary, exit code, redacted stderr, post-run git status, and git diff.
- Failed runs preserve the manifest and any artifacts written before failure.
- Console output remains concise and script-friendly while pointing to the run directory for details.
- The web dashboard summarizes the latest observable state: current TASK status, active or recently failed runs, latest evaluation verdicts, and recommended next actions.
- The web dashboard makes turn state observable, including whether the current turn is continuing or explicitly terminated.
- Turn observability includes commit state: pending, skipped, failed, or completed when commit support is implemented.

## Acceptance Criteria
- `jj run plan.md --dry-run` creates `.jj/runs/<run-id>/input.md`, planning JSON, `docs/SPEC.md`, `docs/TASK.md`, and `manifest.json`.
- Dry-run does not modify workspace `docs/SPEC.md` or `docs/TASK.md` and does not invoke implementation Codex.
- With no `OPENAI_API_KEY`, planning selects Codex CLI fallback or records a clear missing-Codex failure.
- Injected planner selection takes precedence over API and CLI providers in tests/internal wiring.
- Non-dry-run writes workspace spec/task docs, invokes Codex, captures Codex artifacts, captures git status/diff, writes `docs/EVAL.md`, and updates manifest.
- `--cwd` changes the target workspace but does not change plan path resolution.
- Running outside git fails by default and succeeds with `--allow-no-git`, with no-git mode recorded.
- `manifest.json` includes run status, config, git metadata, planner provider, Codex result, evaluation result, artifact paths, and errors.
- `jj serve --cwd .` exposes a dashboard-first home page, run index, and per-run artifact pages without secret leakage or path traversal.
- The dashboard makes current TASK state, in-progress work, recent failures, and evaluation status visible without requiring manual artifact inspection.
- The dashboard exposes a TASK/status list page and a block diagram where the active workflow phase is highlighted.
- Selecting `Finish Turn` prevents transition to a next turn; selecting `Continue to Next Turn` leaves next-turn progression available.
- A `PASS` or `PARTIAL` non-dry-run turn includes a final commit step before `Continue to Next Turn` can proceed or `Finish Turn` can complete.
- Failed turns do not create an automatic commit and instead remain reviewable through `.jj/runs/<run-id>/manifest.json` and artifacts.
- Any implementation that changes `jj` behavior also updates the relevant docs so development remains document-based.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
