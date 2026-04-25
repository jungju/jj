# SPEC
## Overview
`jj` is a local, document-first CLI for reproducible AI coding workflows. Given one non-empty Markdown plan, it generates implementation-ready `docs/SPEC.md` and `docs/TASK.md`, optionally runs Codex implementation, captures git and execution evidence, produces evaluation output, and exposes the current state through a dashboard-first local web UI.

The workflow is audit-oriented. `jj` records planning inputs, generated documents, Codex artifacts, git baseline/status/diff evidence, evaluation results, and a manifest under `.jj/runs/<run-id>/`. Git integration is observational by default: `jj run` must not stage, commit, reset, checkout, stash, clean, or otherwise mutate git history.

The current iteration focuses on closing remaining confidence gaps: `jj serve` must stay usable with malformed, partial, or legacy manifests; artifact serving must remain fail-closed and path-safe; Codex CLI planner fallback must be testable without `OPENAI_API_KEY`; and redaction must cover persisted artifacts and served HTML.

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
- Preserve user changes and record evidence instead of reverting, staging, committing, or hiding workspace state.

## Non-Goals
- `jj` is not a cloud service.
- `jj` is not a multi-user dashboard.
- `jj` is not a general-purpose DAG or workflow engine.
- `jj` does not replace git review; it leaves evidence that makes review easier.
- `jj` does not guarantee AI output correctness; it makes the process inspectable.
- `jj` does not expose arbitrary local files through the web UI.
- `jj run` does not create git commits by default.
- Adding an explicit commit feature is out of scope for this iteration.
- This iteration does not require live OpenAI or live Codex calls in automated tests.

## User Stories
- As an individual developer, I want to write `plan.md` once and get SPEC, TASK, implementation evidence, and evaluation artifacts without repeating context across tools.
- As a small team member, I want AI-generated changes to include requirements, task instructions, diff evidence, and evaluation notes so reviewers can understand the change.
- As an AI workflow experimenter, I want each run to be reproducible and comparable through run manifests and artifacts.
- As a user without an OpenAI API key, I want planning to still work through Codex CLI fallback.
- As a reviewer, I want `jj serve --cwd .` to open on current task and run status rather than a file listing.
- As a git user, I want `jj` to leave reviewable changes in the working tree instead of committing them automatically.
- As a dashboard user, I want old, malformed, or partial run manifests to appear as unavailable entries without breaking the whole page.

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
- Non-dry-run must capture Codex events, Codex summary, Codex exit status, final git status, git diff patch, git diff stat, and evaluation output.
- Non-dry-run must not run `git add`, `git commit`, `git reset`, `git checkout`, `git stash`, `git clean`, or any other git history-mutating command.
- Pre-existing dirty workspace state must be preserved and recorded as baseline evidence.
- Failed phases must still leave any produced artifacts and a failed or partial manifest when possible.
- `jj serve --cwd .` must serve a local dashboard at `/`.
- The dashboard must render when `.jj/runs` is empty.
- The dashboard must render when one or more run manifests are malformed, unreadable, incomplete, legacy-shaped, or reference missing artifacts.
- Malformed or legacy runs must be represented as invalid, unavailable, or degraded entries with concise redacted errors.
- Valid runs must remain usable when other runs are malformed.
- Missing, skipped, absent, or legacy commit metadata must not be treated as a current workflow failure.
- Artifact links must be generated only for paths that are safe and allowed by the manifest or explicit public workspace document allowlist.
- Artifact routes must fail closed when a run manifest is malformed, missing, or lacks the requested artifact path.
- The serve UI must render Markdown safely and block path traversal or access outside allowed workspace/run artifact paths.

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

Validation failures must identify the failed phase and reason, such as missing plan, empty plan, directory input, unsupported extension, git unavailable, run id collision, planner parse failure, document write failure, Codex failure, evaluation failure, or manifest write failure.

### `jj serve`
Supported flags:
- `--cwd <path>`: target workspace. Defaults to current directory.
- `--addr <host:port>`: local bind address. Default must be local-only.

The root route `/` must render the dashboard, not README and not a raw artifact listing. Artifact routes must validate the raw decoded request path before cleaning, joining, or normalization. Invalid artifact requests must return a non-2xx response without leaking filesystem paths or raw secret-like values.

## Pipeline Behavior
1. Resolve the positional plan path from the invocation directory.
2. Resolve and validate the target workspace from `--cwd`.
3. Validate that the plan exists, is Markdown, and contains non-whitespace content.
4. Validate git unless `--allow-no-git` is set.
5. Create `.jj/runs/<run-id>/` and required subdirectories.
6. Write `input.md` from the original plan after redaction where persisted output may expose secrets.
7. Capture git baseline metadata when available: repo root, branch, HEAD, dirty state, and status before.
8. Select the planner provider using injected, OpenAI, then Codex CLI fallback order.
9. Run planning perspectives and persist raw planning JSON with redaction applied before persistence.
10. Merge planning drafts into final run-local `docs/SPEC.md` and `docs/TASK.md`.
11. Write or update manifest state as phases complete.
12. If dry-run, skip workspace doc writes, Codex implementation, git diff after implementation, and workspace evaluation. Record evaluation as skipped or not run.
13. If non-dry-run, write workspace `docs/SPEC.md` and `docs/TASK.md` before implementation.
14. Run Codex implementation using generated SPEC and TASK.
15. Generate `docs/EVAL.md` in the run artifacts and workspace for non-dry-run.
16. Capture final git status, git diff patch, and git diff stat after docs, Codex, and evaluation have completed or failed as far as possible.
17. Do not stage, commit, reset, checkout, stash, clean, or otherwise mutate git history.
18. Finish manifest with final status, errors, artifact paths, provider metadata, Codex result, git metadata, and evaluation result.
19. When serving, load run manifests independently so one malformed manifest cannot abort the entire dashboard.
20. For malformed or untrusted manifests, render a sanitized invalid-run summary and omit trusted artifact links.

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
- `git/baseline.txt` or `git/baseline.json`
- `git/status.before.txt`
- `git/status.after.txt`
- `git/diff.patch`
- `git/diff.stat.txt` or `git/diff-summary.txt`

`manifest.json` must use artifact paths relative to the run directory and include at least:
- `run_id`, `status`, `started_at`, `ended_at` or `finished_at`
- `cwd`, `input_path` or `plan_path`, `dry_run`, `allow_no_git`
- `config` with effective non-secret settings
- `planner.provider`, `planner.model`, and planning artifact paths
- `git.available`, `git.head`, `git.branch`, `dirty_before`, `dirty_after`
- `codex.ran`, `codex.status`, `codex.model`, `codex.exit_code`, and Codex artifact paths
- `evaluation.status`, `evaluation.summary`, and risk or failure counts when available
- `artifacts`
- `errors`
- `redaction_applied`

If commit metadata exists for backward compatibility, it must report `ran:false` or `status:skipped` in newly generated default-workflow manifests. Legacy manifests may contain historical commit data, but the dashboard must not imply current default auto-commit behavior. Valid manifest statuses must distinguish dry-run completion, successful implementation/evaluation, partial failure, and failed runs.

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
- eval document path
- dry-run mode
- no-git mode
- serve bind address

Environment variables may configure OpenAI key/model and Codex binary/model. `.jjrc` may define project defaults, but secrets should not be stored there. If secret-like values are present in `.jjrc`, output and UI rendering must redact them. Commit-related config fields may remain for compatibility, but they must be ignored or rendered as skipped for the default workflow in this iteration.

## Error Handling
- Errors must include the phase that failed.
- Validation errors should fail before running planner or Codex.
- Git capture errors are fatal unless `--allow-no-git` applies.
- Planner malformed JSON or unusable output must not produce a successful empty SPEC/TASK.
- Codex failure must still capture exit status, available output, git status, manifest state, and possible evaluation information.
- Evaluation failure must not erase implementation artifacts.
- Manifest writing should use atomic write or temp-file-and-rename semantics.
- Failed runs should preserve partial artifacts and write a failed or partial manifest whenever possible.
- Lack of a commit must not be treated as an error because no commit is attempted by default.
- Serve manifest loading errors must be isolated per run and rendered as sanitized invalid-run entries.
- Artifact authorization must fail closed for malformed, missing, or untrusted manifests.

## Security and Privacy
- Raw API keys, Bearer [redacted], Authorization headers, `sk-...` style keys, and provider secrets must never appear in manifests, logs, served HTML, generated summaries, or user-facing errors.
- Redaction must be centralized and applied before persisting or rendering manifest values, config display, plan content, planner raw output, Codex output, event summaries, errors, Markdown, artifact previews, and web pages.
- Malformed-manifest errors must be redacted and must not include raw manifest snippets, absolute filesystem paths, or secret-like values.
- The web UI must restrict file access to the configured workspace and `.jj/runs` artifacts.
- Artifact routes must reject raw decoded paths containing `..` segments before path cleanup.
- Artifact routes must reject encoded traversal that decodes to `..`, absolute Unix paths, Windows drive or UNC-style paths, backslashes, NUL bytes, empty paths, and hidden path segments such as `.secret`.
- Artifact routes must serve only manifest-listed run artifacts or explicit public workspace document allowlist entries.
- After raw validation, path resolution must still verify the final resolved path remains inside the allowed root.
- Markdown rendering must be safe and must not execute raw scripts.
- The default server bind address must be local-only.

## Observability
- Every run must have a manifest that summarizes status, config, planner provider, git metadata, Codex result, evaluation result, artifacts, errors, risks, and redaction state.
- Dashboard summaries must show current TASK state, in-progress or recent runs, evaluation result, risks or failures, next action, and links to safe artifacts.
- Invalid or malformed runs must be visible as degraded entries so users can distinguish no runs from unreadable runs.
- Git evidence must include baseline and final status/diff artifacts where git is available.
- Provider fallback behavior must be observable in manifests, especially Codex fallback when `OPENAI_API_KEY` is absent.
- Test evidence must cover no default commit, malformed manifest handling, artifact path rejection, valid artifact serving, Codex fallback selection, and redaction across persisted and served outputs.

## Acceptance Criteria
- `jj run plan.md --dry-run` creates `.jj/runs/<run-id>/input.md`, planning JSON, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`.
- Dry-run does not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files.
- Without `OPENAI_API_KEY`, planner provider is Codex CLI fallback and is recorded in the manifest using deterministic test coverage with a fake Codex binary or runner.
- With `OPENAI_API_KEY`, OpenAI is selected unless an injected planner is present.
- Injected planner always has highest priority for tests and internal callers.
- Non-dry-run writes workspace SPEC/TASK before Codex implementation and then records Codex artifacts, git status/diff, `docs/EVAL.md`, and manifest evaluation result.
- Non-dry-run does not create a git commit and does not run staging or history-mutating git commands.
- A normal non-dry-run in a git repo leaves `git rev-parse HEAD` unchanged.
- Pre-existing dirty changes are preserved and recorded as baseline evidence.
- Non-git workspaces fail by default and run with `--allow-no-git` while recording git unavailable metadata.
- `--cwd` changes the target workspace but does not change positional plan path resolution.
- Planner, Codex, or evaluation failures leave a failed or partial manifest and available artifacts.
- `jj serve --cwd .` renders a dashboard at `/` with TASK state, recent run status, evaluation result, failures or risks, next actions, and safe links.
- The dashboard renders successfully with empty runs, malformed JSON manifests, incomplete manifests, legacy commit-success manifests, missing artifact references, and mixed valid/invalid runs.
- Malformed or legacy manifest runs do not expose artifact links that bypass the manifest allowlist.
- Artifact serving rejects raw or encoded traversal, absolute paths, backslashes, Windows drive paths, NUL bytes, hidden segments, and paths outside allowed roots through the real HTTP handling stack.
- Artifact serving still serves valid manifest-listed artifacts and explicit public workspace docs.
- Manifest, logs, Codex artifacts, planner artifacts, errors, and served pages do not expose raw API keys, Bearer [redacted], Authorization header values, or secret-like `.jjrc` values.
- `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass.
