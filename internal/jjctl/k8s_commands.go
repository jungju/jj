package jjctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type kubeConfigFile struct {
	Clusters []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server string `yaml:"server"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Exec any `yaml:"exec"`
		} `yaml:"user"`
	} `yaml:"users"`
}

type k8sCredentialRecord struct {
	ID               string
	UserID           string
	Name             string
	ContextName      string
	ServerURL        string
	DefaultNamespace string
	CredentialRef    string
	HasExecPlugin    bool
}

func newK8SCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "k8s", Short: "Manage Kubernetes credentials"}
	credential := newK8SCredentialCommand(cc)
	root.AddCommand(credential)
	root.AddCommand(newK8SKeyAliasCommand(cc))
	root.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check Kubernetes CLI availability",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := exec.LookPath("kubectl"); err != nil {
				return CodeError{Code: ErrK8SClusterUnreachable, Message: "kubectl을 찾을 수 없습니다.", Remedy: "kubectl을 설치하거나 PATH에 추가하세요.", Err: err}
			}
			fmt.Fprintln(cc.stdout, "✓ kubectl installed")
			return nil
		},
	})
	return root
}

func newK8SCredentialCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "credential", Short: "Manage Kubernetes credentials"}
	root.AddCommand(newK8SCredentialAddCommand(cc, "credential"))
	root.AddCommand(newK8SCredentialListCommand(cc, "credential"))
	root.AddCommand(newK8SCredentialShowCommand(cc))
	root.AddCommand(newK8SCredentialVerifyCommand(cc, "credential"))
	root.AddCommand(newK8SCredentialRemoveCommand(cc, "credential"))
	return root
}

func newK8SKeyAliasCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "key", Short: "Alias for k8s credential"}
	root.AddCommand(newK8SCredentialAddCommand(cc, "key"))
	root.AddCommand(newK8SCredentialListCommand(cc, "key"))
	root.AddCommand(newK8SCredentialVerifyCommand(cc, "key"))
	root.AddCommand(newK8SCredentialRemoveCommand(cc, "key"))
	return root
}

func newK8SCredentialAddCommand(cc *commandContext, noun string) *cobra.Command {
	var kubeconfigPath string
	var contextName string
	var namespace string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a Kubernetes credential",
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
			record, err := app.AddK8SCredential(cmd.Context(), account.UserID, args[0], kubeconfigPath, contextName, namespace)
			if err != nil {
				return err
			}
			fmt.Fprintln(cc.stdout, "Kubernetes credential 정보를 확인했습니다.")
			fmt.Fprintf(cc.stdout, "\nName:      %s\nContext:   %s\nNamespace: %s\nServer:    %s\n\n", record.Name, record.ContextName, record.DefaultNamespace, record.ServerURL)
			if err := app.VerifyK8SCredential(cmd.Context(), record.Name, record.DefaultNamespace, cc.stdout); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "\n✓ %s 등록 완료\n", noun)
			return nil
		},
	}
	cmd.Flags().StringVar(&kubeconfigPath, "from-kubeconfig", "", "kubeconfig path")
	cmd.Flags().StringVar(&contextName, "context", "", "kubeconfig context")
	cmd.Flags().StringVar(&namespace, "namespace", "", "default namespace")
	_ = cmd.MarkFlagRequired("from-kubeconfig")
	_ = cmd.MarkFlagRequired("context")
	_ = cmd.MarkFlagRequired("namespace")
	return cmd
}

func newK8SCredentialListCommand(cc *commandContext, noun string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Kubernetes credentials",
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
			rows, err := app.DB.QueryContext(cmd.Context(), `SELECT name, COALESCE(context_name, ''), COALESCE(default_namespace, ''), COALESCE(server_url, ''), COALESCE(last_verified_at, '')
FROM k8s_credentials WHERE user_id = ? AND deleted_at IS NULL ORDER BY name`, account.UserID)
			if err != nil {
				return err
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				found = true
				var name, contextName, namespace, server, verified string
				if err := rows.Scan(&name, &contextName, &namespace, &server, &verified); err != nil {
					return err
				}
				fmt.Fprintf(cc.stdout, "%s\t%s\t%s\t%s\t%s\n", name, contextName, namespace, server, verified)
			}
			if !found {
				fmt.Fprintf(cc.stdout, "등록된 Kubernetes %s가 없습니다.\n", noun)
			}
			return rows.Err()
		},
	}
}

func newK8SCredentialShowCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show Kubernetes credential metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			record, err := app.LoadK8SCredential(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "Name:      %s\n", record.Name)
			fmt.Fprintf(cc.stdout, "Context:   %s\n", record.ContextName)
			fmt.Fprintf(cc.stdout, "Namespace: %s\n", record.DefaultNamespace)
			fmt.Fprintf(cc.stdout, "Server:    %s\n", record.ServerURL)
			fmt.Fprintf(cc.stdout, "Exec:      %t\n", record.HasExecPlugin)
			fmt.Fprintln(cc.stdout, "Secret:    stored as encrypted reference")
			return nil
		},
	}
}

func newK8SCredentialVerifyCommand(cc *commandContext, noun string) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <name>",
		Short: "Verify Kubernetes credential permissions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			if err := app.VerifyK8SCredential(cmd.Context(), args[0], "", cc.stdout); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ %s 검증 완료\n", noun)
			return nil
		},
	}
}

func newK8SCredentialRemoveCommand(cc *commandContext, noun string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove Kubernetes credential",
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
			affected, err := softDeleteByName(cmd.Context(), app.DB, "k8s_credentials", account.UserID, args[0], now)
			if err != nil {
				return err
			}
			if affected == 0 {
				return CodeError{Code: ErrK8SCredentialNotFound, Message: "Kubernetes credential을 찾을 수 없습니다.", Remedy: "jjctl k8s credential list로 이름을 확인하세요."}
			}
			if err := app.Audit(cmd.Context(), account.UserID, "k8s.credential.remove", "k8s_credential", args[0], nil); err != nil {
				return err
			}
			fmt.Fprintf(cc.stdout, "✓ %s 제거 완료: %s\n", noun, args[0])
			return nil
		},
	}
}

func (a *App) AddK8SCredential(ctx context.Context, userID, name, kubeconfigPath, contextName, namespace string) (k8sCredentialRecord, error) {
	kubeconfigPath = expandHome(kubeconfigPath)
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return k8sCredentialRecord{}, CodeError{Code: ErrK8SCredentialInvalid, Message: "kubeconfig 파일을 읽을 수 없습니다.", Err: err}
	}
	parsed, err := parseKubeconfig(data)
	if err != nil {
		return k8sCredentialRecord{}, err
	}
	ctxInfo, ok := findKubeContext(parsed, contextName)
	if !ok {
		return k8sCredentialRecord{}, CodeError{Code: ErrK8SContextNotFound, Message: "kubeconfig context를 찾을 수 없습니다.", Remedy: "kubectl config get-contexts로 이름을 확인하세요."}
	}
	serverURL := findKubeServer(parsed, ctxInfo.clusterName)
	if serverURL == "" {
		return k8sCredentialRecord{}, CodeError{Code: ErrK8SCredentialInvalid, Message: "context에 연결된 cluster server URL을 찾을 수 없습니다."}
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = ctxInfo.namespace
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = "default"
	}
	hasExec := kubeUserHasExec(parsed, ctxInfo.userName)
	now := a.timestamp()
	ref, err := a.Secrets.Save("kubeconfig "+name, data, now)
	if err != nil {
		return k8sCredentialRecord{}, err
	}
	id := newID("k8s")
	_, err = a.DB.ExecContext(ctx, `INSERT INTO k8s_credentials (
  id, user_id, name, cluster_name, context_name, server_url, default_namespace, credential_type, credential_ref, has_exec_plugin, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'kubeconfig', ?, ?, ?, ?)
ON CONFLICT(user_id, name) DO UPDATE SET
  cluster_name = excluded.cluster_name,
  context_name = excluded.context_name,
  server_url = excluded.server_url,
  default_namespace = excluded.default_namespace,
  credential_ref = excluded.credential_ref,
  has_exec_plugin = excluded.has_exec_plugin,
  deleted_at = NULL,
  updated_at = excluded.updated_at`,
		id, userID, name, ctxInfo.clusterName, contextName, serverURL, namespace, ref, boolInt(hasExec), now, now)
	if err != nil {
		return k8sCredentialRecord{}, err
	}
	if err := a.Audit(ctx, userID, "k8s.credential.add", "k8s_credential", name, map[string]any{"context": contextName, "namespace": namespace}); err != nil {
		return k8sCredentialRecord{}, err
	}
	return k8sCredentialRecord{ID: id, UserID: userID, Name: name, ContextName: contextName, ServerURL: serverURL, DefaultNamespace: namespace, CredentialRef: ref, HasExecPlugin: hasExec}, nil
}

func (a *App) LoadK8SCredential(ctx context.Context, name string) (k8sCredentialRecord, error) {
	account, err := a.CurrentAccount(ctx)
	if err != nil {
		return k8sCredentialRecord{}, err
	}
	var record k8sCredentialRecord
	record.UserID = account.UserID
	err = a.DB.QueryRowContext(ctx, `SELECT id, name, COALESCE(context_name, ''), COALESCE(server_url, ''), COALESCE(default_namespace, ''), credential_ref, has_exec_plugin
FROM k8s_credentials WHERE user_id = ? AND name = ? AND deleted_at IS NULL`, account.UserID, name).Scan(&record.ID, &record.Name, &record.ContextName, &record.ServerURL, &record.DefaultNamespace, &record.CredentialRef, &record.HasExecPlugin)
	if errors.Is(err, sql.ErrNoRows) {
		return record, CodeError{Code: ErrK8SCredentialNotFound, Message: "Kubernetes credential을 찾을 수 없습니다.", Remedy: "jjctl k8s credential list로 이름을 확인하세요."}
	}
	return record, err
}

func (a *App) VerifyK8SCredential(ctx context.Context, name, namespaceOverride string, stdout anyWriter) error {
	record, err := a.LoadK8SCredential(ctx, name)
	if err != nil {
		return err
	}
	namespace := firstNonEmpty(namespaceOverride, record.DefaultNamespace, "default")
	if _, err := exec.LookPath("kubectl"); err != nil {
		return CodeError{Code: ErrK8SClusterUnreachable, Message: "kubectl을 찾을 수 없습니다.", Remedy: "kubectl을 설치하거나 PATH에 추가하세요.", Err: err}
	}
	data, err := a.Secrets.Load(record.CredentialRef)
	if err != nil {
		return CodeError{Code: ErrK8SCredentialInvalid, Message: "저장된 kubeconfig를 읽을 수 없습니다.", Err: err}
	}
	tmp, cleanup, err := writeTempSecretFile("jj-kubeconfig-*", data)
	if err != nil {
		return err
	}
	defer cleanup()
	checks := [][]string{
		{"get", "namespace", namespace},
		{"auth", "can-i", "get", "deployments", "-n", namespace},
		{"auth", "can-i", "create", "deployments", "-n", namespace},
		{"auth", "can-i", "patch", "deployments", "-n", namespace},
		{"auth", "can-i", "get", "pods", "-n", namespace},
		{"auth", "can-i", "get", "services", "-n", namespace},
	}
	fmt.Fprintln(stdout, "권한 확인:")
	for _, check := range checks {
		args := append([]string{"--kubeconfig", tmp, "--context", record.ContextName}, check...)
		out, err := runCommand(ctx, "", "kubectl", args...)
		label := strings.Join(check, " ")
		if err != nil {
			return CodeError{Code: ErrK8SPermissionDenied, Message: "Kubernetes 권한 확인에 실패했습니다: " + label, Remedy: "namespace-scoped deployment 권한을 확인하세요.", Err: err}
		}
		if check[0] == "auth" && strings.TrimSpace(out) != "yes" {
			return CodeError{Code: ErrK8SPermissionDenied, Message: "Kubernetes 권한이 부족합니다: " + label, Remedy: "해당 namespace 권한을 추가하세요."}
		}
		fmt.Fprintf(stdout, "✓ %s\n", label)
	}
	now := a.timestamp()
	if _, err := a.DB.ExecContext(ctx, "UPDATE k8s_credentials SET last_verified_at = ?, updated_at = ? WHERE id = ?", now, now, record.ID); err != nil {
		return err
	}
	return a.Audit(ctx, record.UserID, "k8s.credential.verify", "k8s_credential", record.ID, map[string]any{"namespace": namespace})
}

func parseKubeconfig(data []byte) (kubeConfigFile, error) {
	var parsed kubeConfigFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return parsed, CodeError{Code: ErrK8SCredentialInvalid, Message: "kubeconfig YAML을 파싱할 수 없습니다.", Err: err}
	}
	return parsed, nil
}

type kubeContextInfo struct {
	clusterName string
	userName    string
	namespace   string
}

func findKubeContext(parsed kubeConfigFile, contextName string) (kubeContextInfo, bool) {
	for _, item := range parsed.Contexts {
		if item.Name == contextName {
			return kubeContextInfo{clusterName: item.Context.Cluster, userName: item.Context.User, namespace: item.Context.Namespace}, true
		}
	}
	return kubeContextInfo{}, false
}

func findKubeServer(parsed kubeConfigFile, clusterName string) string {
	for _, item := range parsed.Clusters {
		if item.Name == clusterName {
			return item.Cluster.Server
		}
	}
	return ""
}

func kubeUserHasExec(parsed kubeConfigFile, userName string) bool {
	for _, item := range parsed.Users {
		if item.Name == userName && item.User.Exec != nil {
			return true
		}
	}
	return false
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func writeTempSecretFile(pattern string, data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", func() {}, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", func() {}, err
	}
	return name, func() { _ = os.Remove(name) }, nil
}
