package jjctl

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/jungju/jj/internal/secrets"
	"github.com/spf13/cobra"
)

type commandContext struct {
	home   string
	stdout io.Writer
	stderr io.Writer
}

func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	cc := &commandContext{stdout: stdout, stderr: stderr}
	cmd := &cobra.Command{
		Use:           "jjctl",
		Short:         "CLI-first GitHub, Codex, and Kubernetes deployment tool",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.PersistentFlags().StringVar(&cc.home, "home", "", "jj home directory (default: ~/.jj or JJ_HOME)")
	cmd.AddCommand(newAuthCommand(cc))
	cmd.AddCommand(newRepoCommand(cc))
	cmd.AddCommand(newDocsCommand(cc))
	cmd.AddCommand(newAskCommand(cc))
	cmd.AddCommand(newDoctorCommand(cc))
	cmd.AddCommand(newCodexDoctorCommand(cc))
	cmd.AddCommand(newK8SCommand(cc))
	cmd.AddCommand(newRegistryCommand(cc))
	cmd.AddCommand(newPoolCommand(cc))
	cmd.AddCommand(newDeployCommand(cc))
	return cmd
}

func Main(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cmd := NewRootCommand(stdout, stderr)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", secrets.Redact(err.Error()))
		return 1
	}
	return 0
}

func (cc *commandContext) openApp(ctx context.Context) (*App, error) {
	return OpenApp(ctx, AppOptions{Home: cc.home, Stdout: cc.stdout, Stderr: cc.stderr})
}

func newDoctorCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local jjctl dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()
			fmt.Fprintln(cc.stdout, "jjctl doctor")
			fmt.Fprintf(cc.stdout, "✓ home: %s\n", app.Paths.Home)
			fmt.Fprintf(cc.stdout, "✓ sqlite: %s\n", app.Paths.DBPath)
			printToolCheck(cc.stdout, "git")
			printToolCheck(cc.stdout, "codex")
			printToolCheck(cc.stdout, "kubectl")
			printToolCheck(cc.stdout, "docker")
			return nil
		},
	}
}

func newCodexDoctorCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "codex", Short: "Codex helper commands"}
	root.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check Codex CLI availability",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := exec.LookPath("codex"); err != nil {
				return CodeError{
					Code:    ErrCodexNotInstalled,
					Message: "Codex CLI를 찾을 수 없습니다.",
					Remedy:  "Codex CLI를 설치하거나 PATH에 추가하세요.",
					Err:     err,
				}
			}
			fmt.Fprintln(cc.stdout, "✓ codex installed")
			return nil
		},
	})
	return root
}

func newK8SDoctorCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "k8s", Short: "Kubernetes helper commands"}
	root.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check Kubernetes CLI availability",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := exec.LookPath("kubectl"); err != nil {
				return CodeError{
					Code:    ErrK8SClusterUnreachable,
					Message: "kubectl을 찾을 수 없습니다.",
					Remedy:  "kubectl을 설치하거나 PATH에 추가하세요.",
					Err:     err,
				}
			}
			fmt.Fprintln(cc.stdout, "✓ kubectl installed")
			return nil
		},
	})
	return root
}

func printToolCheck(stdout io.Writer, name string) {
	if path, err := exec.LookPath(name); err == nil {
		fmt.Fprintf(stdout, "✓ %s: %s\n", name, path)
		return
	}
	fmt.Fprintf(stdout, "! %s: not found\n", name)
}

func currentWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
