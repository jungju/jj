# jj Security Baseline Tasks

## Current Task

### TASK-0017: Harden secret redaction and workspace boundaries

- Mode: security
- Status: done
- Priority: high
- Validation command: `./scripts/validate.sh`

## Acceptance Criteria

- Shared redaction is applied before writing or rendering manifests, planner outputs, Codex events, summaries, logs, git evidence, generated workspace state, validation artifacts, CLI errors, and dashboard HTML.
- Redaction covers provider secrets, Authorization, Bearer, Cookie, and Set-Cookie values, private key blocks, credential URLs, common key/token/secret/password/cookie fields including generic `*_KEY`, `*_TOKEN`, and `*_SECRET` names, nested JSON-like values, env maps, dotenv-style lines, quoted values, query strings, generic high-entropy token-like strings, and mixed prompt/log text.
- Unstructured redaction uses the fixed `[jj-omitted]` marker, normalizes legacy generic redaction placeholders, and does not preserve original secret lengths.
- Workspace, plan, state, run, artifact, and dashboard paths use symlink-aware containment checks with `--cwd` as the workspace trust boundary.
- Relative plan paths continue to resolve from the invocation directory, but the resolved plan file must remain inside the resolved target workspace; absolute plan paths must also resolve inside `--cwd`.
- Artifact writes reject absolute paths, traversal, unsafe segments, Windows drive prefixes, hidden artifact names, and symlink escapes.
- Codex output artifacts must resolve under `.jj/runs/<run-id>/`, may not traverse symlinked output parents, and are revalidated before post-run fallback creation, redaction, or readback.
- Run IDs reject traversal-like values, invalid characters, configured secret values, and common token patterns without echoing the rejected value in validation errors.
- `jj run --dry-run` writes planning artifacts and state snapshots only under `.jj/runs/<run-id>/`; it does not write or update SQLite workspace state, `docs/PRD.md`, `docs/SPEC.md`, or `docs/TASK.md`.
- `jj serve` defaults to a local-only bind, serves only approved project docs/state and manifest-listed run artifacts, blocks traversal and dotfile browsing, escapes rendered content, and sends `Cache-Control: no-store`.
- `jj serve` exposes a project-oriented dashboard with development flow, GitHub token status, registered repository projects, per-project docs, per-project task summaries, and per-project run logs.
- `jj serve` loads workspace `.env` before resolving server config, keeps shell environment precedence, supports `OPENAI_KEY` as an `OPENAI_API_KEY` alias, and allows Kubernetes-related values such as `KUBECONFIG` and `K8S_CONFIG` to be supplied without serving `.env`.
- `jj serve` exposes guarded run history, run detail, two-run comparison, and sanitized JSON audit export routes derived from validated run IDs and sanitized manifest fields only.
- Codex, Git, repository, and validation commands use explicit argv/env handling through `exec.CommandContext`, resolved command working directories, filtered environments, and timeouts.
- Command and environment metadata is sanitized before persistence, including paired sensitive argv fragments such as `--token <value>` and inline values such as `--api-key=value`.
- Manifest configuration is produced through a shared SafeConfig projection that keeps non-secret fields and key presence metadata while omitting runtime secret values.
- Regression tests cover redaction, containment, artifact safety, dry-run leakage, command metadata, dashboard traversal, symlink escape prevention, and safe dashboard display paths.

## Implementation Notes

- Redaction lives in `internal/security` and is re-exported by `internal/secrets`.
- Central helper APIs include `RedactString`, `RedactBytes`, `RedactMap`, `NewSafeConfig`, `SafeJoin`, and `SafeJoinNoSymlinks`.
- Configured sensitive literals use the same low-information filtering as environment-derived secrets so values such as `true`, `false`, `null`, and `none` do not become global redaction traps.
- Server dotenv loading is startup-only process environment hydration; raw `.env` contents are not persisted into run artifacts or exposed through dashboard document routes.
- Artifact safety lives in `internal/artifact.Store` and `security.SafeJoin`.
- `internal/artifact.Store` mirrors every redacted generated document it writes into `.jj/documents.sqlite3`; existing `.jj/` files, externally generated Codex logs/summaries, autopilot logs, web-run logs, next-intent input, and workspace SPEC/task writes are imported into the same SQLite database after sanitization. SQLite current SPEC/task rows are the workspace source of truth; document mirror rows are derived local history/search data.
- `docs/PLAN.md` bootstraps the first product direction. After SQLite workspace SPEC exists, planning treats it as source of truth and keeps `docs/PLAN.md` as background product vision.
- Full run orchestration appends SQLite task state during planning, runs implementation and validation, then writes SQLite SPEC state only when validation passes through result-based SPEC reconciliation. A successful run in a clean git workspace commits validated source/doc changes, while leaving `.jj/documents.sqlite3` and `.jj/runs/` uncommitted; dirty-before-run workspaces skip commit. Dry-runs keep the planned after-state in run snapshots without mutating workspace state.
- SQLite task state is append-only task proposal history: every run appends fresh task records, full runs select the first newly proposed task, and previous `active` or `in_progress` tasks are returned to `queued`.
- Dashboard communication separates workspace task state from run evidence. Workspace task state is product work in `.jj/documents.sqlite3`, exposed through the virtual `.jj/tasks.json` route; `docs/TASK.md` is human-maintained product documentation. Run evidence is the artifacts, validation, summaries, and logs stored under `.jj/runs/<run-id>/`.
- When jj is used to build jj itself, workspace tasks are tasks for the jj product and run logs/artifacts are evidence from the jj execution that planned or implemented those tasks.
- Dashboard routes are intentionally narrow: `README.md`, `docs/PLAN.md`, `docs/PRD.md`, `docs/SPEC.md`, `docs/TASK.md`, SQLite-backed `.jj/spec.json`/`.jj/tasks.json` virtual views, and manifest-listed run artifacts.
- Dashboard project routes group the served workspace and sanitized repository URLs from run history as projects, but never browse outside the served workspace to load external repo docs.
- Run inspection routes are intentionally manifest-derived: `/runs`, `/runs/<run-id>`, `/runs/compare?left=<run-id>&right=<run-id>`, and `/runs/audit?run=<run-id>`.
- Dashboard display paths use `[workspace]`, `.jj/runs/<run-id>`, and `[path]` instead of raw absolute workspace paths.
- Limitation: redaction is best-effort pattern and configured-secret matching; it is not a substitute for avoiding intentional persistence of unnecessary raw credentials.

## Verification Checklist

- [x] `go test ./...`
- [x] `go vet ./...`
- [x] `go build -o jj ./cmd/jj`
- [x] `./scripts/validate.sh`

## Remaining Follow-Up Candidates

- Add a public security architecture note if future users need deeper threat-model documentation.
- Consider an opt-in debug mode for local absolute paths if a user explicitly needs them during dashboard troubleshooting.
