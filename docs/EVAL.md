# EVAL

## Summary

The focused verification/safety iteration satisfies the plan. I independently inspected the relevant diffs and reran the focused and full verification commands; fallback planning, no-default-commit behavior, fail-closed artifact serving, malformed/legacy manifest handling, traversal rejection, and redaction coverage are now backed by deterministic tests.

## Result

PASS

## Verdict

PASS

## Score

96

## Task Completion

- internal/serve/serve.go now sanitizes path-related errors, adds invalid-run artifact-link guidance, and labels legacy commit-success metadata as historical.
- internal/serve/serve_test.go adds real httptest.Server traversal/leak probes, malformed/missing/legacy dashboard assertions, and valid artifact/public doc positive checks.
- internal/run/run_test.go strengthens no-default-commit regressions by asserting no staged changes remain after non-dry-run flows.
- Workspace diff also includes docs/SPEC.md and docs/TASK.md updates that scope this iteration around reproducible verification evidence.

## Verification Results

- PASS: GOCACHE=/tmp/jj-go-build-cache go test ./internal/serve ./internal/run
- PASS: GOCACHE=/tmp/jj-go-build-cache go test ./...
- PASS: GOCACHE=/tmp/jj-go-build-cache go vet ./...
- PASS: GOCACHE=/tmp/jj-go-build-cache GOMODCACHE=/tmp/jj-go-mod-cache go build -o jj ./cmd/jj

## Diff Summary

- internal/serve/serve.go now sanitizes path-related errors, adds invalid-run artifact-link guidance, and labels legacy commit-success metadata as historical.
- internal/serve/serve_test.go adds real httptest.Server traversal/leak probes, malformed/missing/legacy dashboard assertions, and valid artifact/public doc positive checks.
- internal/run/run_test.go strengthens no-default-commit regressions by asserting no staged changes remain after non-dry-run flows.
- Workspace diff also includes docs/SPEC.md and docs/TASK.md updates that scope this iteration around reproducible verification evidence.

## Missing Tests

- No missing tests were reported by evaluator.

## Next Actions

- Keep the new fallback, traversal, redaction, and no-commit tests in CI.
- Consider adding a small legacy-manifest compatibility note to user docs if older runs without top-level artifacts maps are common.

## What Changed

- internal/serve/serve.go now sanitizes path-related errors, adds invalid-run artifact-link guidance, and labels legacy commit-success metadata as historical.
- internal/serve/serve_test.go adds real httptest.Server traversal/leak probes, malformed/missing/legacy dashboard assertions, and valid artifact/public doc positive checks.
- internal/run/run_test.go strengthens no-default-commit regressions by asserting no staged changes remain after non-dry-run flows.
- Workspace diff also includes docs/SPEC.md and docs/TASK.md updates that scope this iteration around reproducible verification evidence.

## SPEC Requirement Results

- PASS: `jj run <plan.md>` must read an existing, non-empty Markdown plan file. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: The positional plan path must be resolved relative to the shell invocation directory, not relative to `--cwd`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `--cwd` must select the target workspace where `.jj/runs`, workspace docs, Codex execution, git capture, and serve content are rooted. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: By default the target workspace must be a git repository. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `--allow-no-git` must allow non-git workspaces and record `git.available=false` in the manifest. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `--run-id` must select the run directory name and fail if that run already exists. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: If `--run-id` is omitted, `jj` must generate a unique time-based run id. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Planner provider selection order must be injected planner, OpenAI planner when `OPENAI_API_KEY` is present, then Codex CLI fallback planner when no API key is present. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Planning output must be structured and persisted as raw planning artifacts after redaction. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Drafts must be merged into final run-local `docs/SPEC.md` and `docs/TASK.md` for every successful planning run. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Dry-run must not write workspace `docs/SPEC.md`, workspace `docs/TASK.md`, workspace `docs/EVAL.md`, or code files. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Dry-run must not invoke implementation Codex. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Non-dry-run must write workspace SPEC and TASK before running implementation Codex. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Non-dry-run must capture Codex events, Codex summary, Codex exit status, final git status, git diff patch, git diff stat, and evaluation output. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Non-dry-run must not run `git add`, `git commit`, `git reset`, `git checkout`, `git stash`, `git clean`, or any other git history-mutating command. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Pre-existing dirty workspace state must be preserved and recorded as baseline evidence. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Failed phases must still leave produced artifacts and a failed or partial manifest when possible. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `jj serve --cwd .` must serve a local dashboard at `/`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: The dashboard must render when `.jj/runs` is empty. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: The dashboard must render when one or more run manifests are malformed, unreadable, incomplete, legacy-shaped, or reference missing artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Malformed or legacy runs must be represented as invalid, unavailable, or degraded entries with concise redacted errors and no trusted artifact links. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Missing, skipped, absent, or legacy commit metadata must not be treated as a current workflow failure. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Artifact links must be generated only for paths that are safe and allowed by the manifest or explicit public workspace document allowlist. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Artifact routes must fail closed when a run manifest is malformed, missing, or lacks the requested artifact path. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: The serve UI must render Markdown safely and block path traversal or access outside allowed workspace/run artifact paths. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Verification coverage must prove Codex fallback with `OPENAI_API_KEY` unset, HTTP traversal rejection, persisted and served redaction, malformed-manifest fail-closed behavior, and default no-commit behavior. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `jj run plan.md --dry-run` creates `.jj/runs/<run-id>/input.md`, planning artifacts, run-local `docs/SPEC.md`, run-local `docs/TASK.md`, and `manifest.json`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Dry-run does not create or modify workspace `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, or code files. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Without `OPENAI_API_KEY`, planner provider is Codex CLI fallback and is recorded as `codex` in the manifest using deterministic fake-Codex coverage. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: With `OPENAI_API_KEY`, OpenAI is selected unless an injected planner is present. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Injected planner always has highest priority for tests and internal callers. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Non-dry-run writes workspace SPEC/TASK before Codex implementation and records Codex artifacts, git status/diff, `docs/EVAL.md`, and manifest evaluation result. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Non-dry-run does not create a git commit, does not stage files, and leaves `git rev-parse HEAD` unchanged. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Pre-existing dirty changes are preserved and recorded as baseline evidence. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Non-git workspaces fail by default and run with `--allow-no-git` while recording git unavailable metadata. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `jj serve --cwd .` renders a dashboard for no runs, valid runs, failed runs, malformed manifests, incomplete manifests, and legacy commit-success manifests. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Malformed or untrusted manifests cannot authorize artifact access. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Valid manifest-listed artifacts and explicit public workspace docs still serve successfully. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Representative raw and encoded traversal probes through the real HTTP stack return non-2xx and do not leak raw paths or fake secrets. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Fake secrets injected through plan text, planner output, Codex output, config-like values, manifest-like values, docs, and served pages do not appear in persisted text artifacts or representative HTTP responses. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Optional verification artifacts are redacted, run-relative, and linked only through the manifest artifact map. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `go test ./internal/serve ./internal/run`, `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.

## TASK Item Results

- PASS: Inspect existing code and tests in `cmd/jj`, `internal/run`, `internal/serve`, manifest structs, config handling, redaction helpers, git helpers, planner selection, Codex integration, and evaluation code. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Identify which latest PARTIAL gaps are already covered and which need stronger reproducible evidence. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Reuse existing fake planner, fake Codex, temp workspace, temp git repo, manifest, redaction, and HTTP test helpers where available. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add or tighten deterministic Codex fallback coverage: unset `OPENAI_API_KEY`, configure a temporary fake Codex executable or runner, run a dry-run or planning-only flow, and assert `manifest.json` records planner provider `codex`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Ensure the fake Codex executable emits output that matches the fallback parser contract closely enough to prove provider selection and parsing without invoking the real Codex binary. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add or tighten no-default-commit regression coverage: run a non-dry-run in a fresh temporary git repo with fake planner and fake Codex, record HEAD before and after, assert HEAD is unchanged, assert dirty state is preserved when present, and assert newly generated manifest commit metadata is absent, skipped, or `ran:false`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Confirm no implementation path calls `git add`, `git commit`, `git reset`, `git checkout`, `git stash`, `git clean`, or another history-mutating git command. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add or extend serve tests using `httptest.Server` or the actual registered handler stack with normal `net/http` request parsing. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Cover traversal and unsafe artifact requests including `docs/../manifest.json`, encoded slash traversal, encoded dot-dot traversal, `.secret/../manifest.json`, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL byte encodings, hidden segments, and hidden run artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Assert unsafe artifact requests return non-2xx responses and do not leak absolute filesystem paths or raw fake secrets. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Assert malformed, incomplete, unreadable, and legacy manifests render as degraded or unavailable dashboard rows without trusted artifact links. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Assert a legacy manifest with historical `commit.status=success` is treated as historical/degraded and does not imply current default auto-commit behavior. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Assert valid manifest-listed artifacts and explicit public workspace docs still serve successfully through the same HTTP stack. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add a concise compatibility note in the dashboard invalid-run row or docs explaining that legacy runs without a trusted top-level `artifacts` map cannot expose artifact links for safety. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add or extend a redaction regression that injects fake secrets through plan text, planner output, Codex summary/events, config or `.jjrc`-like values, manifest-visible strings, docs, served Markdown, and error paths where practical. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Scan only deterministic text run artifacts and representative HTTP response bodies for raw fake secret strings to avoid binary or build-output noise. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: If any raw fake secret appears, route that write or render path through the central redaction helper immediately before persistence or HTML output. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: If verification artifacts are produced, write redacted run-relative files such as `verification/summary.md`, `verification/results.json`, and optional `verification/http-probes.jsonl`, then list them in the manifest artifact map. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Keep edits scoped to `internal/run`, `internal/serve`, docs, and existing tests unless the codebase clearly has a shared helper package that should own the behavior. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Run focused tests first, then full test, vet, and build verification. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Keep default calls to `git add`, `git commit`, or commit helpers removed or disabled during `jj run`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Ensure default non-dry-run does not change `git rev-parse HEAD`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Ensure pre-existing dirty files are not staged, committed, reverted, or otherwise modified by commit logic. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Ensure non-dry-run still writes workspace SPEC/TASK before Codex implementation. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Ensure non-dry-run still captures Codex events, summary, exit status, errors, git status before/after, diff patch, diff stat, EVAL, and manifest evaluation result. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Ensure workspace and run-local `docs/EVAL.md` are generated for non-dry-run. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Ensure manifest generation includes accurate dirty metadata, artifact paths, planner provider/model, Codex result, evaluation result, errors, redaction marker, and no successful default commit metadata. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Keep CLI flags for `jj run`: `--cwd`, `--run-id`, `--dry-run`, `--allow-no-git`, `--planner-agents` or compatible `--planning-agents`, `--openai-model`, `--codex-model`, `--spec-doc`, and `--task-doc`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Keep CLI flags for `jj serve`: `--cwd` and `--addr`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add or refine run-summary loading so malformed manifests cannot crash the dashboard. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Render malformed, unreadable, incomplete, or legacy manifests as sanitized invalid or unavailable run entries. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add concise dashboard or docs guidance that legacy manifests without a trusted artifact map cannot expose artifact links by design. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Generate artifact links only from manifest-listed artifacts or explicit public workspace document allowlist entries. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Fail closed for artifact requests against malformed, missing, or untrusted manifests. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Preserve safe Markdown rendering and secret redaction for served content. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add or preserve raw decoded artifact path validation before normalization. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Reject traversal, encoded traversal, hidden segments, hidden artifacts, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL bytes, empty paths, and paths outside allowed roots. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Still serve valid manifest-listed artifacts and explicit public workspace docs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add deterministic Codex fallback coverage with `OPENAI_API_KEY` unset and no injected planner. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add redaction coverage that scans persisted text run artifacts and representative HTTP responses. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Add or update no-default-commit coverage that compares HEAD before and after non-dry-run and checks manifest commit metadata. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test input validation for missing, empty, directory, non-Markdown, and valid Markdown plans. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test `--cwd` with relative plan paths to prove the plan path is resolved from invocation directory. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test config precedence: CLI flags, env vars, `.jjrc`, defaults. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test secret redaction for OpenAI keys, Bearer [redacted], authorization headers, and secret-like `.jjrc` values. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test planner provider selection: injected first, OpenAI when key exists, Codex fallback when no key exists. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Deterministic Codex fallback test with `OPENAI_API_KEY` unset and a fake Codex binary or runner; assert `planner.provider=codex` in `manifest.json`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test manifest serialization, status fields, and relative artifact paths. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test run id collision handling. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Unit test malformed planner output so it fails without writing successful empty SPEC/TASK. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Integration test dry-run with fake planner: run artifacts are created, workspace docs/code are untouched, and implementation Codex is not called. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Integration test non-dry-run in a temporary git repo with fake planner and fake Codex runner: workspace docs, Codex artifacts, git status/diff, EVAL, and manifest are created. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Regression test non-dry-run with fake planner and fake Codex does not create a git commit and leaves HEAD unchanged. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Regression test dirty workspace before run remains dirty, is recorded as `dirty_before=true`, and is not committed. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Manifest test asserts default non-dry-run has no successful commit metadata and still has git availability, dirty flags, status paths, diff path, and diff stat path. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Integration test no-git workspace: fail without `--allow-no-git`, succeed with it and record git unavailable. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Failure-path tests for planner failure, Codex failure, and evaluation failure: manifest status and partial artifacts remain. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: HTTP handler tests for dashboard with no docs, no runs, successful run, failed run, missing evaluation, malformed manifest, incomplete manifest, legacy commit-success manifest, missing artifact references, and mixed valid/invalid runs. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: HTTP security tests through the real handler stack rejecting raw and encoded traversal, hidden paths, absolute paths, Windows drive paths, UNC-style paths, backslash traversal, NUL bytes, hidden segments, and hidden run artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: HTTP handler test proving a valid manifest-listed artifact still serves successfully. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: HTTP handler test proving a malformed-manifest run cannot serve unlisted artifacts. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Markdown rendering tests that raw script content is escaped or removed. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Security regression test injecting fake secrets through env, `.jjrc`, plan, planner output, Codex output, errors, manifests, docs, and served pages; assert selected artifacts and HTTP responses do not contain raw secrets. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Run focused tests: `GOCACHE=/tmp/jj-go-build-cache go test ./internal/serve ./internal/run`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Run final verification: `GOCACHE=/tmp/jj-go-build-cache go test ./...`, `GOCACHE=/tmp/jj-go-build-cache go vet ./...`, and `GOCACHE=/tmp/jj-go-build-cache GOMODCACHE=/tmp/jj-go-mod-cache go build -o jj ./cmd/jj`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Codex fallback with `OPENAI_API_KEY` unset is covered by deterministic fake-Codex testing and records provider `codex`. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Real HTTP handler traversal probes fail closed and do not leak raw paths or secrets. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Valid manifest-listed artifacts and explicit public workspace docs still serve. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Malformed, incomplete, unreadable, and legacy manifests render without breaking the dashboard and cannot authorize artifact access. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Legacy commit-success metadata is clearly historical or degraded and does not imply current auto-commit behavior. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Persisted text artifacts and representative served responses redact injected fake secrets. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Non-dry-run with fake Codex leaves HEAD unchanged, preserves dirty state, and has no successful default commit metadata. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: Optional verification artifacts, if added, are redacted, run-relative, and manifest-listed. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.
- PASS: `go test ./internal/serve ./internal/run`, `go test ./...`, `go vet ./...`, and `go build -o jj ./cmd/jj` pass. Evidence: docs/SPEC.md, docs/TASK.md, planning/evaluation.json, git/diff-summary.txt, codex/summary.md.

## Requirements Coverage

- PASS: Codex fallback without OPENAI_API_KEY is covered with injected/fake runners and a fake Codex executable; manifest provider is asserted as codex.
- PASS: Default non-dry-run no-commit policy is covered by HEAD unchanged, skipped commit metadata, dirty workspace preservation, and no staged changes assertions.
- PASS: Code search found no implementation calls to git add, commit, reset, checkout, stash, or clean.
- PASS: jj serve handles malformed, missing, incomplete, and legacy manifests without breaking the dashboard, with invalid runs shown without trusted artifact links.
- PASS: Legacy commit-success metadata is shown as historical and does not imply current auto-commit behavior.
- PASS: Artifact serving remains manifest-allowlisted and rejects raw/encoded traversal, hidden paths, absolute paths, Windows/UNC-style paths, backslashes, NUL bytes, and unlisted artifacts through the HTTP stack.
- PASS: Valid manifest-listed artifacts and explicit public workspace docs still serve successfully.
- PASS: Redaction coverage includes persisted run artifacts, workspace docs, manifest output, dashboard, served docs/artifacts, and unsafe-path error responses.

## Test Coverage

- PASS: GOCACHE=/tmp/jj-go-build-cache go test ./internal/serve ./internal/run
- PASS: GOCACHE=/tmp/jj-go-build-cache go test ./...
- PASS: GOCACHE=/tmp/jj-go-build-cache go vet ./...
- PASS: GOCACHE=/tmp/jj-go-build-cache GOMODCACHE=/tmp/jj-go-mod-cache go build -o jj ./cmd/jj

## Risks

- Automated coverage intentionally uses fake OpenAI/Codex surfaces; live external provider compatibility remains outside this iteration's scope.
- Future artifact types must continue to route through the central redaction and manifest allowlist paths.

## Unknowns

- (none)

## Regressions

- (none)

## Recommended Follow-ups

- Keep the new fallback, traversal, redaction, and no-commit tests in CI.
- Consider adding a small legacy-manifest compatibility note to user docs if older runs without top-level artifacts maps are common.

## Secret Redaction Check

- Generated evaluation content was redacted before being written.

