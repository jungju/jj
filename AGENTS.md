# AGENTS.md

## Product

This repository is managed by jj.

jj is a CLI-first development and deployment tool for GitHub repositories.
It uses Codex for development assistance and Kubernetes deployment pools for deployment.

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
- Use docs/product for product decisions.
- Use docs/architecture for system design.
- Use docs/deployment for deployment behavior.
- Use docs/security for security and permission rules.
- Use docs/adr for irreversible decisions.

## Git rules

- Never force-push.
- Never merge without user approval.
- Do not modify unrelated files.
- Do not amend, squash, rebase, or rewrite history unless explicitly requested.
- Do not push unless explicitly requested.
- If the worktree was dirty before the task and the new changes cannot be separated safely, stop before commit and explain the blocker.

## Deployment rules

- Do not deploy automatically.
- Do not run kubectl apply unless the user explicitly asks.
- Do not print kubeconfig, tokens, certificates, or registry credentials.
- Do not create cluster-wide RBAC resources unless explicitly requested.
- Prefer namespace-scoped manifests.
- Before changing deployment files, update docs/deployment/.
- Before deployment, show the deployment plan and expected resources.
- Production deployment requires explicit user approval.
