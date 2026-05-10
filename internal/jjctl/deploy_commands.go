package jjctl

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type deployConfig struct {
	Version int `yaml:"version"`
	Repo    struct {
		Provider string `yaml:"provider"`
		FullName string `yaml:"full_name"`
	} `yaml:"repo"`
	Deploy struct {
		App     string               `yaml:"app"`
		Targets []deployConfigTarget `yaml:"targets"`
	} `yaml:"deploy"`
}

type deployConfigTarget struct {
	Name        string `yaml:"name"`
	Pool        string `yaml:"pool"`
	Environment string `yaml:"environment"`
	Namespace   string `yaml:"namespace"`
	Strategy    string `yaml:"strategy"`
	Image       struct {
		Registry   string `yaml:"registry,omitempty"`
		Repository string `yaml:"repository,omitempty"`
		Tag        string `yaml:"tag,omitempty"`
	} `yaml:"image,omitempty"`
	Build struct {
		Context    string `yaml:"context,omitempty"`
		Dockerfile string `yaml:"dockerfile,omitempty"`
	} `yaml:"build,omitempty"`
	Manifests struct {
		Type string `yaml:"type"`
		Path string `yaml:"path"`
	} `yaml:"manifests"`
	Rollout struct {
		Kind           string `yaml:"kind"`
		Name           string `yaml:"name"`
		TimeoutSeconds int    `yaml:"timeout_seconds"`
	} `yaml:"rollout"`
}

type deployPlan struct {
	Repo         CurrentRepositoryRecord
	Config       deployConfig
	ConfigPath   string
	ConfigTarget deployConfigTarget
	Target       targetRecord
	ImageRef     string
	ManifestPath string
}

func newDeployCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "deploy", Short: "Plan and run Kubernetes deployments"}
	root.AddCommand(newDeployInitCommand(cc))
	root.AddCommand(newDeployConfigCommand(cc))
	root.AddCommand(newDeployPlanCommand(cc))
	root.AddCommand(newDeployRunCommand(cc))
	root.AddCommand(newDeployStatusCommand(cc))
	root.AddCommand(newDeployHistoryCommand(cc))
	root.AddCommand(newDeployLogsCommand(cc))
	root.AddCommand(newDeployRollbackPlanCommand(cc))
	return root
}

func newDeployInitCommand(cc *commandContext) *cobra.Command {
	var poolName string
	var targetName string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create jj.deploy.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			repo, err := app.CurrentRepository(cmd.Context())
			if err != nil {
				return err
			}
			var target targetRecord
			if poolName != "" || targetName != "" {
				if poolName == "" || targetName == "" {
					return fmt.Errorf("--pool and --target must be used together")
				}
				target, err = app.LoadTarget(cmd.Context(), poolName+"/"+targetName)
				if err != nil {
					return err
				}
			}
			path := filepath.Join(repo.LocalPath, "jj.deploy.yaml")
			if fileExists(path) {
				fmt.Fprintf(cc.stdout, "jj.deploy.yaml already exists: %s\n", path)
				return nil
			}
			cfg := defaultDeployConfig(repo.FullName, target)
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return err
			}
			now := app.timestamp()
			var defaultPool any
			var defaultTarget any
			if target.ID != "" {
				defaultPool = target.PoolID
				defaultTarget = target.ID
			}
			_, err = app.DB.ExecContext(cmd.Context(), `INSERT INTO repo_deployment_configs (
  id, user_id, repository_id, config_path, default_pool_id, default_target_id, created_at, updated_at
) VALUES (?, ?, ?, 'jj.deploy.yaml', ?, ?, ?, ?)
ON CONFLICT(user_id, repository_id) DO UPDATE SET
  default_pool_id = excluded.default_pool_id,
  default_target_id = excluded.default_target_id,
  updated_at = excluded.updated_at`,
				newID("rdc"), repo.UserID, repo.ID, defaultPool, defaultTarget, now, now)
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ jj.deploy.yaml 생성 완료: %s\n", path)
			fmt.Fprintln(cc.stdout, "Dockerfile, Kubernetes manifest, registry 설정을 확인하세요.")
			return nil
		},
	}
	cmd.Flags().StringVar(&poolName, "pool", "", "default deployment pool")
	cmd.Flags().StringVar(&targetName, "target", "", "default deployment target")
	return cmd
}

func newDeployConfigCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print jj.deploy.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := ensureInsideRepo(currentWorkingDirectory())
			if err != nil {
				return err
			}
			path := filepath.Join(repoRoot, "jj.deploy.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				return CodeError{Code: ErrDeployConfigNotFound, Message: "jj.deploy.yaml을 찾을 수 없습니다.", Remedy: "jjctl deploy init을 실행하세요.", Err: err}
			}
			fmt.Fprint(cc.stdout, string(data))
			return nil
		},
	}
}

func newDeployPlanCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "plan <target>",
		Short: "Show deployment plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			plan, err := app.BuildDeployPlan(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printDeployPlan(cc.stdout, plan)
			if err := app.recordDeploymentPlan(cmd.Context(), plan, "planned", "", ""); err != nil {
				return err
			}
			return app.Audit(cmd.Context(), plan.Repo.UserID, "deploy.plan", "repository", plan.Repo.ID, map[string]any{"target": plan.Target.PoolName + "/" + plan.Target.Name})
		},
	}
}

func newDeployRunCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "run <target>",
		Short: "Run deployment after user approval",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			plan, err := app.BuildDeployPlan(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printDeployPlan(cc.stdout, plan)
			deploymentID, err := app.startDeployment(cmd.Context(), plan)
			if err != nil {
				return err
			}
			if err := app.runDeploymentSteps(cmd.Context(), plan, deploymentID, cc.stdout); err != nil {
				_ = app.finishDeployment(cmd.Context(), deploymentID, "failed", "", err)
				return err
			}
			if !confirmDeploy(cc.stdout) {
				_ = app.finishDeployment(cmd.Context(), deploymentID, "cancelled", ErrDeployUserCancelled, nil)
				return CodeError{Code: ErrDeployUserCancelled, Message: "사용자가 배포를 취소했습니다.", Remedy: "다시 실행하려면 jjctl deploy run <target>을 사용하세요."}
			}
			if err := app.kubectlApply(cmd.Context(), plan, deploymentID, cc.stdout); err != nil {
				_ = app.finishDeployment(cmd.Context(), deploymentID, "failed", "", err)
				return err
			}
			if err := app.finishDeployment(cmd.Context(), deploymentID, "succeeded", "", nil); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "\nDeployment: %s\nStatus:     succeeded\nImage:      %s\nNamespace:  %s\n", deploymentID, plan.ImageRef, plan.Target.Namespace)
			return app.Audit(cmd.Context(), plan.Repo.UserID, "deploy.run", "deployment", deploymentID, map[string]any{"status": "succeeded"})
		},
	}
}

func newDeployStatusCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show latest deployment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			repo, err := app.CurrentRepository(cmd.Context())
			if err != nil {
				return err
			}
			var id, status, strategy, namespace, started string
			err = app.DB.QueryRowContext(cmd.Context(), `SELECT id, status, strategy, namespace, started_at
FROM deployments WHERE repository_id = ? ORDER BY started_at DESC LIMIT 1`, repo.ID).Scan(&id, &status, &strategy, &namespace, &started)
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintln(cc.stdout, "배포 이력이 없습니다.")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "Deployment: %s\nStatus:     %s\nStrategy:   %s\nNamespace:  %s\nStarted:    %s\n", id, status, strategy, namespace, started)
			return nil
		},
	}
}

func newDeployHistoryCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "List deployment history",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			repo, err := app.CurrentRepository(cmd.Context())
			if err != nil {
				return err
			}
			rows, err := app.DB.QueryContext(cmd.Context(), `SELECT id, status, strategy, COALESCE(image_ref, ''), namespace, started_at
FROM deployments WHERE repository_id = ? ORDER BY started_at DESC LIMIT 20`, repo.ID)
			if err != nil {
				return err
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				found = true
				var id, status, strategy, image, namespace, started string
				if err := rows.Scan(&id, &status, &strategy, &image, &namespace, &started); err != nil {
					return err
				}
				fmt.Fprintf(cc.stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", id, status, strategy, image, namespace, started)
			}
			if !found {
				fmt.Fprintln(cc.stdout, "배포 이력이 없습니다.")
			}
			return rows.Err()
		},
	}
}

func newDeployLogsCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "logs <deployment-id>",
		Short: "Show deployment events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			rows, err := app.DB.QueryContext(cmd.Context(), `SELECT event_type, COALESCE(message, ''), created_at
FROM deployment_events WHERE deployment_id = ? ORDER BY created_at`, args[0])
			if err != nil {
				return err
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				found = true
				var eventType, message, created string
				if err := rows.Scan(&eventType, &message, &created); err != nil {
					return err
				}
				fmt.Fprintf(cc.stdout, "%s\t%s\t%s\n", created, eventType, message)
			}
			if !found {
				fmt.Fprintln(cc.stdout, "deployment event가 없습니다.")
			}
			return rows.Err()
		},
	}
}

func newDeployRollbackPlanCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback-plan <deployment-id>",
		Short: "Show rollback information without executing rollback",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			var repoID, started string
			err = app.DB.QueryRowContext(cmd.Context(), "SELECT repository_id, started_at FROM deployments WHERE id = ?", args[0]).Scan(&repoID, &started)
			if errors.Is(err, sql.ErrNoRows) {
				return CodeError{Code: ErrDeployRollbackUnsupported, Message: "기준 deployment를 찾을 수 없습니다.", Remedy: "jjctl deploy history로 deployment id를 확인하세요."}
			}
			if err != nil {
				return err
			}
			var id, status, image, namespace string
			err = app.DB.QueryRowContext(cmd.Context(), `SELECT id, status, COALESCE(image_ref, ''), namespace
FROM deployments
WHERE repository_id = ? AND started_at < ?
ORDER BY started_at DESC LIMIT 1`, repoID, started).Scan(&id, &status, &image, &namespace)
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintln(cc.stdout, "이전 deployment가 없습니다. 자동 rollback은 v1에서 지원하지 않습니다.")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cc.stdout, "Rollback Plan (read-only)")
			fmt.Fprintf(cc.stdout, "Previous deployment: %s\nStatus:              %s\nImage:               %s\nNamespace:           %s\n", id, status, image, namespace)
			fmt.Fprintln(cc.stdout, "v1은 자동 rollback을 실행하지 않습니다.")
			return nil
		},
	}
}

func defaultDeployConfig(fullName string, target targetRecord) deployConfig {
	var cfg deployConfig
	cfg.Version = 1
	cfg.Repo.Provider = "github"
	cfg.Repo.FullName = fullName
	appName := fullName
	if _, name, err := parseGitHubFullName(fullName); err == nil {
		appName = name
	}
	cfg.Deploy.App = appName
	if target.Name == "" {
		target.Name = "dev"
		target.PoolName = "personal-dev"
		target.Environment = "dev"
		target.Namespace = "default"
		target.Strategy = "apply-only"
	}
	item := deployConfigTarget{
		Name:        target.Name,
		Pool:        target.PoolName,
		Environment: target.Environment,
		Namespace:   target.Namespace,
		Strategy:    target.Strategy,
	}
	item.Manifests.Type = "directory"
	item.Manifests.Path = "k8s/" + target.Name
	item.Rollout.Kind = "deployment"
	item.Rollout.Name = appName
	item.Rollout.TimeoutSeconds = 180
	if target.Strategy == "build-push-apply" {
		item.Image.Registry = "ghcr"
		item.Image.Repository = "ghcr.io/" + fullName
		item.Image.Tag = "git-sha"
		item.Build.Context = "."
		item.Build.Dockerfile = "Dockerfile"
		item.Manifests.Type = "kustomize"
		item.Manifests.Path = "k8s/overlays/" + target.Name
	}
	cfg.Deploy.Targets = []deployConfigTarget{item}
	return cfg
}

func (a *App) BuildDeployPlan(ctx context.Context, targetArg string) (deployPlan, error) {
	repo, err := a.CurrentRepository(ctx)
	if err != nil {
		return deployPlan{}, err
	}
	path := filepath.Join(repo.LocalPath, "jj.deploy.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return deployPlan{}, CodeError{Code: ErrDeployConfigNotFound, Message: "jj.deploy.yaml을 찾을 수 없습니다.", Remedy: "jjctl deploy init을 실행하세요.", Err: err}
	}
	var cfg deployConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return deployPlan{}, CodeError{Code: ErrDeployConfigInvalid, Message: "jj.deploy.yaml 파싱에 실패했습니다.", Err: err}
	}
	cfgTarget, err := selectDeployConfigTarget(cfg, targetArg)
	if err != nil {
		return deployPlan{}, err
	}
	target, err := a.LoadTarget(ctx, cfgTarget.Pool+"/"+cfgTarget.Name)
	if err != nil {
		return deployPlan{}, err
	}
	if target.Strategy != cfgTarget.Strategy {
		return deployPlan{}, CodeError{Code: ErrDeployConfigInvalid, Message: "jj.deploy.yaml 전략과 deployment target 전략이 다릅니다.", Remedy: "jj.deploy.yaml 또는 pool target 설정을 맞추세요."}
	}
	manifestPath := filepath.Join(repo.LocalPath, cfgTarget.Manifests.Path)
	if !fileExists(manifestPath) {
		return deployPlan{}, CodeError{Code: ErrDeployManifestNotFound, Message: "manifest path를 찾을 수 없습니다.", Remedy: "jj.deploy.yaml의 manifests.path를 확인하세요."}
	}
	imageRef := ""
	if cfgTarget.Strategy == "build-push-apply" {
		tag := cfgTarget.Image.Tag
		if tag == "" || tag == "git-sha" {
			sha := gitCommitSHA(ctx, repo.LocalPath)
			if len(sha) > 12 {
				sha = sha[:12]
			}
			tag = sha
		}
		imageRef = cfgTarget.Image.Repository + ":" + tag
	}
	return deployPlan{Repo: repo, Config: cfg, ConfigPath: path, ConfigTarget: cfgTarget, Target: target, ImageRef: imageRef, ManifestPath: manifestPath}, nil
}

func selectDeployConfigTarget(cfg deployConfig, targetArg string) (deployConfigTarget, error) {
	poolName := ""
	targetName := targetArg
	if strings.Contains(targetArg, "/") {
		var err error
		poolName, targetName, err = parsePoolTarget(targetArg)
		if err != nil {
			return deployConfigTarget{}, err
		}
	}
	for _, target := range cfg.Deploy.Targets {
		if target.Name == targetName && (poolName == "" || target.Pool == poolName) {
			if target.Strategy != "apply-only" && target.Strategy != "build-push-apply" {
				return deployConfigTarget{}, CodeError{Code: ErrDeployStrategyUnsupported, Message: "지원하지 않는 배포 전략입니다.", Remedy: "apply-only 또는 build-push-apply를 사용하세요."}
			}
			if target.Manifests.Path == "" {
				return deployConfigTarget{}, CodeError{Code: ErrDeployConfigInvalid, Message: "manifests.path가 필요합니다."}
			}
			return target, nil
		}
	}
	return deployConfigTarget{}, CodeError{Code: ErrPoolTargetNotFound, Message: "jj.deploy.yaml에서 target을 찾을 수 없습니다.", Remedy: "jjctl deploy config로 targets를 확인하세요."}
}

func printDeployPlan(stdout anyWriter, plan deployPlan) {
	fmt.Fprintln(stdout, "Deployment Plan")
	fmt.Fprintf(stdout, "\nRepo:       %s\n", plan.Repo.FullName)
	fmt.Fprintf(stdout, "Target:     %s/%s\n", plan.Target.PoolName, plan.Target.Name)
	fmt.Fprintf(stdout, "Namespace:  %s\n", plan.Target.Namespace)
	fmt.Fprintf(stdout, "Strategy:   %s\n", plan.ConfigTarget.Strategy)
	if plan.ImageRef != "" {
		fmt.Fprintf(stdout, "Image:      %s\n", plan.ImageRef)
	}
	fmt.Fprintln(stdout, "\n실행 예정:")
	steps := []string{"manifest render", "kubectl diff", "kubectl apply", "rollout status 확인"}
	if plan.ConfigTarget.Strategy == "build-push-apply" {
		steps = []string{"docker build", "docker push", "manifest render", "kubectl diff", "kubectl apply", "rollout status 확인"}
	}
	for i, step := range steps {
		fmt.Fprintf(stdout, "%d. %s\n", i+1, step)
	}
	fmt.Fprintf(stdout, "\nManifest: %s\n", plan.ConfigTarget.Manifests.Path)
}

func (a *App) recordDeploymentPlan(ctx context.Context, plan deployPlan, status, diffSummary, errorMessage string) error {
	_, err := a.DB.ExecContext(ctx, `INSERT INTO deployments (
  id, user_id, repository_id, pool_id, target_id, status, strategy, git_branch, git_commit_sha,
  image_ref, manifest_path, namespace, plan_summary, diff_summary, started_at, completed_at, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID("dep"), plan.Repo.UserID, plan.Repo.ID, plan.Target.PoolID, plan.Target.ID, status, plan.ConfigTarget.Strategy, gitCurrentBranch(ctx, plan.Repo.LocalPath), gitCommitSHA(ctx, plan.Repo.LocalPath), nullable(plan.ImageRef), plan.ConfigTarget.Manifests.Path, plan.Target.Namespace, "plan generated", nullable(diffSummary), a.timestamp(), a.timestamp(), nullable(errorMessage))
	return err
}

func (a *App) startDeployment(ctx context.Context, plan deployPlan) (string, error) {
	id := newID("dep")
	_, err := a.DB.ExecContext(ctx, `INSERT INTO deployments (
  id, user_id, repository_id, pool_id, target_id, status, strategy, git_branch, git_commit_sha,
  image_ref, manifest_path, namespace, plan_summary, started_at
) VALUES (?, ?, ?, ?, ?, 'running', ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, plan.Repo.UserID, plan.Repo.ID, plan.Target.PoolID, plan.Target.ID, plan.ConfigTarget.Strategy, gitCurrentBranch(ctx, plan.Repo.LocalPath), gitCommitSHA(ctx, plan.Repo.LocalPath), nullable(plan.ImageRef), plan.ConfigTarget.Manifests.Path, plan.Target.Namespace, "deployment started", a.timestamp())
	if err != nil {
		return "", err
	}
	return id, a.addDeploymentEvent(ctx, id, "credential_verified", "credential selected", nil)
}

func (a *App) runDeploymentSteps(ctx context.Context, plan deployPlan, deploymentID string, stdout anyWriter) error {
	if err := a.VerifyK8SCredential(ctx, plan.Target.CredentialName, plan.Target.Namespace, stdout); err != nil {
		return err
	}
	if err := a.addDeploymentEvent(ctx, deploymentID, "permission_checked", "kubernetes permissions checked", nil); err != nil {
		return err
	}
	if gitWorkingTreeDirty(ctx, plan.Repo.LocalPath) {
		fmt.Fprintln(stdout, "! working tree가 dirty입니다. 현재 상태 그대로 배포 계획을 진행합니다.")
	}
	if plan.ConfigTarget.Strategy == "build-push-apply" {
		if plan.ImageRef == "" {
			return CodeError{Code: ErrDeployConfigInvalid, Message: "image repository 설정이 필요합니다.", Remedy: "jj.deploy.yaml deploy.targets[].image.repository를 설정하세요."}
		}
		if plan.ConfigTarget.Build.Dockerfile == "" {
			return CodeError{Code: ErrDeployConfigInvalid, Message: "Dockerfile 설정이 필요합니다."}
		}
		buildContext := filepath.Join(plan.Repo.LocalPath, firstNonEmpty(plan.ConfigTarget.Build.Context, "."))
		dockerfile := filepath.Join(plan.Repo.LocalPath, plan.ConfigTarget.Build.Dockerfile)
		if !fileExists(dockerfile) {
			return CodeError{Code: ErrDeployConfigInvalid, Message: "Dockerfile을 찾을 수 없습니다.", Remedy: "Dockerfile을 만들거나 jj.deploy.yaml build.dockerfile을 수정하세요."}
		}
		fmt.Fprintf(stdout, "docker build: %s\n", plan.ImageRef)
		if _, err := runCommand(ctx, plan.Repo.LocalPath, "docker", "build", "-t", plan.ImageRef, "-f", dockerfile, buildContext); err != nil {
			return CodeError{Code: ErrImageBuildFailed, Message: "image build에 실패했습니다.", Err: err}
		}
		if err := a.addDeploymentEvent(ctx, deploymentID, "image_built", plan.ImageRef, nil); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "docker push: %s\n", plan.ImageRef)
		if _, err := runCommand(ctx, plan.Repo.LocalPath, "docker", "push", plan.ImageRef); err != nil {
			return CodeError{Code: ErrImagePushFailed, Message: "image push에 실패했습니다.", Err: err}
		}
		if err := a.addDeploymentEvent(ctx, deploymentID, "image_pushed", plan.ImageRef, nil); err != nil {
			return err
		}
	}
	if err := a.addDeploymentEvent(ctx, deploymentID, "manifest_rendered", plan.ConfigTarget.Manifests.Path, nil); err != nil {
		return err
	}
	diff, err := a.kubectlDiff(ctx, plan)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "\nKubernetes diff:")
	if strings.TrimSpace(diff) == "" {
		fmt.Fprintln(stdout, "(no diff output)")
	} else {
		fmt.Fprintln(stdout, strings.TrimSpace(diff))
	}
	if _, err := a.DB.ExecContext(ctx, "UPDATE deployments SET diff_summary = ? WHERE id = ?", strings.TrimSpace(diff), deploymentID); err != nil {
		return err
	}
	return a.addDeploymentEvent(ctx, deploymentID, "diff_generated", "kubectl diff completed", nil)
}

func (a *App) kubectlDiff(ctx context.Context, plan deployPlan) (string, error) {
	cred, err := a.LoadK8SCredential(ctx, plan.Target.CredentialName)
	if err != nil {
		return "", err
	}
	kubeconfig, err := a.Secrets.Load(cred.CredentialRef)
	if err != nil {
		return "", CodeError{Code: ErrK8SCredentialInvalid, Message: "저장된 kubeconfig를 읽을 수 없습니다.", Err: err}
	}
	tmp, cleanup, err := writeTempSecretFile("jj-kubeconfig-*", kubeconfig)
	if err != nil {
		return "", err
	}
	defer cleanup()
	args := kubectlManifestArgs("diff", tmp, cred.ContextName, plan.Target.Namespace, plan.ConfigTarget)
	out, err := runCommand(ctx, plan.Repo.LocalPath, "kubectl", args...)
	if err != nil {
		return out, CodeError{Code: ErrDeployDiffFailed, Message: "kubectl diff에 실패했습니다.", Remedy: "manifest와 Kubernetes 권한을 확인하세요.", Err: err}
	}
	return out, nil
}

func (a *App) kubectlApply(ctx context.Context, plan deployPlan, deploymentID string, stdout anyWriter) error {
	cred, err := a.LoadK8SCredential(ctx, plan.Target.CredentialName)
	if err != nil {
		return err
	}
	kubeconfig, err := a.Secrets.Load(cred.CredentialRef)
	if err != nil {
		return CodeError{Code: ErrK8SCredentialInvalid, Message: "저장된 kubeconfig를 읽을 수 없습니다.", Err: err}
	}
	tmp, cleanup, err := writeTempSecretFile("jj-kubeconfig-*", kubeconfig)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := a.addDeploymentEvent(ctx, deploymentID, "user_approved", "user approved kubectl apply", nil); err != nil {
		return err
	}
	if err := a.addDeploymentEvent(ctx, deploymentID, "kubectl_apply_started", plan.ConfigTarget.Manifests.Path, nil); err != nil {
		return err
	}
	args := kubectlManifestArgs("apply", tmp, cred.ContextName, plan.Target.Namespace, plan.ConfigTarget)
	out, err := runCommand(ctx, plan.Repo.LocalPath, "kubectl", args...)
	if strings.TrimSpace(out) != "" {
		fmt.Fprintln(stdout, strings.TrimSpace(out))
	}
	if err != nil {
		return CodeError{Code: ErrDeployApplyFailed, Message: "kubectl apply에 실패했습니다.", Err: err}
	}
	if err := a.addDeploymentEvent(ctx, deploymentID, "kubectl_apply_finished", "kubectl apply completed", nil); err != nil {
		return err
	}
	if plan.ConfigTarget.Rollout.Kind != "" && plan.ConfigTarget.Rollout.Name != "" {
		timeout := fmt.Sprintf("%ds", plan.ConfigTarget.Rollout.TimeoutSeconds)
		if plan.ConfigTarget.Rollout.TimeoutSeconds <= 0 {
			timeout = "180s"
		}
		rolloutArgs := []string{"--kubeconfig", tmp, "--context", cred.ContextName, "-n", plan.Target.Namespace, "rollout", "status", strings.ToLower(plan.ConfigTarget.Rollout.Kind) + "/" + plan.ConfigTarget.Rollout.Name, "--timeout", timeout}
		out, err := runCommand(ctx, plan.Repo.LocalPath, "kubectl", rolloutArgs...)
		if strings.TrimSpace(out) != "" {
			fmt.Fprintln(stdout, strings.TrimSpace(out))
		}
		if err != nil {
			_ = a.addDeploymentEvent(ctx, deploymentID, "rollout_failed", err.Error(), nil)
			return CodeError{Code: "DEPLOY_ROLLOUT_FAILED", Message: "rollout status 확인에 실패했습니다.", Err: err}
		}
		if err := a.addDeploymentEvent(ctx, deploymentID, "rollout_succeeded", plan.ConfigTarget.Rollout.Name, nil); err != nil {
			return err
		}
	}
	return nil
}

func kubectlManifestArgs(action, kubeconfig, contextName, namespace string, target deployConfigTarget) []string {
	args := []string{"--kubeconfig", kubeconfig, "--context", contextName, "-n", namespace, action}
	if target.Manifests.Type == "kustomize" {
		args = append(args, "-k", target.Manifests.Path)
	} else {
		args = append(args, "-f", target.Manifests.Path)
	}
	return args
}

func confirmDeploy(stdout anyWriter) bool {
	fmt.Fprint(stdout, "\n배포를 진행할까요? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func (a *App) finishDeployment(ctx context.Context, deploymentID, status, errorCode string, cause error) error {
	var errMsg any
	var code any
	if cause != nil {
		errMsg = cause.Error()
		var codeErr CodeError
		if errors.As(cause, &codeErr) && codeErr.Code != "" {
			code = codeErr.Code
		} else if errorCode != "" {
			code = errorCode
		}
	} else if errorCode != "" {
		code = errorCode
	}
	_, err := a.DB.ExecContext(ctx, "UPDATE deployments SET status = ?, completed_at = ?, error_code = ?, error_message = ? WHERE id = ?", status, a.timestamp(), code, errMsg, deploymentID)
	return err
}

func (a *App) addDeploymentEvent(ctx context.Context, deploymentID, eventType, message string, metadata any) error {
	var metadataJSON any
	if metadata != nil {
		data, err := yaml.Marshal(metadata)
		if err != nil {
			return err
		}
		metadataJSON = string(data)
	}
	_, err := a.DB.ExecContext(ctx, `INSERT INTO deployment_events (
  id, deployment_id, event_type, message, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?)`,
		newID("evt"), deploymentID, eventType, nullable(message), metadataJSON, a.timestamp())
	return err
}
