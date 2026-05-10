package jjctl

import (
	"errors"
	"fmt"
)

const (
	ErrAuthNotLoggedIn           = "AUTH_NOT_LOGGED_IN"
	ErrAuthDeviceFlowExpired     = "AUTH_DEVICE_FLOW_EXPIRED"
	ErrAuthGitHubTokenRevoked    = "AUTH_GITHUB_TOKEN_REVOKED"
	ErrAuthGitHubLoginFailed     = "AUTH_GITHUB_LOGIN_FAILED"
	ErrRepoNotFound              = "REPO_NOT_FOUND"
	ErrRepoPermissionDenied      = "REPO_PERMISSION_DENIED"
	ErrRepoNotGitDirectory       = "REPO_NOT_GIT_DIRECTORY"
	ErrRepoRemoteNotGitHub       = "REPO_REMOTE_NOT_GITHUB"
	ErrRepoAlreadyRegistered     = "REPO_ALREADY_REGISTERED"
	ErrRepoArchived              = "REPO_ARCHIVED"
	ErrRepoDisabled              = "REPO_DISABLED"
	ErrDocsAlreadyExists         = "DOCS_ALREADY_EXISTS"
	ErrDocsManifestMissing       = "DOCS_MANIFEST_MISSING"
	ErrDocsInitFailed            = "DOCS_INIT_FAILED"
	ErrCodexNotInstalled         = "CODEX_NOT_INSTALLED"
	ErrCodexRunFailed            = "CODEX_RUN_FAILED"
	ErrK8SCredentialNotFound     = "K8S_CREDENTIAL_NOT_FOUND"
	ErrK8SCredentialInvalid      = "K8S_CREDENTIAL_INVALID"
	ErrK8SContextNotFound        = "K8S_CONTEXT_NOT_FOUND"
	ErrK8SNamespaceNotFound      = "K8S_NAMESPACE_NOT_FOUND"
	ErrK8SPermissionDenied       = "K8S_PERMISSION_DENIED"
	ErrK8SClusterUnreachable     = "K8S_CLUSTER_UNREACHABLE"
	ErrPoolNotFound              = "POOL_NOT_FOUND"
	ErrPoolAlreadyExists         = "POOL_ALREADY_EXISTS"
	ErrPoolTargetNotFound        = "POOL_TARGET_NOT_FOUND"
	ErrPoolTargetAlreadyExists   = "POOL_TARGET_ALREADY_EXISTS"
	ErrRegistryNotFound          = "REGISTRY_NOT_FOUND"
	ErrRegistryCredentialInvalid = "REGISTRY_CREDENTIAL_INVALID"
	ErrDeployConfigNotFound      = "DEPLOY_CONFIG_NOT_FOUND"
	ErrDeployConfigInvalid       = "DEPLOY_CONFIG_INVALID"
	ErrDeployStrategyUnsupported = "DEPLOY_STRATEGY_UNSUPPORTED"
	ErrDeployManifestNotFound    = "DEPLOY_MANIFEST_NOT_FOUND"
	ErrDeployDiffFailed          = "DEPLOY_DIFF_FAILED"
	ErrDeployUserCancelled       = "DEPLOY_USER_CANCELLED"
	ErrDeployApplyFailed         = "DEPLOY_APPLY_FAILED"
	ErrDeployRollbackUnsupported = "DEPLOY_ROLLBACK_UNSUPPORTED"
	ErrImageBuildFailed          = "IMAGE_BUILD_FAILED"
	ErrImagePushFailed           = "IMAGE_PUSH_FAILED"
)

type CodeError struct {
	Code    string
	Message string
	Remedy  string
	Err     error
}

func (e CodeError) Error() string {
	if e.Code == "" {
		if e.Err != nil {
			return e.Err.Error()
		}
		return e.Message
	}
	base := fmt.Sprintf("%s: %s", e.Code, e.Message)
	if e.Remedy != "" {
		base += "\n해결 방법: " + e.Remedy
	}
	if e.Err != nil {
		base += "\n원인: " + e.Err.Error()
	}
	return base
}

func (e CodeError) Unwrap() error {
	return e.Err
}

func IsCode(err error, code string) bool {
	var codeErr CodeError
	return errors.As(err, &codeErr) && codeErr.Code == code
}
