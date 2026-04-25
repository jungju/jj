# SPEC
## Overview
`jj` is a document-first CLI for reproducible AI coding workflows. A run starts from one Markdown plan file, generates implementation-ready `docs/SPEC.md` and `docs/TASK.md`, optionally invokes Codex to implement the work, captures evidence, evaluates the result, and exposes the current state through a local dashboard.

All run outputs are stored under `.jj/runs/<run-id>/` so users can compare runs, audit AI-generated changes, and verify whether implementation matches the original intent.

## Goals
- Generate concrete `docs/SPEC.md` and `docs/TASK.md` from a single non-empty Markdown plan.
- Use multiple planning perspectives: product/spec, implementation/tasking, and QA/evaluation.
- Support OpenAI planning when `OPENAI_API_KEY` is configured and Codex CLI fallback planning when it is not.
- Connect generated documents to Codex implementation in the current workspace.
- Capture Codex evidence, git status, git diff, evaluation output, and manifest metadata for each run.
- Make `jj serve --cwd .` dashboard-first, showing TASK state and run progress before file browsing.
- Keep document updates and implementation behavior aligned as the completion standard.
- Avoid persisting or rendering secrets in config, manifests, logs, docs, events, or served HTML.

## Non-Goals
- `jj` is not a cloud service.
- `jj` is not a multi-user dashboard.
- `jj` is not a general-purpose DAG workflow engine.
- `jj` does not replace human git review.
- `jj` does not guarantee AI output correctness; it makes the process auditable.
- `jj serve` does not require authentication, cloud sync, or collaborative permissions.

## User Stories
- As an individual developer, I want to turn `plan.md` into SPEC/TASK docs so I can start implementation from stable instructions.
- As a Codex user, I want implementation, diff, and evaluation evidence saved together so I can inspect what happened after a run.
- As a small team member, I want generated artifacts and manifest metadata so AI-assisted changes are reviewable.
- As an experimenter, I want repeatable runs with provider and config metadata so I can compare planning and implementation outcomes.
- As a local dashboard user, I want the first screen to show current TASK status, recent runs, evaluation state, risks, and next actions.

## Functional Requirements
- `jj run <plan.md>` must read an existing, non-empty Markdown file.
- `--cwd` must select the target workspace without changing how a relative plan path is resolved.
- By default, the target workspace must be inside a git repository.
- `--allow-no-git` must permit execution outside git and record no-git mode in the manifest.
- `--dry-run` must create planning artifacts under `.jj/runs/<run-id>/` and must not write workspace `docs/SPEC.md`, workspace `docs/TASK.md`, or invoke Codex implementation.
- Non-dry-run must write generated SPEC/TASK docs to the workspace before invoking Codex.
- Planner provider resolution order must be injected planner, OpenAI planner when `OPENAI_API_KEY` exists, then Codex CLI fallback planner.
- Each run must preserve raw planning outputs from product/spec, implementation/tasking, and QA/evaluation perspectives.
- Final merged `docs/SPEC.md` and `docs/TASK.md` must be written into the run artifact directory for all runs.
- Non-dry-run must capture Codex events, Codex summary when available, git status, git diff, evaluation output, and manifest updates.
- `docs/EVAL.md` must evaluate the plan, SPEC, TASK, implementation or dry-run evidence, test results, remaining risks, and next action.
- `jj serve --cwd .` must serve a dashboard at the root path and provide navigation to README, `plan.md`, project docs, run artifacts, SPEC, TASK, EVAL, git diff, events, and manifest.

## CLI Behavior
- Commands: `jj run <plan.md>` and `jj serve`.
- Run flags include `--cwd`, `--run-id`, `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, `--task-doc`, `--allow-no-git`, and `--dry-run`.
- Serve flags include `--cwd` and address or port configuration.
- CLI config precedence should be defaults, `.jjrc`, environment variables, then CLI flags, with CLI flags taking precedence.
- Invalid input such as missing files, empty files, directories, unreadable files, and non-Markdown inputs must return a clear non-zero error.
- Git absence must fail unless `--allow-no-git` is provided.
- Skipped steps must be represented explicitly in the manifest instead of omitted.

## Pipeline Behavior
- Create a run directory before work starts.
- Copy the input plan to `input.md`.
- Capture git baseline metadata when git is available.
- Resolve and execute the planner provider according to the provider order.
- Generate raw planning outputs and merge them into final SPEC/TASK documents.
- In dry-run mode, stop after planning artifacts, optional evaluation where possible, and manifest finalization.
- In non-dry-run mode, write workspace docs, invoke Codex implementation using generated docs, capture Codex evidence, capture post-run git status and diff, run evaluation, and update manifest status.
- Run status must distinguish success, partial failure, planner failure, Codex failure, evaluation failure, and artifact/write failure where possible.

## Artifact Layout
Each run directory `.jj/runs/<run-id>/` should contain:

- `input.md`: exact copied user plan.
- `planning/`: raw planning JSON or equivalent outputs from product, implementation, and QA perspectives.
- `docs/SPEC.md`: final merged specification.
- `docs/TASK.md`: final merged implementation task document.
- `docs/EVAL.md`: evaluation output when generated.
- `codex/events.jsonl`: Codex event stream when implementation runs.
- `codex/summary.md`: Codex execution summary when available.
- `git/status.txt`: captured git status after execution or skipped marker.
- `git/diff.patch`: captured git diff after execution or skipped marker.
- `manifest.json`: machine-readable run metadata, config, paths, status, provider, git metadata, Codex result, evaluation result, timestamps, errors, skipped steps, and redacted environment summary.

## Configuration
- `.jjrc` may define project defaults for cwd, run id behavior, planning agent count, OpenAI model, Codex model, document names, no-git mode, dry-run behavior, Codex binary, and serve address.
- Environment variables may provide OpenAI and Codex defaults, including API key presence and model names.
- CLI flags must override `.jjrc` and environment-derived defaults.
- Effective config must be recorded in the manifest with sensitive values redacted.
- The Codex binary and models must be configurable so tests and local environments can use fakes or alternate binaries.

## Error Handling
- Input validation errors must fail fast with actionable messages.
- If git metadata is required but unavailable, the run must fail unless `--allow-no-git` is set.
- Planner, Codex, evaluation, and artifact write failures must be recorded separately in the manifest.
- Partial artifacts must not be hidden; status and error summaries must describe what completed, failed, or was skipped.
- Dashboard rendering must tolerate missing files, corrupt manifests, empty `.jj/runs`, and degraded runs without returning a server-wide 500.

## Security and Privacy
- Never persist or render API keys, Bearer [redacted], authorization headers, password-like values, or token/key values from `.jjrc` or environment variables.
- Redaction must apply to manifests, logs, planner outputs, generated docs, Codex events, evaluation docs, and served HTML.
- Render Markdown safely for local use by escaping raw HTML and applying redaction before display.
- The dashboard must only display redacted config and artifact content.

## Observability
- `manifest.json` must provide a concise overview of run status, timestamps, cwd, plan path, dry-run flag, no-git flag, planner provider, models, git baseline, artifact paths, Codex result, evaluation result, skipped steps, and errors.
- Git capture must include branch, commit when available, dirty state, status, and diff after implementation.
- Codex capture must include event stream and summary when implementation runs.
- Evaluation must identify fulfilled criteria, failed criteria, skipped steps, unresolved risks, and recommended next action.
- The dashboard must surface current TASK status, active or latest run, recent run statuses, evaluation state, failures, risks, and next actions.

## Acceptance Criteria
- `jj run plan.md --dry-run` creates `.jj/runs/<run-id>/input.md`, planning outputs, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json` without modifying workspace docs.
- Dry-run records implementation/Codex as skipped and does not invoke Codex.
- `OPENAI_API_KEY` absence selects Codex CLI fallback planning instead of failing solely due to missing API key.
- `OPENAI_API_KEY` presence selects the OpenAI planner unless an injected planner is supplied.
- No-git workspaces fail by default and succeed only with `--allow-no-git`, with no-git mode recorded.
- Non-dry-run writes workspace `docs/SPEC.md` and `docs/TASK.md`, runs Codex, captures Codex evidence, captures git status and diff, writes `docs/EVAL.md`, and updates the manifest.
- `manifest.json` contains run status, config, git metadata, planner provider, Codex result, evaluation result, artifact paths, skipped steps, and redacted environment/config fields.
- `jj serve --cwd .` opens a dashboard-first root view showing TASK state, run progress or latest run, recent statuses, evaluation result, risks, failures, and next actions.
- The web UI links to README, plan, project docs, SPEC, TASK, EVAL, manifest, git diff, events, and run artifacts.
- Broken run artifacts do not crash the dashboard.
- No real API key, Bearer [redacted], authorization header, or obvious secret value appears in artifacts or served pages.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
