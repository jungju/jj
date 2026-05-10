# AGENTS.md

## Product

This repository is managed by jj.

jj is a local, document-first AI coding workflow CLI.
It uses Codex for local, validation-gated development assistance and auditable run artifacts.

## Working rules

- Before making changes, explain the plan.
- Prefer minimal, reviewable changes.
- Do not overwrite existing files unless explicitly requested.
- Do not read, print, or expose secrets.
- For jj development tasks in this repository, finish the task with a local git commit after the relevant validation passes, unless the user explicitly asks not to commit.
- Do not push automatically unless the user explicitly asks.
- Do not create a pull request automatically unless the user explicitly asks.
- When changing public behavior, update docs/.
- After code changes, run the smallest relevant test command.
- Summarize changed files after every task.

## jj development flow

This section applies when an agent is working on jj itself in this repository.

- Start by checking the current git status and identifying pre-existing user changes.
- Make a short plan, then implement the smallest coherent change that satisfies the request.
- Keep unrelated files out of scope. If unrelated dirty files already exist, leave them untouched.
- If code behavior changes, update the matching README or docs page in the same task.
- Run the smallest relevant tests first. For broad changes, run `go test ./...`; before release-like handoff, run `./scripts/validate.sh`.
- Review the final diff before committing.
- Commit only the files changed for the current task. Do not include unrelated pre-existing changes.
- Use a clear commit message that describes the user-visible or developer-visible change.
- End the response with implemented features, changed files, tests run, commit SHA, and remaining work.

## Docs rules

- Keep docs concise and implementation-oriented.
- Treat docs/PRD.md, docs/SPEC.md, docs/TASK.md, and README.md as the current product boundary.
- Do not introduce new runtime surfaces that conflict with those documents.
- Keep docs/ changes synchronized with public behavior changes.

## Git rules

- Never force-push.
- Never merge without user approval.
- Do not modify unrelated files.
- Do not amend, squash, rebase, or rewrite history unless explicitly requested.
- Do not push unless explicitly requested.
- If the worktree was dirty before the task and the new changes cannot be separated safely, stop before commit and explain the blocker.
