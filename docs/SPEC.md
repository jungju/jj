# SPEC
## Overview
`jj` is a local CLI for making AI coding workflows reproducible and reviewable. Given one non-empty Markdown planning file, it generates implementation-ready `docs/SPEC.md` and `docs/TASK.md`, optionally runs Codex implementation, captures git and execution evidence, produces evaluation output, and exposes the current state through a local dashboard.

The product treats documents as the source of truth: implementation starts from the generated documents, and completion requires the documents, code behavior, artifacts, and evaluation evidence to agree.

## Goals
- Generate concrete `docs/SPEC.md` and `docs/TASK.md` from one Markdown plan.
- Use multiple planning perspectives: product/spec, implementation/tasking, and QA/evaluation.
- Run without `OPENAI_API_KEY` by falling back to Codex CLI planning.
- Run Codex implementation in the target workspace for non-dry-run executions.
- Store every run under `.jj/runs/<run-id>/` for audit, comparison, and debugging.
- Capture Codex events, summaries, git status, git diff, evaluation output, and manifest metadata.
- Provide `jj serve` as a dashboard-first local web UI showing current TASK state, recent runs, evaluation state, risks, and next actions.
- Keep secrets out of manifests, logs, artifacts shown in the UI, and generated web pages.
- Preserve user changes and record evidence instead of reverting or hiding workspace state.

## Non-Goals
- `jj` is not a cloud service.
- `jj` is not a multi-user dashboard.
- `jj` is not a general-purpose DAG or workflow engine.
- `jj` does not replace git review; it leaves evidence that makes review easier.
- `jj` does not guarantee AI output correctness; it makes the process inspectable.
- `jj` does not expose arbitrary local files through the web UI.

## User Stories
- As an individual developer, I want to write `plan.md` once and get SPEC, TASK, implementation evidence, and evaluation artifacts without repeating context across tools.
- As a small team member, I want AI-generated changes to include requirements, task instructions, diff evidence, and evaluation notes so reviewers can understand the change.
- As an AI workflow experimenter, I want each run to be reproducible and comparable through run manifests and artifacts.
- As a user without an OpenAI API key, I want planning to still work through Codex CLI fallback.
- As a reviewer, I want `jj serve --cwd .` to open on current task and run status rather than a file listing.

## Functional Requirements
- `jj run <plan.md>` must read an existing, non-empty Markdown plan file.
- The positional plan path must be resolved relative to the shell invocation directory, not relative to `--cwd`.
- `--cwd` must select the target workspace where `.jj/runs`, workspace docs, Codex execution, git capture, and serve content are rooted.
- By default the target workspace must be a git repository.
- `--allow-no-git` must allow non-git workspaces and record `git.available=false` in the manifest.
- `--run-id` must select the run directory name and fail if that run already exists.
- If `--run-id` is omitted, `jj` must generate a unique time-based run id.
- Planner provider selection order must be: injected planner, OpenAI planner when `OPENAI_API_KEY` is present, Codex CLI fallback planner when no API key is present.
- Planning output must be structured and persisted as raw planning artifacts.
- Drafts must be merged into final run-local `docs/SPEC.md` and `docs/TASK.md` for every successful planning run.
- Dry-run must not write workspace `docs/SPEC.md`, workspace `docs/TASK.md`, workspace `docs/EVAL.md`, or code files.
- Dry-run must not invoke implementation Codex.
- Non-dry-run must write workspace SPEC and TASK before running implementation Codex.
- Non-dry-run must capture Codex events, Codex summary, Codex exit status, final git status, git diff, and evaluation output.
- Failed phases must still leave any produced artifacts and a failed or partial manifest when possible.
- `jj serve --cwd .` must serve a local dashboard at `/`.
- The serve UI must render Markdown safely and block path traversal or access outside allowed workspace/run artifact paths.

## CLI Behavior
### `jj run <plan.md>`
Supported flags:
- `--cwd <path>`: target workspace. Defaults to current directory.
- `--run-id <id>`: explicit run id. Existing run directory is an error.
- `--dry-run`: generate planning artifacts only.
- `--allow-no-git`: permit execution outside a git repository.
- `--planner-agents <n>` or `--planning-agents <n>`: planning perspective/fanout count. Default is 3.
- `--openai-model <model>`: OpenAI planner model override.
- `--codex-model <model>`: Codex fallback and implementation model override.
- `--spec-doc <path>`: workspace spec document path. Default `docs/SPEC.md`.
- `--task-doc <path>`: workspace task document path. Default `docs/TASK.md`.

Validation failures must identify the failed phase and reason, such as missing plan, empty plan, directory input, unsupported extension, git unavailable, run id collision, planner parse failure, document write failure, Codex failure, evaluation failure, or manifest write failure.

### `jj serve`
Supported flags:
- `--cwd <path>`: target workspace. Defaults to current directory.
- `--addr <host:port>`: local bind address. Default must be local-only.

The root route `/` must render the dashboard, not README and not a raw artifact listing.

## Pipeline Behavior
1. Resolve the positional plan path from the invocation directory.
2. Resolve and validate the target workspace from `--cwd`.
3. Validate that the plan exists, is Markdown, and contains non-whitespace content.
4. Validate git unless `--allow-no-git` is set.
5. Create `.jj/runs/<run-id>/` and required subdirectories.
6. Write `input.md` from the original plan.
7. Capture git baseline metadata when available: repo root, branch, HEAD, dirty state, and status before.
8. Select the planner provider using injected, OpenAI, then Codex CLI fallback order.
9. Run planning perspectives and persist raw planning JSON.
10. Merge planning drafts into final run-local `docs/SPEC.md` and `docs/TASK.md`.
11. Write or update manifest state as phases complete.
12. If dry-run, skip workspace doc writes, Codex implementation, git diff after implementation, and workspace evaluation. Record evaluation as skipped or not run.
13. If non-dry-run, write workspace `docs/SPEC.md` and `docs/TASK.md` before implementation.
14. Run Codex implementation using generated SPEC and TASK.
15. Capture Codex artifacts, final git status, git diff, and diff stat.
16. Generate `docs/EVAL.md` in the run artifacts and workspace for non-dry-run.
17. Finish manifest with final status, errors, artifact paths, provider metadata, Codex result, git metadata, and evaluation result.

## Artifact Layout
Each run is stored under `.jj/runs/<run-id>/`.

Required planning artifacts:
- `input.md`
- `planning/product_spec.json`
- `planning/implementation_task.json`
- `planning/qa_eval.json`
- `planning/merged.json`
- `docs/SPEC.md`
- `docs/TASK.md`
- `manifest.json`

Required non-dry-run artifacts when available:
- `docs/EVAL.md`
- `codex/events.jsonl`
- `codex/summary.md`
- `codex/exit.json`
- `git/baseline.txt`
- `git/status.before.txt`
- `git/status.after.txt`
- `git/diff.patch`
- `git/diff.stat.txt`

`manifest.json` must use artifact paths relative to the run directory and include at least:
- `run_id`, `status`, `started_at`, `ended_at`
- `cwd`, `input_path`, `dry_run`, `allow_no_git`
- `config` with effective non-secret settings
- `planner.provider`, `planner.model`, and planning artifact paths
- `git.available`, `git.head`, `git.branch`, `dirty_before`, `dirty_after`
- `codex.ran`, `codex.status`, `codex.model`, `codex.exit_code`, and Codex artifact paths
- `evaluation.status`, `evaluation.summary`, risk or failure counts when available
- `artifacts`
- `errors`
- `redaction_applied`

Valid manifest statuses must distinguish dry-run completion, successful implementation/evaluation, partial failure, and failed runs.

## Configuration
Configuration sources must be merged with deterministic precedence: CLI flags, environment variables, `.jjrc`, then defaults.

Configurable values include:
- target workspace cwd
- run id
- planner agent count
- OpenAI model
- Codex binary path
- Codex model
- spec document path
- task document path
- dry-run mode
- no-git mode
- serve bind address

Environment variables may configure OpenAI key/model and Codex binary/model. `.jjrc` may define project defaults, but secrets should not be stored there. If secret-like values are present in `.jjrc`, output and UI rendering must redact them.

## Error Handling
- Errors must include the phase that failed.
- Validation errors should fail before running planner or Codex.
- Git capture errors are fatal unless `--allow-no-git` applies.
- Planner malformed JSON or unusable output must not produce a successful empty SPEC/TASK.
- Codex failure must still capture exit status, available output, git status, manifest state, and possible evaluation information.
- Evaluation failure must not erase implementation artifacts.
- Manifest writing should use atomic write or temp-file-and-rename semantics.
- Failed runs should preserve partial artifacts and write a failed or partial manifest whenever possible.

## Security and Privacy
- Raw API keys, Bearer [redacted], Authorization headers, `sk-...` style keys, and provider secrets must never appear in manifests, logs, served HTML, generated summaries, or user-facing errors.
- Redaction must be centralized and applied before persisting or rendering manifest values, config display, planner raw output, Codex output, event summaries, errors, and web pages.
- The web UI must restrict file access to the configured workspace and `.jj/runs` artifacts.
- Artifact routes must block `../`, absolute paths, hidden secret targets, and paths outside the allowed roots.
- Markdown rendering must be safe and must not execute raw scripts.
- The default server bind address must be local-only.

## Observability
- Every run must have a manifest that summarizes status, configuration, provider choice, git metadata, Codex result, evaluation result, artifact paths, and errors.
- Git evidence must capture baseline and post-run state without reverting existing user changes.
- Codex evidence must include events when available, summary, exit status, model, and duration when available.
- Evaluation must summarize plan compliance, generated document presence, Codex result, git diff summary, test results, remaining risks, and next actions.
- `jj serve` must surface current TASK status, active or recent runs, evaluation status, failures, risks, and next actions on the first screen.

## Acceptance Criteria
- `jj run plan.md --dry-run` creates `.jj/runs/<run-id>/input.md`, planning JSON, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`.
- Dry-run does not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files.
- Without `OPENAI_API_KEY`, planner provider is Codex CLI fallback and is recorded in the manifest.
- With `OPENAI_API_KEY`, OpenAI is selected unless an injected planner is present.
- Injected planner always has highest priority for tests and internal callers.
- Non-dry-run writes workspace SPEC/TASK before Codex implementation and then records Codex artifacts, git status/diff, `docs/EVAL.md`, and manifest evaluation result.
- Non-git workspaces fail by default and run with `--allow-no-git` while recording git unavailable metadata.
- `--cwd` changes the target workspace but does not change positional plan path resolution.
- Planner, Codex, or evaluation failures leave a failed or partial manifest and available artifacts.
- `jj serve --cwd .` renders a dashboard at `/` with TASK state, recent run status, evaluation result, failures/risks, next actions, and links to README, plan, docs, runs, manifest, Codex summary, and git diff.
- Manifest, logs, Codex artifacts, planner artifacts, and served pages do not expose raw API keys, Bearer [redacted], or Authorization header values.
- Artifact serving rejects path traversal and paths outside allowed roots.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
