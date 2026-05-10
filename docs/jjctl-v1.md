# jjctl v1

`jjctl` is the local-first CLI for registering GitHub repositories, running Codex in a checked-out repository, and deploying registered repositories to Kubernetes deployment pools.

## Local State

- Global home defaults to `~/.jj` and can be overridden with `JJ_HOME` or `--home`.
- SQLite state is stored at `~/.jj/jj.sqlite`.
- GitHub tokens, kubeconfigs, and registry credentials are stored as encrypted local secret references, not as plaintext SQLite values.
- Repository-local files are `.jj/config.json`, `.jj/docs-manifest.json`, `AGENTS.md`, `jj.deploy.yaml`, and `docs/`.

## Implemented Commands

```bash
jjctl auth login
jjctl auth status
jjctl auth logout
jjctl repo add <owner/repo|.>
jjctl repo list
jjctl repo status
jjctl repo remove <owner/repo>
jjctl repo sync
jjctl docs init
jjctl docs status
jjctl docs validate
jjctl ask "<request>"
jjctl k8s credential add <name> --from-kubeconfig <path> --context <context> --namespace <namespace>
jjctl k8s credential list
jjctl k8s credential show <name>
jjctl k8s credential verify <name>
jjctl k8s credential remove <name>
jjctl k8s key add <name> --from-kubeconfig <path> --context <context> --namespace <namespace>
jjctl registry add <name> --url <registry-url>
jjctl registry list
jjctl registry show <name>
jjctl registry verify <name>
jjctl registry remove <name>
jjctl pool create <name>
jjctl pool list
jjctl pool show <name>
jjctl pool remove <name>
jjctl pool target add <pool>/<target> --credential <name> --namespace <namespace> --environment <env> --strategy <apply-only|build-push-apply>
jjctl deploy init
jjctl deploy plan <target>
jjctl deploy run <target>
jjctl deploy status
jjctl deploy history
jjctl deploy logs <deployment-id>
jjctl deploy rollback-plan <deployment-id>
jjctl doctor
jjctl codex doctor
jjctl k8s doctor
```

## Safety Rules

- `jjctl ask` never commits, pushes, creates PRs, or deploys automatically.
- `jjctl deploy plan` records a plan but does not apply resources.
- `jjctl deploy run` requires explicit approval at `배포를 진행할까요? [y/N]`; the default is `N`.
- `rollback-plan` is read-only in v1.
- Commands do not print tokens, kubeconfig content, certificates, registry credentials, or SSH private keys.
