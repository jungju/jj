# EVAL

## Summary

Latest changes address the main remaining gaps with focused code and tests: graceful malformed/legacy manifest handling, fail-closed artifact authorization, deterministic Codex fallback coverage, and broader redaction checks. I cannot mark PASS strictly because evidence is still reported rather than independently shown for full end-to-end manual flows, live Codex/OpenAI behavior, and complete secret scanning across every possible persisted/rendered surface.

## Result

PARTIAL

## Verdict

PARTIAL

## Score

91

## Task Completion

- Redacted persisted input artifacts input-original.md and input.md.
- Added deterministic no-OPENAI_API_KEY Codex fallback test using a fake Codex executable.
- Added persisted artifact redaction regression covering plan, planner, Codex, workspace docs, and manifest outputs.
- Made dashboard manifest loading graceful for malformed, incomplete, and legacy manifests.
- Changed invalid runs to render as unavailable rows without trusted artifact links.
- Changed artifact authorization to fail closed unless a valid manifest top-level artifacts map lists the requested path.
- Sanitized manifest, document, and artifact errors to avoid path or secret leakage.
- Added serve tests for malformed manifests, incomplete manifests, legacy commit-success manifests, malformed-manifest artifact rejection, and served redaction.

## Verification Results

- Reported PASS: GOCACHE=/tmp/jj-go-build-cache go test ./internal/serve ./internal/run.
- Reported PASS: GOCACHE=/tmp/jj-go-build-cache go test ./... .
- Reported PASS: GOCACHE=/tmp/jj-go-build-cache go vet ./... .
- Reported PASS: GOCACHE=/tmp/jj-go-build-cache GOMODCACHE=/tmp/jj-go-mod-cache go build -o jj ./cmd/jj .
- New tests cover fake Codex fallback, persisted artifact redaction, malformed/incomplete/legacy manifest dashboard rendering, malformed-manifest artifact rejection, sanitized malformed manifest responses, and served secret redaction.

## Diff Summary

- Redacted persisted input artifacts input-original.md and input.md.
- Added deterministic no-OPENAI_API_KEY Codex fallback test using a fake Codex executable.
- Added persisted artifact redaction regression covering plan, planner, Codex, workspace docs, and manifest outputs.
- Made dashboard manifest loading graceful for malformed, incomplete, and legacy manifests.
- Changed invalid runs to render as unavailable rows without trusted artifact links.
- Changed artifact authorization to fail closed unless a valid manifest top-level artifacts map lists the requested path.
- Sanitized manifest, document, and artifact errors to avoid path or secret leakage.
- Added serve tests for malformed manifests, incomplete manifests, legacy commit-success manifests, malformed-manifest artifact rejection, and served redaction.

## Missing Tests

- No missing tests were reported by evaluator.

## Next Actions

- Run one real local dry-run with OPENAI_API_KEY unset and a known Codex CLI binary, then inspect manifest planner.provider.
- Run manual HTTP traversal probes against the built jj binary for encoded, hidden, Windows-style, NUL, and backslash paths.
- Add a small compatibility note or migration guidance for legacy manifests without top-level artifacts maps.
- Keep a periodic artifact/HTML secret scan in CI if the project adds more artifact types.

## What Changed

- Redacted persisted input artifacts input-original.md and input.md.
- Added deterministic no-OPENAI_API_KEY Codex fallback test using a fake Codex executable.
- Added persisted artifact redaction regression covering plan, planner, Codex, workspace docs, and manifest outputs.
- Made dashboard manifest loading graceful for malformed, incomplete, and legacy manifests.
- Changed invalid runs to render as unavailable rows without trusted artifact links.
- Changed artifact authorization to fail closed unless a valid manifest top-level artifacts map lists the requested path.
- Sanitized manifest, document, and artifact errors to avoid path or secret leakage.
- Added serve tests for malformed manifests, incomplete manifests, legacy commit-success manifests, malformed-manifest artifact rejection, and served redaction.

## SPEC Requirement Results

- FAIL: `jj run <plan.md>` must read an existing, non-empty Markdown plan file. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: The positional plan path must be resolved relative to the shell invocation directory, not relative to `--cwd`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `--cwd` must select the target workspace where `.jj/runs`, workspace docs, Codex execution, git capture, and serve content are rooted. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: By default the target workspace must be a git repository. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `--allow-no-git` must allow non-git workspaces and record `git.available=false` in the manifest. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `--run-id` must select the run directory name and fail if that run already exists. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: If `--run-id` is omitted, `jj` must generate a unique time-based run id. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Planner provider selection order must be: injected planner, OpenAI planner when `OPENAI_API_KEY` is present, Codex CLI fallback planner when no API key is present. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Planning output must be structured and persisted as raw planning artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Drafts must be merged into final run-local `docs/SPEC.md` and `docs/TASK.md` for every successful planning run. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Dry-run must not write workspace `docs/SPEC.md`, workspace `docs/TASK.md`, workspace `docs/EVAL.md`, or code files. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Dry-run must not invoke implementation Codex. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Non-dry-run must write workspace SPEC and TASK before running implementation Codex. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Non-dry-run must capture Codex events, Codex summary, Codex exit status, final git status, git diff patch, git diff stat, and evaluation output. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Non-dry-run must not run `git add`, `git commit`, `git reset`, `git checkout`, `git stash`, `git clean`, or any other git history-mutating command. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Pre-existing dirty workspace state must be preserved and recorded as baseline evidence. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Failed phases must still leave any produced artifacts and a failed or partial manifest when possible. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `jj serve --cwd .` must serve a local dashboard at `/`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: The dashboard must render when `.jj/runs` is empty. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: The dashboard must render when one or more run manifests are malformed, unreadable, incomplete, legacy-shaped, or reference missing artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Malformed or legacy runs must be represented as invalid, unavailable, or degraded entries with concise redacted errors. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Valid runs must remain usable when other runs are malformed. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Missing, skipped, absent, or legacy commit metadata must not be treated as a current workflow failure. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Artifact links must be generated only for paths that are safe and allowed by the manifest or explicit public workspace document allowlist. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Artifact routes must fail closed when a run manifest is malformed, missing, or lacks the requested artifact path. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: The serve UI must render Markdown safely and block path traversal or access outside allowed workspace/run artifact paths. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `jj run plan.md --dry-run` creates `.jj/runs/<run-id>/input.md`, planning JSON, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Dry-run does not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Without `OPENAI_API_KEY`, planner provider is Codex CLI fallback and is recorded in the manifest using deterministic test coverage with a fake Codex binary or runner. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: With `OPENAI_API_KEY`, OpenAI is selected unless an injected planner is present. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Injected planner always has highest priority for tests and internal callers. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Non-dry-run writes workspace SPEC/TASK before Codex implementation and then records Codex artifacts, git status/diff, `docs/EVAL.md`, and manifest evaluation result. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Non-dry-run does not create a git commit and does not run staging or history-mutating git commands. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: A normal non-dry-run in a git repo leaves `git rev-parse HEAD` unchanged. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Pre-existing dirty changes are preserved and recorded as baseline evidence. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Non-git workspaces fail by default and run with `--allow-no-git` while recording git unavailable metadata. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `--cwd` changes the target workspace but does not change positional plan path resolution. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Planner, Codex, or evaluation failures leave a failed or partial manifest and available artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `jj serve --cwd .` renders a dashboard at `/` with TASK state, recent run status, evaluation result, failures or risks, next actions, and safe links. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: The dashboard renders successfully with empty runs, malformed JSON manifests, incomplete manifests, legacy commit-success manifests, missing artifact references, and mixed valid/invalid runs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Malformed or legacy manifest runs do not expose artifact links that bypass the manifest allowlist. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Artifact serving rejects raw or encoded traversal, absolute paths, backslashes, Windows drive paths, NUL bytes, hidden segments, and paths outside allowed roots through the real HTTP handling stack. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Artifact serving still serves valid manifest-listed artifacts and explicit public workspace docs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Manifest, logs, Codex artifacts, planner artifacts, errors, and served pages do not expose raw API keys, Bearer [redacted], Authorization header values, or secret-like `.jjrc` values. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.

## TASK Item Results

- FAIL: Inspect the existing codebase: CLI framework, module layout, tests, current `jj run`, current `jj serve`, config handling, artifact code, manifest code, git helpers, planner selection, redaction helpers, and docs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Confirm current non-dry-run git flow has no calls that stage, commit, reset, checkout, stash, clean, or otherwise mutate git history. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Preserve no-default-commit behavior and ensure manifest commit metadata is absent, skipped, or `ran:false` for newly generated default runs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Confirm final git evidence still captures baseline, `status.before`, `status.after`, diff patch, diff stat or summary, dirty flags, HEAD, branch, and git availability. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Inspect `internal/serve` manifest loading, dashboard aggregation, run summary construction, artifact link generation, and artifact route authorization. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add or refine a manifest parse result type that distinguishes valid runs from malformed, unreadable, incomplete, or legacy runs without aborting dashboard rendering. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Update dashboard rendering so malformed runs show a degraded invalid/unavailable row with redacted error text and no trusted artifact links. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure valid runs still show status, evaluation, risks, next action, and artifact links when another run is malformed. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure legacy commit metadata is treated as historical only; missing, skipped, absent, or old successful commit fields must not degrade current workflow health. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure artifact route authorization fails closed when a run manifest is malformed, missing, lacks an artifact map, or does not list the requested artifact path. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Preserve and extend raw decoded artifact path validation before `path.Clean`, `filepath.Clean`, root joining, or normalization. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Reject `docs/../manifest.json`, encoded traversal, `.secret/../manifest.json`, absolute paths, Windows drive and UNC-style paths, backslash traversal, NUL bytes, empty paths, hidden segments, hidden artifacts, and paths outside allowed roots. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure valid manifest-listed artifacts and explicit public workspace docs still serve successfully. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Inspect planner provider selection in `internal/run` or the relevant planner/config package. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add a deterministic test that unsets `OPENAI_API_KEY`, points Codex config or PATH to a temporary fake executable, runs a dry-run or planning-only pipeline, and asserts the manifest records provider `codex`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Keep the fake Codex executable minimal and matched to the fallback parser contract: emit valid planner JSON or expected Codex fallback output and exit zero. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Inspect redaction entry points for plan persistence, planner artifacts, Codex events and summaries, manifest writes, errors, dashboard rendering, Markdown rendering, and artifact views. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add one end-to-end redaction regression that injects fake secrets through plan text, planner output, Codex output, config or `.jjrc`, manifest values, and served pages where practical. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Scan created run artifacts and representative HTTP responses for raw fake secret strings; assert only redacted placeholders appear. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: If any surface bypasses redaction, route that write or render path through the central redaction helper immediately before persistence or HTML output. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add or update HTTP tests using the actual `net/http` request path handling stack for raw and encoded traversal cases. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Re-run existing no-commit regression tests to confirm HEAD remains unchanged, dirty workspace state remains dirty, and no successful commit metadata appears. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Run focused tests first: `go test ./internal/serve ./internal/run`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Run final verification: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Keep default calls to `git add`, `git commit`, or commit helpers removed or disabled during `jj run`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure default non-dry-run does not change `git rev-parse HEAD`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure pre-existing dirty files are not staged, committed, reverted, or otherwise modified by commit logic. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure non-dry-run still writes workspace SPEC/TASK before Codex implementation. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure non-dry-run still captures Codex events, summary, exit status, and errors. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure non-dry-run still captures git status before/after, diff patch, and diff stat after evaluation generation. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Ensure workspace and run-local `docs/EVAL.md` are still generated for non-dry-run. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Update manifest generation with accurate dirty metadata, artifact paths, planner provider/model, Codex result, evaluation result, errors, redaction marker, and no successful default commit metadata. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Keep CLI flags for `jj run`: `--cwd`, `--run-id`, `--dry-run`, `--allow-no-git`, `--planner-agents` or compatible `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, and `--task-doc`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Keep CLI flags for `jj serve`: `--cwd` and `--addr`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add or refine run-summary loading so malformed manifests cannot crash the dashboard. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Render malformed, unreadable, incomplete, or legacy manifests as sanitized invalid-run entries. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Generate artifact links only from manifest-listed artifacts or explicit public workspace document allowlist entries. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Fail closed for artifact requests against malformed, missing, or untrusted manifests. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Preserve safe Markdown rendering and secret redaction for served content. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add raw decoded artifact path validation before normalization. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Reject `docs/../manifest.json`, encoded traversal, `.secret/../manifest.json`, absolute paths, backslash traversal, Windows drive paths, UNC-style paths, NUL bytes, empty paths, hidden run artifacts, and paths outside allowed roots. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Still serve valid manifest-listed artifacts and explicit public workspace docs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add deterministic Codex fallback test coverage with `OPENAI_API_KEY` unset and no injected planner. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Add redaction coverage that scans persisted run artifacts and representative HTTP responses. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test input validation for missing, empty, directory, non-Markdown, and valid Markdown plans. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test `--cwd` with relative plan paths to prove the plan path is resolved from invocation directory. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test config precedence: CLI flags, env vars, `.jjrc`, defaults. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test secret redaction for OpenAI keys, Bearer [redacted], Authorization headers, and secret-like `.jjrc` values. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test planner provider selection: injected first, OpenAI when key exists, Codex fallback when no key exists. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Deterministic Codex fallback test with `OPENAI_API_KEY` unset and a fake Codex binary or runner; assert `planner.provider=codex` in `manifest.json`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test manifest serialization, status fields, and relative artifact paths. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test run id collision handling. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Unit test malformed planner output so it fails without writing successful empty SPEC/TASK. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Integration test dry-run with fake planner: run artifacts are created, workspace docs/code are untouched, Codex runner is not called. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Integration test non-dry-run in a temporary git repo with fake planner and fake Codex runner: workspace docs, Codex artifacts, git status/diff, EVAL, and manifest are created. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Regression test non-dry-run with fake planner and fake Codex does not create a git commit and leaves HEAD unchanged. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Regression test dirty workspace before run remains dirty, is recorded as `dirty_before=true`, and is not committed. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Manifest test asserts default non-dry-run has no successful commit metadata and still has git availability, dirty flags, status paths, diff path, and diff stat path. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Integration test no-git workspace: fail without `--allow-no-git`, succeed with it and record git unavailable. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Failure-path tests for planner failure, Codex failure, and evaluation failure: manifest status and partial artifacts remain. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: HTTP handler tests for dashboard with no docs, no runs, successful run, failed run, missing evaluation, malformed manifest, incomplete manifest, legacy commit-success manifest, missing artifact references, and mixed valid/invalid runs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: HTTP security tests rejecting `docs/../manifest.json`, encoded traversal such as `docs%2f..%2fmanifest.json` and `%2e%2e`, `.secret/../manifest.json`, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL bytes, hidden segments, and hidden run artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: HTTP handler test proving a valid manifest-listed artifact still serves successfully. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: HTTP handler test proving a malformed-manifest run cannot serve unlisted artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Markdown rendering tests that raw script content is escaped or removed. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Security regression test injecting fake secrets through env, `.jjrc`, plan, planner output, Codex output, errors, manifests, and served pages; assert artifacts and HTTP responses do not contain raw secrets. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Run focused tests: `go test ./internal/serve ./internal/run`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Run final verification: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Required SPEC and TASK documents are generated with the expected sections. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `jj run plan.md --dry-run` creates only run-local planning artifacts and manifest. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Dry-run side effects on workspace docs/code are covered by tests. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Provider fallback works without `OPENAI_API_KEY` and is recorded in manifest. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Injected planner is available for deterministic tests. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Non-dry-run records workspace docs, Codex artifacts, git evidence, evaluation output, and final manifest. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Default non-dry-run creates no git commit, leaves HEAD unchanged, and produces no successful commit metadata. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Pre-existing dirty workspace state is preserved and recorded. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `jj serve --cwd .` renders a dashboard-first root page. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Dashboard rendering is graceful for empty runs, malformed manifests, incomplete manifests, legacy manifests, missing artifact references, and mixed valid/invalid runs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Artifact serving rejects traversal, encoded traversal, hidden paths, absolute paths, Windows-style paths, backslashes, NUL bytes, unlisted artifacts, and malformed-manifest artifact requests. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Valid manifest-listed artifacts and public workspace docs still serve successfully. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: Secret redaction is verified across persisted artifacts and representative served HTML. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: The implementation preserves existing CLI flags, config precedence, artifact layout, and manifest-relative artifact paths. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- FAIL: `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.

## Requirements Coverage

- No-default-commit behavior appears preserved from prior changes and is not regressed by this iteration.
- Dashboard-first serve behavior is materially improved for malformed, incomplete, legacy, and mixed valid/invalid runs.
- Artifact serving is now stricter: malformed or missing artifact maps fail closed, and valid manifest-listed artifacts still have coverage.
- Codex fallback without OPENAI_API_KEY now has deterministic executable-level test coverage, not just injected planner coverage.
- Secret redaction coverage expanded to persisted run artifacts, workspace docs generated during non-dry-run, dashboard HTML, and artifact views.
- Original broad product flow appears substantially covered by existing tests plus these additions, but not all manual acceptance criteria are evidenced.

## Test Coverage

- Reported PASS: GOCACHE=/tmp/jj-go-build-cache go test ./internal/serve ./internal/run.
- Reported PASS: GOCACHE=/tmp/jj-go-build-cache go test ./... .
- Reported PASS: GOCACHE=/tmp/jj-go-build-cache go vet ./... .
- Reported PASS: GOCACHE=/tmp/jj-go-build-cache GOMODCACHE=/tmp/jj-go-mod-cache go build -o jj ./cmd/jj .
- New tests cover fake Codex fallback, persisted artifact redaction, malformed/incomplete/legacy manifest dashboard rendering, malformed-manifest artifact rejection, sanitized malformed manifest responses, and served secret redaction.

## Risks

- Live OpenAI and real Codex CLI behavior were not exercised; fake executable coverage proves selection/invocation shape but not external tool compatibility.
- Top-level manifest artifacts are now the sole run artifact allowlist; older manifests without that map become unavailable by design, which is safe but may reduce legacy usability.
- Manual dry-run inspection, manual serve traversal probing, and manual end-to-end secret scanning were not shown.
- Redaction is broader, but complete assurance across every future artifact/error surface still depends on all writers continuing to call the central redaction path.

## Unknowns

- (none)

## Regressions

- No confirmed regressions from the provided evidence.
- Legacy manifests lacking a top-level artifacts map can no longer serve artifacts; this is an intentional fail-closed behavior, but it is a compatibility regression for old runs.

## Recommended Follow-ups

- Run one real local dry-run with OPENAI_API_KEY unset and a known Codex CLI binary, then inspect manifest planner.provider.
- Run manual HTTP traversal probes against the built jj binary for encoded, hidden, Windows-style, NUL, and backslash paths.
- Add a small compatibility note or migration guidance for legacy manifests without top-level artifacts maps.
- Keep a periodic artifact/HTML secret scan in CI if the project adds more artifact types.

## Secret Redaction Check

- Generated evaluation content was redacted before being written.

