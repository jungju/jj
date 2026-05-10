package jjctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newRegistryCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "registry", Short: "Manage image registries"}
	root.AddCommand(newRegistryAddCommand(cc))
	root.AddCommand(newRegistryListCommand(cc))
	root.AddCommand(newRegistryShowCommand(cc))
	root.AddCommand(newRegistryVerifyCommand(cc))
	root.AddCommand(newRegistryRemoveCommand(cc))
	return root
}

func newRegistryAddCommand(cc *commandContext) *cobra.Command {
	var registryURL string
	var tokenEnv string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register an image registry",
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
			if _, err := url.ParseRequestURI(registryURL); err != nil && !strings.Contains(registryURL, ".") {
				return fmt.Errorf("invalid registry url %q", registryURL)
			}
			token := os.Getenv(tokenEnv)
			credentialType := "none"
			if strings.TrimSpace(token) != "" {
				credentialType = "token"
			}
			ref, err := app.Secrets.Save("registry "+args[0], []byte(token), app.timestamp())
			if err != nil {
				return err
			}
			now := app.timestamp()
			_, err = app.DB.ExecContext(cmd.Context(), `INSERT INTO registries (
  id, user_id, name, registry_url, credential_type, credential_ref, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id, name) DO UPDATE SET
  registry_url = excluded.registry_url,
  credential_type = excluded.credential_type,
  credential_ref = excluded.credential_ref,
  deleted_at = NULL,
  updated_at = excluded.updated_at`,
				newID("reg"), account.UserID, args[0], registryURL, credentialType, ref, now, now)
			if err != nil {
				return err
			}
			if err := app.Audit(cmd.Context(), account.UserID, "registry.add", "registry", args[0], map[string]any{"url": registryURL, "credential_type": credentialType}); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ registry 등록 완료: %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&registryURL, "url", "", "registry URL")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "JJ_REGISTRY_TOKEN", "environment variable containing registry token")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newRegistryListCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registries",
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
			rows, err := app.DB.QueryContext(cmd.Context(), `SELECT name, registry_url, credential_type, COALESCE(last_verified_at, '')
FROM registries WHERE user_id = ? AND deleted_at IS NULL ORDER BY name`, account.UserID)
			if err != nil {
				return err
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				found = true
				var name, registryURL, credentialType, verified string
				if err := rows.Scan(&name, &registryURL, &credentialType, &verified); err != nil {
					return err
				}
				fmt.Fprintf(cc.stdout, "%s\t%s\t%s\t%s\n", name, registryURL, credentialType, verified)
			}
			if !found {
				fmt.Fprintln(cc.stdout, "등록된 registry가 없습니다.")
			}
			return rows.Err()
		},
	}
}

func newRegistryShowCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show registry metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			reg, err := app.LoadRegistry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "Name:       %s\nURL:        %s\nCredential: %s\n", reg.Name, reg.URL, reg.CredentialType)
			return nil
		},
	}
}

func newRegistryVerifyCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <name>",
		Short: "Verify registry metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			reg, err := app.LoadRegistry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if _, err := exec.LookPath("docker"); err != nil {
				return CodeError{Code: ErrRegistryCredentialInvalid, Message: "docker CLI를 찾을 수 없습니다.", Remedy: "docker 또는 호환 image builder를 설치하세요.", Err: err}
			}
			now := app.timestamp()
			if _, err := app.DB.ExecContext(cmd.Context(), "UPDATE registries SET last_verified_at = ?, updated_at = ? WHERE id = ?", now, now, reg.ID); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ registry 확인 완료: %s\n", reg.Name)
			return nil
		},
	}
}

func newRegistryRemoveCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove registry",
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
			affected, err := softDeleteByName(cmd.Context(), app.DB, "registries", account.UserID, args[0], app.timestamp())
			if err != nil {
				return err
			}
			if affected == 0 {
				return CodeError{Code: ErrRegistryNotFound, Message: "registry를 찾을 수 없습니다.", Remedy: "jjctl registry list로 이름을 확인하세요."}
			}
			if err := app.Audit(cmd.Context(), account.UserID, "registry.remove", "registry", args[0], nil); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ registry 제거 완료: %s\n", args[0])
			return nil
		},
	}
}

type registryRecord struct {
	ID             string
	Name           string
	URL            string
	CredentialType string
	CredentialRef  string
}

func (a *App) LoadRegistry(ctx context.Context, name string) (registryRecord, error) {
	account, err := a.CurrentAccount(ctx)
	if err != nil {
		return registryRecord{}, err
	}
	var reg registryRecord
	err = a.DB.QueryRowContext(ctx, `SELECT id, name, registry_url, credential_type, credential_ref
FROM registries WHERE user_id = ? AND name = ? AND deleted_at IS NULL`, account.UserID, name).Scan(&reg.ID, &reg.Name, &reg.URL, &reg.CredentialType, &reg.CredentialRef)
	if errors.Is(err, sql.ErrNoRows) {
		return reg, CodeError{Code: ErrRegistryNotFound, Message: "registry를 찾을 수 없습니다.", Remedy: "jjctl registry list로 이름을 확인하세요."}
	}
	return reg, err
}
