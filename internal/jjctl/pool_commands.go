package jjctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newPoolCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "pool", Short: "Manage deployment pools"}
	root.AddCommand(newPoolCreateCommand(cc))
	root.AddCommand(newPoolListCommand(cc))
	root.AddCommand(newPoolShowCommand(cc))
	root.AddCommand(newPoolRemoveCommand(cc))
	target := &cobra.Command{Use: "target", Short: "Manage deployment targets"}
	target.AddCommand(newPoolTargetAddCommand(cc))
	target.AddCommand(newPoolTargetListCommand(cc))
	target.AddCommand(newPoolTargetShowCommand(cc))
	target.AddCommand(newPoolTargetVerifyCommand(cc))
	target.AddCommand(newPoolTargetRemoveCommand(cc))
	root.AddCommand(target)
	return root
}

func newPoolCreateCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create deployment pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			account, err := app.CurrentAccount(cmd.Context())
			if err != nil {
				return err
			}
			now := app.timestamp()
			_, err = app.DB.ExecContext(cmd.Context(), `INSERT INTO deployment_pools (id, user_id, name, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(user_id, name) DO UPDATE SET deleted_at = NULL, updated_at = excluded.updated_at`,
				newID("pool"), account.UserID, args[0], now, now)
			if err != nil {
				return err
			}
			if err := app.Audit(cmd.Context(), account.UserID, "pool.create", "deployment_pool", args[0], nil); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ deployment pool 생성 완료: %s\n", args[0])
			return nil
		},
	}
}

func newPoolListCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List deployment pools",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			account, err := app.CurrentAccount(cmd.Context())
			if err != nil {
				return err
			}
			rows, err := app.DB.QueryContext(cmd.Context(), `SELECT p.name, COUNT(t.id)
FROM deployment_pools p
LEFT JOIN deployment_targets t ON t.pool_id = p.id AND t.deleted_at IS NULL
WHERE p.user_id = ? AND p.deleted_at IS NULL
GROUP BY p.id, p.name ORDER BY p.name`, account.UserID)
			if err != nil {
				return err
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				found = true
				var name string
				var count int
				if err := rows.Scan(&name, &count); err != nil {
					return err
				}
				fmt.Fprintf(cc.stdout, "%s\t%d targets\n", name, count)
			}
			if !found {
				fmt.Fprintln(cc.stdout, "등록된 deployment pool이 없습니다.")
			}
			return rows.Err()
		},
	}
}

func newPoolShowCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show deployment pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			pool, err := app.LoadPool(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "%s\n", pool.Name)
			return listTargets(cmd.Context(), app, cc.stdout, pool.ID)
		},
	}
}

func newPoolRemoveCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove deployment pool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			account, err := app.CurrentAccount(cmd.Context())
			if err != nil {
				return err
			}
			affected, err := softDeleteByName(cmd.Context(), app.DB, "deployment_pools", account.UserID, args[0], app.timestamp())
			if err != nil {
				return err
			}
			if affected == 0 {
				return CodeError{Code: ErrPoolNotFound, Message: "deployment pool을 찾을 수 없습니다.", Remedy: "jjctl pool list로 이름을 확인하세요."}
			}
			fmt.Fprintf(cc.stdout, "✓ deployment pool 제거 완료: %s\n", args[0])
			return nil
		},
	}
}

func newPoolTargetAddCommand(cc *commandContext) *cobra.Command {
	var credential string
	var namespace string
	var environment string
	var strategy string
	cmd := &cobra.Command{
		Use:   "add <pool>/<target>",
		Short: "Add deployment target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			poolName, targetName, err := parsePoolTarget(args[0])
			if err != nil {
				return err
			}
			pool, err := app.LoadPool(cmd.Context(), poolName)
			if err != nil {
				return err
			}
			cred, err := app.LoadK8SCredential(cmd.Context(), credential)
			if err != nil {
				return err
			}
			if strategy != "apply-only" && strategy != "build-push-apply" {
				return CodeError{Code: ErrDeployStrategyUnsupported, Message: "지원하지 않는 배포 전략입니다.", Remedy: "apply-only 또는 build-push-apply를 사용하세요."}
			}
			now := app.timestamp()
			_, err = app.DB.ExecContext(cmd.Context(), `INSERT INTO deployment_targets (
  id, user_id, pool_id, credential_id, name, environment, namespace, deploy_strategy, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(pool_id, name) DO UPDATE SET
  credential_id = excluded.credential_id,
  environment = excluded.environment,
  namespace = excluded.namespace,
  deploy_strategy = excluded.deploy_strategy,
  deleted_at = NULL,
  updated_at = excluded.updated_at`,
				newID("target"), pool.UserID, pool.ID, cred.ID, targetName, environment, namespace, strategy, now, now)
			if err != nil {
				return err
			}
			if err := app.Audit(cmd.Context(), pool.UserID, "pool.target.add", "deployment_target", poolName+"/"+targetName, map[string]any{"credential": credential, "strategy": strategy}); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ deployment target 추가 완료: %s/%s\n", poolName, targetName)
			return nil
		},
	}
	cmd.Flags().StringVar(&credential, "credential", "", "Kubernetes credential name")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace")
	cmd.Flags().StringVar(&environment, "environment", "", "environment label")
	cmd.Flags().StringVar(&strategy, "strategy", "apply-only", "deploy strategy: apply-only or build-push-apply")
	_ = cmd.MarkFlagRequired("credential")
	_ = cmd.MarkFlagRequired("namespace")
	_ = cmd.MarkFlagRequired("environment")
	return cmd
}

func newPoolTargetListCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list <pool>",
		Short: "List deployment targets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			pool, err := app.LoadPool(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return listTargets(cmd.Context(), app, cc.stdout, pool.ID)
		},
	}
}

func newPoolTargetShowCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "show <pool>/<target>",
		Short: "Show deployment target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			target, err := app.LoadTarget(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "Target:      %s/%s\nEnvironment: %s\nNamespace:   %s\nStrategy:    %s\nCredential:  %s\n", target.PoolName, target.Name, target.Environment, target.Namespace, target.Strategy, target.CredentialName)
			return nil
		},
	}
}

func newPoolTargetVerifyCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <pool>/<target>",
		Short: "Verify deployment target permissions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			target, err := app.LoadTarget(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := app.VerifyK8SCredential(cmd.Context(), target.CredentialName, target.Namespace, cc.stdout); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ deployment target 검증 완료: %s/%s\n", target.PoolName, target.Name)
			return nil
		},
	}
}

func newPoolTargetRemoveCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <pool>/<target>",
		Short: "Remove deployment target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			target, err := app.LoadTarget(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			now := app.timestamp()
			if _, err := app.DB.ExecContext(cmd.Context(), "UPDATE deployment_targets SET deleted_at = ?, updated_at = ? WHERE id = ?", now, now, target.ID); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ deployment target 제거 완료: %s/%s\n", target.PoolName, target.Name)
			return nil
		},
	}
}

type poolRecord struct {
	ID     string
	UserID string
	Name   string
}

type targetRecord struct {
	ID             string
	UserID         string
	PoolID         string
	PoolName       string
	Name           string
	Environment    string
	Namespace      string
	Strategy       string
	CredentialID   string
	CredentialName string
}

func (a *App) LoadPool(ctx context.Context, name string) (poolRecord, error) {
	account, err := a.CurrentAccount(ctx)
	if err != nil {
		return poolRecord{}, err
	}
	var pool poolRecord
	err = a.DB.QueryRowContext(ctx, `SELECT id, user_id, name FROM deployment_pools
WHERE user_id = ? AND name = ? AND deleted_at IS NULL`, account.UserID, name).Scan(&pool.ID, &pool.UserID, &pool.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return pool, CodeError{Code: ErrPoolNotFound, Message: "deployment pool을 찾을 수 없습니다.", Remedy: "jjctl pool list로 이름을 확인하세요."}
	}
	return pool, err
}

func (a *App) LoadTarget(ctx context.Context, value string) (targetRecord, error) {
	account, err := a.CurrentAccount(ctx)
	if err != nil {
		return targetRecord{}, err
	}
	poolName, targetName, err := parsePoolTarget(value)
	if err != nil {
		return targetRecord{}, err
	}
	var target targetRecord
	err = a.DB.QueryRowContext(ctx, `SELECT t.id, t.user_id, t.pool_id, p.name, t.name, t.environment, t.namespace, t.deploy_strategy, t.credential_id, c.name
FROM deployment_targets t
JOIN deployment_pools p ON p.id = t.pool_id
JOIN k8s_credentials c ON c.id = t.credential_id
WHERE t.user_id = ? AND p.name = ? AND t.name = ? AND t.deleted_at IS NULL AND p.deleted_at IS NULL`,
		account.UserID, poolName, targetName).Scan(&target.ID, &target.UserID, &target.PoolID, &target.PoolName, &target.Name, &target.Environment, &target.Namespace, &target.Strategy, &target.CredentialID, &target.CredentialName)
	if errors.Is(err, sql.ErrNoRows) {
		return target, CodeError{Code: ErrPoolTargetNotFound, Message: "deployment target을 찾을 수 없습니다.", Remedy: "jjctl pool target list <pool>로 이름을 확인하세요."}
	}
	return target, err
}

func listTargets(ctx context.Context, app *App, stdout anyWriter, poolID string) error {
	rows, err := app.DB.QueryContext(ctx, `SELECT t.name, t.environment, t.namespace, t.deploy_strategy, c.name
FROM deployment_targets t
JOIN k8s_credentials c ON c.id = t.credential_id
WHERE t.pool_id = ? AND t.deleted_at IS NULL
ORDER BY t.name`, poolID)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var name, environment, namespace, strategy, credential string
		if err := rows.Scan(&name, &environment, &namespace, &strategy, &credential); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", name, environment, namespace, strategy, credential)
	}
	if !found {
		fmt.Fprintln(stdout, "등록된 deployment target이 없습니다.")
	}
	return rows.Err()
}
