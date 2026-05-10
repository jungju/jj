package jjctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

func newAuthCommand(cc *commandContext) *cobra.Command {
	root := &cobra.Command{Use: "auth", Short: "GitHub authentication"}
	root.AddCommand(newAuthLoginCommand(cc))
	root.AddCommand(newAuthStatusCommand(cc))
	root.AddCommand(newAuthLogoutCommand(cc))
	return root
}

func newAuthLoginCommand(cc *commandContext) *cobra.Command {
	var clientID string
	var scope string
	var tokenEnv string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login with GitHub Device Flow",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := cc.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer app.Close()

			token := strings.TrimSpace(os.Getenv(tokenEnv))
			if token == "" {
				token, scope, err = runGitHubDeviceFlow(cmd.Context(), cc.stdout, clientID, scope)
				if err != nil {
					return err
				}
			}
			scopes := splitScopes(scope)
			user, err := app.SaveGitHubLogin(cmd.Context(), token, scopes)
			if err != nil {
				return err
			}
			fmt.Fprintln(cc.stdout, "✓ GitHub 인증 완료")
			fmt.Fprintln(cc.stdout, "✓ jj 계정 연결 완료")
			fmt.Fprintf(cc.stdout, "\nLogged in as %s\n", user.Login)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", os.Getenv("JJ_GITHUB_CLIENT_ID"), "GitHub OAuth App client ID (or JJ_GITHUB_CLIENT_ID)")
	cmd.Flags().StringVar(&scope, "scope", firstNonEmpty(os.Getenv("JJ_GITHUB_SCOPE"), "repo read:user"), "GitHub OAuth scopes")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "JJ_GITHUB_TOKEN", "environment variable containing a GitHub token for non-interactive login")
	return cmd
}

func newAuthStatusCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show GitHub authentication status",
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
			fmt.Fprintf(cc.stdout, "Logged in as %s\n", account.GitHubLogin)
			return nil
		},
	}
}

func newAuthLogoutCommand(cc *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Logout from GitHub",
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
			if _, err := app.DB.ExecContext(cmd.Context(), "UPDATE github_accounts SET revoked_at = ?, updated_at = ? WHERE user_id = ? AND revoked_at IS NULL", now, now, account.UserID); err != nil {
				return err
			}
			_ = app.Secrets.Delete(account.AccessTokenRef)
			if err := app.Audit(cmd.Context(), account.UserID, "auth.logout", "github_account", "", nil); err != nil {
				return err
			}
			fmt.Fprintln(cc.stdout, "✓ Logged out")
			return nil
		},
	}
}

func runGitHubDeviceFlow(ctx context.Context, stdout io.Writer, clientID, scope string) (string, string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return "", "", CodeError{
			Code:    ErrAuthGitHubLoginFailed,
			Message: "GitHub Device Flow client ID가 없습니다.",
			Remedy:  "JJ_GITHUB_CLIENT_ID를 설정하거나 --client-id를 지정하세요. 자동화 환경에서는 JJ_GITHUB_TOKEN도 사용할 수 있습니다.",
		}
	}
	deviceURL := firstNonEmpty(os.Getenv("JJ_GITHUB_DEVICE_URL"), "https://github.com/login/device/code")
	tokenURL := firstNonEmpty(os.Getenv("JJ_GITHUB_TOKEN_URL"), "https://github.com/login/oauth/access_token")
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("scope", scope)
	var device deviceCodeResponse
	if err := postGitHubForm(ctx, deviceURL, form, &device); err != nil {
		return "", "", err
	}
	if device.Interval <= 0 {
		device.Interval = 5
	}
	fmt.Fprintln(stdout, "GitHub 로그인을 시작합니다.")
	fmt.Fprintln(stdout, "\n브라우저에서 아래 코드를 입력하세요.")
	fmt.Fprintf(stdout, "\nCode: %s\n", device.UserCode)
	fmt.Fprintf(stdout, "URL:  %s\n\n", device.VerificationURI)

	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	for {
		if time.Now().After(deadline) {
			return "", "", CodeError{Code: ErrAuthDeviceFlowExpired, Message: "GitHub Device Flow가 만료되었습니다.", Remedy: "jjctl auth login을 다시 실행하세요."}
		}
		if err := sleepContext(ctx, time.Duration(device.Interval)*time.Second); err != nil {
			return "", "", err
		}
		poll := url.Values{}
		poll.Set("client_id", clientID)
		poll.Set("device_code", device.DeviceCode)
		poll.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		var tokenResp accessTokenResponse
		if err := postGitHubForm(ctx, tokenURL, poll, &tokenResp); err != nil {
			return "", "", err
		}
		switch tokenResp.Error {
		case "":
			if tokenResp.AccessToken == "" {
				return "", "", CodeError{Code: ErrAuthGitHubLoginFailed, Message: "GitHub token 응답이 비어 있습니다.", Remedy: "GitHub OAuth App 설정을 확인하세요."}
			}
			return tokenResp.AccessToken, firstNonEmpty(tokenResp.Scope, scope), nil
		case "authorization_pending":
			continue
		case "slow_down":
			device.Interval += 5
			continue
		case "expired_token":
			return "", "", CodeError{Code: ErrAuthDeviceFlowExpired, Message: "GitHub Device Flow가 만료되었습니다.", Remedy: "jjctl auth login을 다시 실행하세요."}
		default:
			return "", "", CodeError{Code: ErrAuthGitHubLoginFailed, Message: firstNonEmpty(tokenResp.ErrorDesc, tokenResp.Error), Remedy: "GitHub 인증 화면의 승인 상태를 확인하세요."}
		}
	}
}

func postGitHubForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github auth status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func splitScopes(scope string) []string {
	fields := strings.FieldsFunc(scope, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	var scopes []string
	for _, field := range fields {
		if strings.TrimSpace(field) != "" {
			scopes = append(scopes, strings.TrimSpace(field))
		}
	}
	return scopes
}
