# TASK
## Objective
Implement `jj` as a Go CLI that turns a single Markdown plan into auditable planning, implementation, and evaluation artifacts. The implementation must support `jj run` and `jj serve`, provider fallback, dry-run safety, secret redaction, manifest integrity, git evidence capture, and local artifact review.

## Constraints
- Keep the CLI usable without network access by supporting injected planners and fake Codex runners in tests.
- Resolve `<plan.md>` relative to the invocation directory, not `--cwd`.
- Do not write workspace docs or invoke implementation Codex during `--dry-run`.
- Require git by default and support explicit `--allow-no-git`.
- Never serialize or serve raw API keys, bearer tokens, authorization headers, or token-like config values.
- Never overwrite an existing `.jj/runs/<run-id>/` directory.
- Use safe path handling for artifact writes and `jj serve`; do not serve outside the workspace.
- Prefer existing project patterns and dependencies; use Go standard library where sufficient.
- Treat documents as the source of development truth. Before implementing behavior changes, update or generate the relevant `plan.md`, `docs/SPEC.md`, `docs/TASK.md`, README, or run docs; after implementation, verify those docs still match the actual behavior.
- Treat the web UI as dashboard-first. The first screen for `jj serve` must show current TASK state and active progress before secondary document or artifact navigation.

## Implementation Steps
1. Inspect the existing Go module, CLI entrypoint, package structure, tests, and dependencies.
2. Define or update package boundaries: `cmd/jj`, `internal/cli`, `internal/config`, `internal/run`, `internal/planner`, `internal/codex`, `internal/gitutil`, `internal/artifact`, `internal/eval`, and `internal/serve` as appropriate for the current repo.
3. Implement config resolution from defaults, `.jjrc`, environment, and CLI flags with the required precedence.
4. Implement CLI parsing for `jj run <plan.md>` and `jj serve --cwd <path>` with flags for cwd, run id, dry-run, allow-no-git, agents, OpenAI model, Codex model, spec doc, and task doc.
5. Implement plan loading and validation, including relative path behavior, Markdown-like extension checks, missing file errors, and empty file rejection.
6. Implement git utilities for repo detection, repo root, branch, commit SHA, dirty state, baseline metadata, post-run status, and diff capture, with no-git metadata support.
7. Implement run id generation using UTC timestamp plus short random suffix, with explicit override and collision failure.
8. Implement artifact store creation under `.jj/runs/<run-id>/`, atomic writes where practical, stable relative paths, early `input.md`, planning JSON, docs, git artifacts, Codex artifacts, eval, and manifest persistence.
9. Define planner interfaces and JSON schema with `agent`, `summary`, `spec_markdown`, `task_markdown`, `risks`, `assumptions`, `acceptance_criteria`, and `test_plan`.
10. Implement planner provider selection: injected planner, OpenAI planner when `OPENAI_API_KEY` exists, Codex CLI fallback otherwise.
11. Implement OpenAI planner behind an interface with model selection and redacted diagnostics.
12. Implement Codex fallback planner by invoking the configured Codex binary, prompting for one JSON object, extracting/parsing structured output, and writing useful failure artifacts.
13. Implement planner merge logic that produces final SPEC and TASK Markdown using the required section layouts.
14. Implement dry-run finalization: write only run artifacts, mark manifest `dry_run`, print artifact paths, and skip workspace doc writes and Codex implementation.
15. Implement non-dry-run execution: write configured workspace docs, invoke Codex with generated docs, capture events/summary/stderr/exit code, capture git status and diff, and generate `docs/EVAL.md`.
16. Implement deterministic evaluation generation with plan summary, SPEC/TASK checklist, Codex result, git diff summary, test result or reason not run, risks, follow-up actions, and verdict.
17. Implement centralized redaction and use it for manifests, logs, provider errors, Codex output, served HTML, and config display.
18. Implement `jj serve` with `net/http`, dashboard home, run index, per-run artifact navigation, Markdown rendering, redaction, and path traversal rejection.
19. Make the dashboard the `/` route. It must summarize the current `docs/TASK.md`, in-progress runs, recent run status, evaluation verdicts, failed/risky runs, and recommended next actions.
20. Ensure document lists, run detail pages, and artifact pages remain reachable from the dashboard but are not the primary first screen.
21. Ensure failed runs update manifest status and error summary whenever a run directory has been initialized.
22. Run formatting and verification commands, then fix regressions.

## Files and Packages to Inspect
- `go.mod` and `go.sum`
- `cmd/jj`
- Existing `internal/cli` or command packages
- Existing config loading code and `.jjrc` handling
- Existing planner, Codex, git, artifact, eval, and server packages if present
- Existing tests and test helpers
- README and docs for current CLI behavior
- Any existing manifest schema or run artifact fixtures

## Required Changes
- Add or update CLI commands and flags for `run` and `serve`.
- Add resolved config struct with source precedence and redacted serialization.
- Add run orchestration for plan validation, git baseline, artifact initialization, planner execution, merge, dry-run branching, non-dry-run Codex execution, evaluation, and manifest finalization.
- Add planner provider chain and test injection hooks.
- Add Codex CLI execution/fallback planning wrappers with redacted output capture.
- Add artifact layout and manifest contract under `.jj/runs/<run-id>/`.
- Add git metadata and diff/status capture.
- Add deterministic `docs/EVAL.md` generation.
- Add centralized secret redaction utility.
- Add local HTTP serving for a dashboard-first home page, README, plan files, project docs, run index, manifests, SPEC, TASK, EVAL, summaries, and diffs.
- Add dashboard state extraction from `docs/TASK.md`, run manifests, evaluation artifacts, and active/failed run metadata.
- Add or update docs whenever behavior changes, so implementation and product intent remain traceable through `plan.md`, `docs/SPEC.md`, `docs/TASK.md`, and README.

## Web Dashboard Status and Turn Control

Implement a dashboard-centered web UI that makes the current work state visible before users inspect raw artifacts.

Required behavior:

1. Show the current `TASK` and execution situation on the web UI dashboard.
2. Add a TASK/status list page that displays each current task item, status, related run id when available, and links to source docs or artifacts.
3. Represent the current workflow situation as a block diagram.
4. Use blocks for major phases such as planning, merge, document generation, Codex execution, git capture, evaluation, review, commit, and turn control.
5. Visually highlight the active block like a lit status indicator so the user can immediately see the current state.
6. Provide `Continue to Next Turn` and `Finish Turn` controls in the dashboard.
7. Define `Continue to Next Turn` as the state where the current turn may finish its commit step and the workflow may proceed to the next turn.
8. Define `Finish Turn` as the state where the current turn finishes its commit step, is marked as terminal, and the workflow must not automatically create, start, or navigate to the next turn.
9. Add a final commit step for successful or partially successful non-dry-run turns.
10. Do not create an automatic commit for failed turns; preserve those through `.jj/runs/<run-id>/manifest.json` and artifacts.
11. Persist commit state and selected turn state in the run artifact or manifest model when implementation work adds the necessary data structure.
12. Keep the UI local-first and safe: turn controls must not execute hidden shell commands or mutate files outside the configured workspace.

Acceptance criteria:

- The dashboard shows a TASK 상태 리스트 before lower-level artifact lists.
- The dashboard includes a block diagram of the current situation.
- The active phase has a clear 활성 상태 표시.
- `Continue to Next Turn` and `Finish Turn` are visible controls with distinct states.
- When `Finish Turn` is selected, the current turn completes the commit step if eligible and the UI/backend do not proceed to a next turn.
- The block diagram includes a commit block.
- Commit result is recorded in run artifacts or manifest when commit support is implemented.
- The behavior is covered by HTTP handler tests and documented in `docs/SPEC.md`.

## Testing Requirements
- Unit test config precedence across defaults, `.jjrc`, env vars, and CLI flags.
- Unit test redaction for OpenAI-style keys, bearer tokens, authorization headers, `api_key`, `token`, and `password` values.
- Unit test plan loading for relative paths, absolute paths, empty files, missing files, Markdown-like validation, and `--cwd` combinations.
- Unit test planner selection for injected planner, OpenAI key present, OpenAI key absent fallback, and missing Codex binary error.
- Unit test dry-run to assert workspace docs are not written and Codex runner is not called.
- Unit test artifact writes, run id collision behavior, and manifest redaction.
- Integration test dry-run in a temporary git repo and assert run artifacts and manifest fields.
- Integration test dry-run with `--cwd` and a plan path outside cwd.
- Integration test no-git rejection and `--allow-no-git` success.
- Integration test non-dry-run with fake planner and fake Codex runner, asserting workspace docs, Codex artifacts, git status/diff, EVAL, and manifest.
- Integration test dirty workspace baseline handling so pre-existing changes remain visible.
- HTTP tests for `jj serve` run index, Markdown rendering, artifact pages, redaction, and path traversal rejection.
- HTTP tests for the dashboard home page showing TASK summary, in-progress or recent runs, evaluation status, failed run indicators, and next-action links.
- HTTP tests for the TASK/status list page, block diagram rendering, active block highlight, `Continue to Next Turn`, and `Finish Turn`.
- State tests confirming `Finish Turn` blocks next-turn progression and `Continue to Next Turn` leaves next-turn progression available.
- State tests confirming eligible `PASS` and `PARTIAL` non-dry-run turns include a final commit step.
- State tests confirming failed turns skip automatic commit and preserve failure evidence in manifest/artifacts.
- Manifest schema should be protected with structure assertions or snapshot-style tests.

## Manual Verification
- Run `go test ./...`.
- Run `go vet ./...`.
- Run `go build -o jj ./cmd/jj`.
- In a temporary git repo, run `./jj run plan.md --dry-run` and confirm `.jj/runs/<run-id>/` contains `input.md`, planning JSON, generated SPEC/TASK, and manifest while workspace docs remain unchanged.
- Run without `OPENAI_API_KEY` and confirm Codex CLI fallback selection or clear missing-Codex failure is recorded.
- Run a fake or controlled non-dry-run path and confirm Codex artifacts, git status/diff, EVAL, and final manifest are present.
- Start `./jj serve --cwd .` and inspect README/docs/run artifacts while confirming secret fixtures are redacted.
- Confirm the first `jj serve` screen is a dashboard and not only a raw document list.
- Confirm the dashboard shows current TASK status, running/recent runs, latest evaluation status, failed/risky items, and links to docs and artifacts.
- Confirm the dashboard shows a TASK 상태 리스트 page, a block diagram, a lit active-state block, and `Continue to Next Turn`/`Finish Turn` controls.
- Confirm the block diagram includes a commit block.
- Confirm selecting `Finish Turn` completes the eligible commit step and prevents movement into the next turn.
- Confirm failed turns do not create an automatic commit and remain reviewable through manifest/artifacts.

## Done Criteria
- Required SPEC and TASK documents are generated into run artifacts for dry-run and also written to workspace docs for non-dry-run.
- Planner provider fallback order is implemented and recorded in manifest.
- Dry-run is harmless to workspace docs and skips Codex implementation.
- Non-dry-run captures Codex evidence, git status/diff, EVAL, and manifest results.
- No raw secrets appear in manifests, logs, errors, Codex diagnostics, or served HTML in tests.
- `jj serve` provides a dashboard-first home page, expected artifact navigation, and path traversal blocking.
- The dashboard exposes current TASK state, progress, recent results, and next actions before lower-level artifact lists.
- The dashboard exposes TASK/status list, block diagram, active-state highlighting, and turn controls.
- Eligible `PASS` and `PARTIAL` non-dry-run turns include a final commit step.
- Failed turns skip automatic commit and preserve their state through `.jj/runs/<run-id>/manifest.json` and artifacts.
- `Finish Turn` reliably stops next-turn progression after the eligible commit step, while `Continue to Next Turn` allows the workflow to remain eligible for a next turn after the eligible commit step.
- All behavior changes are reflected in the relevant documents before the work is considered done.
- Git-required and `--allow-no-git` behavior are covered.
- All required tests and verification commands pass.
