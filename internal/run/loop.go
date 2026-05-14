package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	b.WriteString("Use the current SQLite workspace SPEC state as the source of truth when present. Treat the original plan as product vision/background, then use this evidence to decide the next smallest useful change.\n\n")
	b.WriteString("Previous run id: ")
	b.WriteString(previousRunID)
	b.WriteString("\n\n")
	appendContinuationRel(&b, "Workspace SPEC State", cwd, DefaultSpecStatePath)
	appendContinuationRel(&b, "Workspace Task State", cwd, DefaultTasksStatePath)
	appendContinuationManifestRel(&b, trustedRunDir, "manifest.json")
	appendContinuationRel(&b, "Previous Validation Summary", trustedRunDir, "validation/summary.md")
	appendContinuationRel(&b, "Previous Git Diff Summary", trustedRunDir, "git/diff-summary.txt")
	appendContinuationRel(&b, "Previous Codex Summary", trustedRunDir, "codex/summary.md")
	return truncateContinuation(sanitizeHandoffText(b.String()), 60000), nil
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
	if IsWorkspaceStatePath(rel) {
		data, ok, err := ReadWorkspaceStateDocument(root, rel)
		if err == nil && ok {
			appendContinuationData(b, title, data)
			return
		}
	}
	path, err := security.SafeJoinNoSymlinks(root, rel, security.PathPolicy{AllowHidden: strings.HasPrefix(rel, ".")})
	if err != nil {
		return
	}
	appendContinuationFile(b, title, path)
}

func appendContinuationData(b *strings.Builder, title string, data []byte) {
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(truncateContinuation(sanitizeHandoffText(string(data)), 12000))
	b.WriteString("\n\n")
}

func appendContinuationFile(b *strings.Builder, title, path string) {
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return
	}
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(truncateContinuation(sanitizeHandoffText(string(data)), 12000))
	b.WriteString("\n\n")
}

func appendContinuationManifestRel(b *strings.Builder, root, rel string) {
	path, err := security.SafeJoinNoSymlinks(root, rel, security.PathPolicy{})
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return
	}
	summary := safeContinuationManifestSummary(data)
	if strings.TrimSpace(summary) == "" {
		return
	}
	b.WriteString("## Previous Manifest Summary\n\n")
	b.WriteString(truncateContinuation(summary, 12000))
	b.WriteString("\n\n")
}

func safeContinuationManifestSummary(data []byte) string {
	var raw struct {
		RunID                    string            `json:"run_id"`
		Status                   string            `json:"status"`
		DryRun                   bool              `json:"dry_run"`
		PlannerProvider          string            `json:"planner_provider"`
		TaskProposalMode         string            `json:"task_proposal_mode"`
		ResolvedTaskProposalMode string            `json:"resolved_task_proposal_mode"`
		SelectedTaskID           string            `json:"selected_task_id"`
		Artifacts                map[string]string `json:"artifacts"`
		Validation               struct {
			Status         string `json:"status"`
			EvidenceStatus string `json:"evidence_status"`
			CommandCount   int    `json:"command_count"`
			PassedCount    int    `json:"passed_count"`
			FailedCount    int    `json:"failed_count"`
		} `json:"validation"`
		Codex struct {
			Ran      bool   `json:"ran"`
			Skipped  bool   `json:"skipped"`
			Status   string `json:"status"`
			ExitCode int    `json:"exit_code"`
		} `json:"codex"`
		Security struct {
			Diagnostics ManifestSecurityDiagnostics `json:"diagnostics"`
		} `json:"security"`
		Git struct {
			DiffRedactionApplied        bool           `json:"diff_redaction_applied"`
			DiffRedactionCount          int            `json:"diff_redaction_count"`
			DiffRedactionCategories     []string       `json:"diff_redaction_categories"`
			DiffRedactionCategoryCounts map[string]int `json:"diff_redaction_category_counts"`
			DiffArtifactLabels          []string       `json:"diff_artifact_labels"`
		} `json:"git"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "manifest_state: unavailable\n"
	}
	diag := sanitizeManifestSecurityDiagnostics(raw.Security.Diagnostics)
	artifactLabels := make([]string, 0, len(raw.Artifacts))
	for name := range raw.Artifacts {
		if err := artifact.ValidateArtifactName(name); err == nil {
			artifactLabels = append(artifactLabels, name)
		}
	}
	sort.Strings(artifactLabels)
	gitDiffCategoryCounts := sanitizeSecurityCategoryCounts(raw.Git.DiffRedactionCategoryCounts, "redaction")
	gitDiffCategories := sortedSecurityCategories(gitDiffCategoryCounts)
	gitDiffLabels := sanitizeSecurityLabels(raw.Git.DiffArtifactLabels)
	summary := map[string]any{
		"run_id":                      raw.RunID,
		"status":                      raw.Status,
		"dry_run":                     raw.DryRun,
		"planner_provider":            raw.PlannerProvider,
		"task_proposal_mode":          raw.TaskProposalMode,
		"resolved_task_proposal_mode": raw.ResolvedTaskProposalMode,
		"selected_task_id":            raw.SelectedTaskID,
		"artifact_labels":             artifactLabels,
		"validation": map[string]any{
			"status":          raw.Validation.Status,
			"evidence_status": raw.Validation.EvidenceStatus,
			"command_count":   raw.Validation.CommandCount,
			"passed_count":    raw.Validation.PassedCount,
			"failed_count":    raw.Validation.FailedCount,
		},
		"codex": map[string]any{
			"ran":       raw.Codex.Ran,
			"skipped":   raw.Codex.Skipped,
			"status":    raw.Codex.Status,
			"exit_code": raw.Codex.ExitCode,
		},
		"security": map[string]any{
			"redacted":                 diag.Redacted,
			"denied_path_count":        diag.DeniedPathCount,
			"denied_path_categories":   diag.DeniedPathCategories,
			"failure_categories":       diag.FailureCategories,
			"git_diff_redacted":        diag.GitDiffRedactionApplied,
			"git_diff_redaction_count": diag.GitDiffRedactionCount,
			"git_diff_categories":      diag.GitDiffRedactionCategories,
			"git_diff_artifact_labels": diag.GitDiffArtifactLabels,
		},
		"git_diff": map[string]any{
			"redaction_applied": raw.Git.DiffRedactionApplied,
			"redaction_count":   raw.Git.DiffRedactionCount,
			"categories":        gitDiffCategories,
			"category_counts":   gitDiffCategoryCounts,
			"artifact_labels":   gitDiffLabels,
		},
	}
	out, err := json.MarshalIndent(security.SanitizeHandoffJSONValue(summary), "", "  ")
	if err != nil {
		return "manifest_state: unavailable\n"
	}
	return sanitizeHandoffText(string(out)) + "\n"
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
