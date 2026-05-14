# jj Product Requirements Document

Last updated: 2026-05-10

## 1. Summary

`jj` is a local, document-first AI coding workflow CLI. It turns a product seed such as `plan.md` into canonical JSON product state, implementation-ready tasks, Codex execution, deterministic validation, and auditable run artifacts.

The product promise is simple: a developer should be able to state intent once, let `jj` plan and execute one bounded task, then inspect exactly what happened through local files and a dashboard without exposing secrets or unsafe workspace paths.

## 2. Problem

AI-assisted coding sessions often scatter context across prompts, terminal output, generated files, git diffs, and validation logs. This creates several recurring problems:

- Users repeat product intent because there is no durable source of truth.
- AI-generated changes are difficult to audit after the session ends.
- It is unclear which task was selected, why it was selected, and whether it passed validation.
- Planning artifacts, command output, diffs, and local dashboard views can accidentally expose secrets or private paths.
- Dry-run planning and full execution can diverge, making previews less trustworthy.

`jj` solves this by making the workflow stateful, local, auditable, and validation-gated.

## 3. Target Users

- Individual developers who use Codex and want a repeatable plan-to-code loop.
- Small teams that need reviewable evidence for AI-authored changes.
- Maintainers experimenting with autonomous or semi-autonomous coding workflows.
- Security-conscious users who need local execution, redacted artifacts, and narrow dashboard access.

## 4. Goals

- Provide a single CLI flow from product intent to planned task, implementation, validation, and evidence capture.
- Treat `.jj/spec.json`, `.jj/tasks.json`, and `.jj/runs/<run-id>/` as canonical runtime state.
- Keep `plan.md` as the initial product seed and later background vision once `.jj/spec.json` exists.
- Select one bounded runnable task per full run and preserve append-only task history.
- Update `.jj/spec.json` only after validation succeeds.
- Store redacted, reviewable run evidence under `.jj/runs/<run-id>/`.
- Mirror redacted `.jj/` documents into `.jj/documents.sqlite3` for local document history without replacing the canonical JSON state files.
- Provide a local dashboard-first `jj serve` experience for SPEC, tasks, validation, runs, risks, failures, and artifacts.
- Apply shared redaction and workspace boundary guardrails before persistence, model handoff, CLI output, or dashboard rendering.
- Keep validation deterministic and independent of live model output.

## 5. Non-Goals

- `jj` is not a hosted cloud service.
- `jj` is not a multi-user dashboard or authentication system.
- `jj` is not a general-purpose workflow engine.
- `jj` does not replace human git review.
- `jj` does not guarantee AI output correctness.
- `jj` does not serve arbitrary workspace files through the dashboard.
- `jj` does not treat `docs/` Markdown files as canonical runtime state.
- `jj` does not use `.jj/documents.sqlite3` as the planning source of truth.

## 6. Product Principles

- Document-first: product direction starts from written intent and remains inspectable.
- Local-first: source code, state, artifacts, and dashboard are local by default.
- Validation-gated: successful validation is the completion signal.
- Evidence-rich: each run should leave enough sanitized evidence to understand what changed and why.
- Fail closed: unsafe paths, secret-looking values, traversal attempts, and malformed run IDs should be rejected or rendered as denied/unavailable states.
- Behavior preserving: refactors and UI polish must not change routes, schemas, security policy, validation behavior, or user-facing data semantics unless explicitly scoped.

## 7. Core Workflow

1. User writes or updates `plan.md` with product intent.
2. User runs `jj run plan.md --dry-run` to preview planning artifacts without mutating workspace state.
3. User runs `jj run plan.md` for a full turn.
4. `jj` resolves the workspace, validates the plan path, redacts inputs, and captures baseline git evidence.
5. Planner reads current `.jj/spec.json` when present, task history, recent run evidence, next-turn intent, and `plan.md` background.
6. Planner appends a fresh task batch to `.jj/tasks.json`.
7. Full run selects the first newly proposed runnable task and invokes the implementation provider, Codex CLI by default.
8. `jj` captures Codex evidence, git evidence, validation results, events, and manifest metadata under `.jj/runs/<run-id>/`.
9. If validation passes, `jj` marks the selected task done and reconciles `.jj/spec.json`.
10. If validation fails, is skipped, or is missing, prior `.jj/spec.json` remains unchanged.
11. User opens `jj serve --cwd .` to inspect current state, recent runs, validation status, risks, failures, and guarded artifacts.

## 8. Functional Requirements

### 8.1 CLI Run

- `jj run <plan.md>` must read a non-empty Markdown plan inside the resolved workspace boundary.
- Relative plan paths must continue to resolve from the invocation directory, then be validated inside `--cwd`.
- `--dry-run` must write planning artifacts and state snapshots only under `.jj/runs/<run-id>/`.
- Full runs must append `.jj/tasks.json` during planning, run implementation, execute validation, then reconcile `.jj/spec.json` only on validation success.
- When the workspace starts clean and validation passes, `jj` should create a local commit containing source changes plus `.jj/spec.json` and `.jj/tasks.json`.
- When the workspace starts dirty, `jj` should skip auto-commit and leave changes reviewable.

### 8.2 Planning And Task Selection

- Planner input must prioritize `.jj/next-intent.md` when it is non-empty.
- When `.jj/spec.json` exists, it must be the planning source of truth.
- `.jj/tasks.json` must remain append-only task proposal history.
- Each run must append new task IDs rather than replacing prior task records.
- Full runs must select one newly proposed runnable task.
- Previous `active` or `in_progress` tasks must return to `queued` when a new full-run task is selected.
- `task-proposal-mode` must act as a category hint unless next-turn intent overrides the planning direction.

### 8.3 Implementation Provider

- Codex CLI must be the default implementation provider.
- Planning may use OpenAI API when `OPENAI_API_KEY` is available.
- If API access is unavailable, planning should support Codex CLI fallback.
- Provider commands must use structured argv/env handling with timeouts and sanitized command metadata.

### 8.4 Validation

- `./scripts/validate.sh` must remain the documented release validation gate.
- Validation must not require live OpenAI API access, real Codex execution, GitHub network access, or nondeterministic model output.
- Validation output persisted by `jj` must be sanitized and summarized.
- Failed validation must block SPEC reconciliation.

### 8.5 State And Artifacts

- Canonical runtime state must be JSON-only:
  - `.jj/spec.json`
  - `.jj/tasks.json`
  - `.jj/runs/<run-id>/`
- Run artifacts must include manifest, events, input snapshot, SPEC/TASK snapshots, git evidence, validation evidence, and command/provider summaries where applicable.
- `.jj/documents.sqlite3` must mirror redacted `.jj/` documents for local document history, but it must remain a derived local mirror rather than the authoritative workspace state.
- Raw `.jj/runs/<run-id>/` artifacts must remain local and uncommitted by default.

### 8.6 Dashboard

- `jj serve` must bind to localhost by default.
- Dashboard must start with current product/workflow state rather than arbitrary file browsing.
- Dashboard must expose the development flow, GitHub token login status, repository projects, current PRD/SPEC/task state, validation state, runs, risks, failures, run details, compare view, sanitized audit export, and guarded artifact links.
- Dashboard must treat one git repository as one project. The served workspace is always a project, and sanitized repositories discovered from GitHub workspace run history are grouped into separate project pages.
- Project pages must show project docs, task summary, and run logs when the repository is the served workspace. Projects discovered only from run history must not browse outside the served workspace for docs.
- Project document routes must be allowlisted to approved files only.
- Run artifact routes must serve only manifest-listed artifacts for validated run IDs.
- Dashboard responses must be redacted, HTML-escaped, and sent with `Cache-Control: no-store`.
- `jj serve` must load workspace `.env` values before resolving server config and web-triggered run environment, while preserving explicit shell environment precedence.
- `jj serve` must accept `OPENAI_KEY` as an alias for `OPENAI_API_KEY` when the canonical variable is unset, and must allow Kubernetes-related values such as `KUBECONFIG` and `K8S_CONFIG` to be supplied through `.env`.

### 8.7 GitHub Workspace Mode

- `--repo` must support clone/update into a local workspace and execute work on a work branch.
- Push must be disabled unless `--push` is explicit.
- `--auto-pr` must be opt-in and should create or reuse a deterministic GitHub PR when configured.
- GitHub tokens and remote URLs must be sanitized before persistence or display.

## 9. Security And Privacy Requirements

- Secrets must be redacted before persistence, model handoff, dashboard rendering, CLI summaries, and command metadata storage.
- Redaction must cover provider keys, authorization headers, cookies, private key blocks, credentialed URLs, common token formats, high-entropy token-like strings, and sensitive JSON/env fields.
- Redaction must use the fixed `[jj-omitted]` marker for omitted unstructured text.
- Workspace and artifact paths must use symlink-aware containment checks.
- Artifact writes must reject absolute paths, traversal, encoded traversal, unsafe hidden segments, Windows drive prefixes, and symlink escapes.
- Run IDs must reject traversal-like, path-shaped, secret-looking, or token-like values.
- Dashboard and CLI errors must avoid echoing attacker-controlled denied path payloads or secret-like inputs.
- Raw commands, raw environments, raw manifests, raw artifact bodies, raw diffs, raw validation payloads, and raw prompt handoffs must not be exposed in unsafe surfaces.
- `.env` files may contain secrets and must not be served through dashboard document or artifact routes.

## 10. UX Requirements

- A first-time user should understand the local workflow from `README.md` and run:
  - `go build -o jj ./cmd/jj`
  - `./jj run plan.md --dry-run`
  - `./jj run plan.md`
  - `./jj serve --cwd .`
- CLI summaries should make the next action clear after each run.
- Dashboard root should make current project state, development flow, GitHub token status, latest run, validation status, and recent failures easy to scan.
- Project pages should let a user review one repository's PRD/SPEC/TASK docs, task summary, and run logs without leaving the dashboard.
- Run detail pages should expose validation evidence, compare-to-previous links, and artifact inventory without raw unsafe content.
- Denied, unavailable, unknown, none, empty, malformed, stale, partial, and inconsistent metadata states should render deterministically.

## 11. Success Metrics

- A user can complete a dry-run and inspect generated planning artifacts without workspace state mutation.
- A full run either passes validation and reconciles SPEC, or fails safely without mutating SPEC.
- `jj serve --cwd .` exposes the current project state, project docs, GitHub token status, and recent runs without unsafe file browsing.
- `./scripts/validate.sh` passes locally and in CI for release gates.
- Security regression tests cover redaction, path containment, symlink escapes, dashboard traversal, command metadata, validation output, and dry-run leakage.
- Existing behavior remains stable during presentation helper maintenance and UI polish.

## 12. Launch Acceptance Criteria

- `go test ./...` passes.
- `go vet ./...` passes.
- `go build -o jj ./cmd/jj` passes.
- `./scripts/validate.sh` passes.
- README, SPEC, TASK, and PRD describe the same product boundaries.
- No documented workflow depends on unguarded file serving, raw artifact export, raw environment dumps, or nondeterministic validation.

## 13. Risks

- Planner or Codex output can be wrong even when the workflow is auditable.
- Overly broad task proposal modes can reopen completed work unless current evidence is interpreted carefully.
- Redaction is a best-effort guardrail and cannot replace avoiding unnecessary persistence of sensitive content.
- Dashboard usability can regress if presentation cleanup changes labels, ordering, links, fallback states, or sanitized data semantics.
- Local auto-commit behavior can surprise users if workspace cleanliness is misunderstood.

## 14. Open Questions

- Should PRD-level product milestones be stored as Markdown only, or mirrored into `.jj/documents.sqlite3` as a first-class document type?
- Should future validation support multiple configured gates while preserving `./scripts/validate.sh` as the release default?
- Should the dashboard eventually support GitHub OAuth/device login, or keep GitHub access configured through environment tokens only?
