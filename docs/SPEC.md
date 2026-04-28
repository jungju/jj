# jj Security Baseline Spec

## Overview

`jj` is a local CLI that turns a Markdown plan or web prompt into canonical JSON state, implementation instructions, Codex execution, validation evidence, and auditable run artifacts. `plan.md` is the initial product seed; once `.jj/spec.json` exists, that SPEC is the planning source of truth and `plan.md` becomes background product vision.

Canonical runtime state is:

- `.jj/spec.json`
- `.jj/tasks.json`
- `.jj/runs/<run-id>/`

Dry-runs write planning artifacts and state snapshots only under `.jj/runs/<run-id>/`; they do not update `.jj/spec.json`, `.jj/tasks.json`, or workspace documentation. Dry-run `snapshots/spec.after.json` remains the proposed preview for compatibility.

Full runs append `.jj/tasks.json` during planning, but do not write workspace `.jj/spec.json` before implementation. When validation passes, jj reconciles the previous SPEC, planned SPEC, selected task, Codex summary, git diff summary, and validation summary into the final `.jj/spec.json`. If validation fails, is missing, or is skipped, the previous workspace SPEC remains unchanged and `snapshots/spec.after.json` records that unchanged state.

When the target workspace is a clean git repository, a successful full run creates a local commit containing source changes plus `.jj/spec.json` and `.jj/tasks.json`. `.jj/runs/<run-id>/` remains uncommitted local artifact history. If the workspace was dirty before the run, jj skips the commit to avoid mixing pre-existing user changes with generated changes.

`.jj/tasks.json` is append-only task proposal history. Each run appends newly proposed tasks with fresh IDs, selects the first new runnable task for full-run implementation, and updates only that selected task after validation. Existing `active` or `in_progress` tasks are returned to `queued` when a new full-run task is selected.

`docs/SPEC.md` and `docs/TASK.md` are repository documentation for the current product boundary. The dashboard exposes those docs, `.jj/spec.json`, `.jj/tasks.json`, `README.md`, `plan.md`, and manifest-listed run artifacts through explicit allowlisted routes only.

## Security Goals

- Redact secrets before data is persisted, rendered, logged, or sent to model/provider prompts.
- Keep all run artifacts under `.jj/runs/<run-id>/`.
- Keep workspace state writes under the resolved workspace root.
- Preserve dry-run as an artifact-only planning mode with no workspace state or docs writes.
- Prevent traversal, hidden artifact paths, Windows drive prefixes, encoded path escapes, and symlink escapes.
- Serve a dashboard-first local UI without arbitrary workspace browsing or raw absolute path disclosure.
- Execute child commands with explicit argv/env handling instead of shell-interpolated command strings.

## Redaction Policy

The shared redaction layer is implemented in `internal/security` and surfaced through `internal/secrets`. Public helpers include `RedactString`, `RedactBytes`, and `RedactMap` for text, bytes, and structured JSON-like maps.

It covers:

- Exact sensitive environment values from keys containing `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, `CREDENTIAL`, `AUTHORIZATION`, or `COOKIE`.
- OpenAI-style API keys, GitHub tokens, npm tokens, AWS access keys, Slack tokens, JWTs, private key blocks, credentialed URLs, Authorization headers, Cookie and Set-Cookie headers, Bearer tokens, and generic high-entropy token-like strings.
- Sensitive JSON-like fields, env maps, and nested values for `api_key`, generic `*_KEY`, `*_TOKEN`, `*_SECRET`, `password`, `authorization`, `cookie`, and credential-like keys.
- JSONL event streams, Markdown/text logs, command output, dotenv-style assignments, quoted secret values, and query-string secret parameters.

Safe structure is preserved where possible with `[jj-omitted]` for unstructured text. Legacy generic redaction placeholders from upstream tools or user-authored input are normalized to the same jj marker before persistence or serving. Structured security-sensitive projections, including manifest configuration, omit values that would require redaction and keep only safe presence metadata. Redaction is a guardrail, not a cryptographic proof that every possible proprietary string is sensitive; callers must still avoid intentionally storing unnecessary raw secrets.

## Path And Artifact Policy

Workspace and artifact paths are resolved with symlink-aware containment checks before reads, writes, and serving.

Plan file paths are resolved before planning starts. Relative plan paths are still interpreted from the invocation directory so existing CLI path semantics remain predictable, but the resolved target must stay inside the resolved `--cwd` workspace. Absolute plan paths must also resolve under the target workspace. Paths outside `--cwd`, encoded escapes, traversal, and symlink escapes are rejected.

Artifact relative paths are rejected when they:

- Are absolute.
- Contain `..`, `.`, empty segments, backslashes, encoded traversal, or NUL bytes.
- Use Windows drive prefixes or UNC-style escapes.
- Include hidden segments such as `.env` or `.secret`.
- Resolve through a symlinked artifact or state path.

Artifact writes use private run permissions and atomic writes. Artifact reads through `jj serve` require the path to be present in the run manifest.

Codex event and summary outputs are resolved under `.jj/runs/<run-id>/` before launch and re-resolved through the run artifact store before fallback creation, redaction, or readback. If a runner replaces either output with a symlink, jj rejects the artifact before reading it and records only a sanitized symlink-path diagnostic.

## Dashboard Policy

`jj serve` binds to `127.0.0.1:7331` by default. External binding requires explicit `--host` or `--addr`.

The dashboard:

- Serves only `README.md`, `plan.md`, `docs/SPEC.md`, `docs/TASK.md`, `.jj/spec.json`, `.jj/tasks.json`, and manifest-listed run artifacts.
- Rejects traversal, encoded traversal, dotfile browsing, malformed run IDs, and symlink escapes.
- Redacts and HTML-escapes rendered metadata and artifact content.
- Uses safe display labels such as `[workspace]`, `.jj/runs/<run-id>`, and `[path]` instead of raw absolute workspace paths.
- Sends `Cache-Control: no-store` on dashboard, JSON, and artifact responses so local review pages are not cached.

Run inspection routes stay inside the guarded `.jj/runs` root:

- `/runs` lists discoverable runs newest first and supports only allowlisted status, dry-run, planner provider, evaluation, and run-id substring filters.
- `/runs/<run-id>` renders sanitized manifest-derived detail, guarded artifact links, evaluation metadata, Codex metadata, sanitized command metadata, security diagnostics, and next-action hints.
- `/runs/compare?left=<run-id>&right=<run-id>` compares exactly two validated run IDs using sanitized manifest fields only.
- `/runs/audit?run=<run-id>` returns a sanitized JSON audit summary for one validated run ID and never embeds raw artifact bodies or raw manifest content.

## Command Policy

Codex, Git, validation, and repository commands are launched through structured `exec.CommandContext` calls with explicit binary and arg slices. Command working directories are resolved and validated before execution, and long-running child commands are bounded by command-specific timeouts.

Command metadata stored in artifacts includes sanitized argv, safe path labels, filtered environments, exit status, duration, and redacted errors. Sensitive flag values such as `--token <value>`, `--api-key <value>`, and `--api-key=value` are replaced with `[jj-omitted]`. Raw environment dumps are not persisted.

## Manifest Policy

`manifest.json` includes sanitized run status, SafeConfig configuration metadata, git metadata, planner provider, Codex result, validation result, artifacts, risks, errors, and security metadata.

SafeConfig records non-secret fields such as planning agent count, model names when they do not match configured secret material, Codex binary when safe, task proposal mode, config file path when safe, OpenAI key environment variable name, OpenAI key presence as a boolean, no-git mode, and canonical state paths. It never stores runtime secret values such as API keys or GitHub tokens.

Run IDs may contain only letters, numbers, dots, underscores, and dashes. Reserved traversal-like IDs, encoded/path-shaped values, and IDs matching configured secrets or common token patterns are rejected before run directory creation or dashboard resolution.

Security metadata records:

- `redaction_applied`
- `workspace_guardrails_applied`
- `redaction_count`
- redaction, path, serve, command, and environment policy summaries

## Validation

Required validation for this baseline:

- `go test ./...`
- `go vet ./...`
- `go build -o jj ./cmd/jj`
- `./scripts/validate.sh`
