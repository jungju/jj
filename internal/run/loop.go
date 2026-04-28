package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/security"
)

type RunOutcome struct {
	Status           string
	ValidationStatus string
	Error            string
	CommitFailed     bool
}

func TurnRunID(loopID string, turn int) string {
	if turn <= 1 {
		return loopID
	}
	return loopID + "-t" + twoDigitTurn(turn)
}

func WorkspaceRootFromRunDir(runDir string) string {
	runDir = strings.TrimSpace(runDir)
	if runDir == "" {
		return ""
	}
	abs, err := filepath.Abs(runDir)
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(abs)))
}

func OutcomeForRun(cwd, runID string) RunOutcome {
	store, err := artifact.NewStore(cwd, runID)
	if err != nil {
		return RunOutcome{Status: StatusSuccess, Error: redactSecrets(err.Error())}
	}
	return OutcomeForRunDir(store.RunDir)
}

func OutcomeForRunDir(runDir string) RunOutcome {
	outcome := RunOutcome{Status: StatusSuccess}
	if strings.TrimSpace(runDir) == "" {
		outcome.Error = "run directory is unknown"
		return outcome
	}
	manifestPath, err := security.SafeJoinNoSymlinks(runDir, "manifest.json", security.PathPolicy{})
	if err != nil {
		outcome.Error = redactSecrets(err.Error())
		return outcome
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		outcome.Error = redactSecrets(err.Error())
		return outcome
	}
	var manifest struct {
		Status       string   `json:"status"`
		ErrorSummary string   `json:"error_summary"`
		Errors       []string `json:"errors"`
		Validation   struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"validation"`
		Commit struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		outcome.Error = redactSecrets(err.Error())
		return outcome
	}
	if strings.TrimSpace(manifest.Status) != "" {
		outcome.Status = manifest.Status
	}
	outcome.ValidationStatus = manifest.Validation.Status
	outcome.Error = manifest.ErrorSummary
	if outcome.Error == "" && len(manifest.Errors) > 0 {
		outcome.Error = manifest.Errors[0]
	}
	if outcome.Error == "" {
		outcome.Error = manifest.Validation.Error
	}
	if manifest.Commit.Status == "failed" {
		outcome.CommitFailed = true
		if manifest.Commit.Error != "" {
			outcome.Error = manifest.Commit.Error
		}
	}
	outcome.Error = redactSecrets(outcome.Error)
	return outcome
}

func BuildContinuationContext(cwd, previousRunID string) (string, error) {
	store, err := artifact.NewStore(cwd, previousRunID)
	if err != nil {
		return "", err
	}
	return BuildContinuationContextFromRunDir(cwd, store.RunDir, previousRunID)
}

func BuildContinuationContextFromRunDir(cwd, previousRunDir, previousRunID string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	trustedRunDir, err := trustedContinuationRunDir(cwd, previousRunDir, previousRunID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("This is an automatic continuation turn for jj.\n")
	b.WriteString("Use the current .jj/spec.json as the source of truth when present. Treat the original plan as product vision/background, then use this evidence to decide the next smallest useful change.\n\n")
	b.WriteString("Previous run id: ")
	b.WriteString(previousRunID)
	b.WriteString("\n\n")
	appendContinuationRel(&b, "Workspace SPEC State", cwd, DefaultSpecStatePath)
	appendContinuationRel(&b, "Workspace Task State", cwd, DefaultTasksStatePath)
	appendContinuationRel(&b, "Previous Manifest", trustedRunDir, "manifest.json")
	appendContinuationRel(&b, "Previous Validation Summary", trustedRunDir, "validation/summary.md")
	appendContinuationRel(&b, "Previous Git Diff Summary", trustedRunDir, "git/diff-summary.txt")
	appendContinuationRel(&b, "Previous Codex Summary", trustedRunDir, "codex/summary.md")
	return truncateContinuation(redactSecrets(b.String()), 60000), nil
}

func trustedContinuationRunDir(cwd, reportedRunDir, runID string) (string, error) {
	store, err := artifact.NewStore(cwd, runID)
	if err != nil {
		return "", err
	}
	reportedRunDir = strings.TrimSpace(reportedRunDir)
	if reportedRunDir == "" {
		return store.RunDir, nil
	}
	reportedAbs, err := filepath.Abs(reportedRunDir)
	if err != nil {
		return "", err
	}
	expectedAbs, err := filepath.Abs(store.RunDir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(reportedAbs); err == nil {
		reportedAbs = resolved
	}
	if resolved, err := filepath.EvalSymlinks(expectedAbs); err == nil {
		expectedAbs = resolved
	}
	if filepath.Clean(reportedAbs) != filepath.Clean(expectedAbs) {
		return "", errors.New("reported run directory is outside the expected run root")
	}
	if _, err := security.SafeJoinNoSymlinks(store.RunDir, "manifest.json", security.PathPolicy{}); err != nil {
		return "", fmt.Errorf("validate continuation run directory: %w", err)
	}
	return store.RunDir, nil
}

func appendContinuationRel(b *strings.Builder, title, root, rel string) {
	path, err := security.SafeJoinNoSymlinks(root, rel, security.PathPolicy{AllowHidden: strings.HasPrefix(rel, ".")})
	if err != nil {
		return
	}
	appendContinuationFile(b, title, path)
}

func appendContinuationFile(b *strings.Builder, title, path string) {
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return
	}
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(truncateContinuation(redactSecrets(string(data)), 12000))
	b.WriteString("\n\n")
}

func truncateContinuation(s string, max int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= max {
		return s
	}
	cut := 0
	for i := range s {
		if i > max {
			break
		}
		cut = i
	}
	return s[:cut] + "\n...[truncated]..."
}

func twoDigitTurn(turn int) string {
	if turn < 10 {
		return "0" + strconv.Itoa(turn)
	}
	return strconv.Itoa(turn)
}
