package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/security"
)

const (
	defaultValidationCommand        = "./scripts/validate.sh"
	defaultValidationCommandTimeout = 10 * time.Minute

	validationStatusPassed  = "passed"
	validationStatusFailed  = "failed"
	validationStatusSkipped = "skipped"
	validationStatusMissing = "missing"

	validationEvidenceRecorded = "recorded"
	validationEvidenceSkipped  = "skipped"
	validationEvidenceMissing  = "missing"
)

type ManifestValidation struct {
	Ran            bool                        `json:"ran"`
	Skipped        bool                        `json:"skipped"`
	Status         string                      `json:"status,omitempty"`
	EvidenceStatus string                      `json:"evidence_status,omitempty"`
	Reason         string                      `json:"reason,omitempty"`
	Summary        string                      `json:"summary,omitempty"`
	ResultsPath    string                      `json:"results_path,omitempty"`
	SummaryPath    string                      `json:"summary_path,omitempty"`
	CommandCount   int                         `json:"command_count"`
	PassedCount    int                         `json:"passed_count"`
	FailedCount    int                         `json:"failed_count"`
	Commands       []ManifestValidationCommand `json:"commands,omitempty"`
}

type ManifestValidationCommand struct {
	Label      string   `json:"label"`
	Name       string   `json:"name,omitempty"`
	Command    string   `json:"command,omitempty"`
	Provider   string   `json:"provider,omitempty"`
	Model      string   `json:"model,omitempty"`
	CWD        string   `json:"cwd,omitempty"`
	RunID      string   `json:"run_id,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	ExitCode   int      `json:"exit_code"`
	DurationMS int64    `json:"duration_ms"`
	Status     string   `json:"status"`
	StdoutPath string   `json:"stdout_path,omitempty"`
	StderrPath string   `json:"stderr_path,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type validationResultsArtifact struct {
	SchemaVersion string `json:"schema_version"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	ManifestValidation
}

func runValidationEvidence(ctx context.Context, cfg Config, store artifact.Store, taskMarkdown string) (ManifestValidation, error) {
	return runValidationEvidenceCommands(ctx, cfg, store, validationCommandsFromTask(taskMarkdown))
}

func runValidationEvidenceCommands(ctx context.Context, cfg Config, store artifact.Store, commands []string) (ManifestValidation, error) {
	started := time.Now().UTC()
	validation := ManifestValidation{
		Status:      validationStatusSkipped,
		Skipped:     true,
		ResultsPath: "validation/results.json",
		SummaryPath: "validation/summary.md",
	}

	if len(commands) == 0 {
		validation.Status = validationStatusMissing
		validation.EvidenceStatus = validationEvidenceMissing
		validation.Reason = "no validation command was declared in task state"
		validation.Summary = "Validation evidence is missing because no validation command was declared in task state."
		return persistValidationEvidence(store, validation, started)
	}

	command, supported := supportedValidationCommand(commands[0])
	if !supported {
		validation.EvidenceStatus = validationEvidenceSkipped
		validation.Reason = "validation command is not supported for automatic execution: " + redactSecrets(commands[0])
		validation.Summary = "Raw validation evidence was skipped because the declared validation command is not supported for automatic execution."
		validation.Commands = []ManifestValidationCommand{{
			Label:    validationLabel(commands[0]),
			Provider: "local",
			CWD:      "[workspace]",
			Argv:     sanitizedValidationArgv(commands[0], cfg.CWD, store.RunDir),
			Status:   validationStatusSkipped,
			Summary:  validation.Summary,
		}}
		validation.CommandCount = len(validation.Commands)
		return persistValidationEvidence(store, validation, started)
	}

	commandPath, err := security.SafeJoin(cfg.CWD, "scripts/validate.sh", security.PathPolicy{})
	if err != nil {
		validation.Status = validationStatusMissing
		validation.EvidenceStatus = validationEvidenceMissing
		validation.Reason = "validation command path is not inside workspace"
		validation.Summary = "Raw validation evidence is missing because the validation command path is invalid."
		return persistValidationEvidence(store, validation, started)
	}
	if info, err := os.Stat(commandPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			validation.Status = validationStatusMissing
			validation.EvidenceStatus = validationEvidenceMissing
			validation.Reason = "validation command was declared but scripts/validate.sh was not found"
			validation.Summary = "Raw validation evidence is missing because scripts/validate.sh was not found."
			validation.Commands = []ManifestValidationCommand{{
				Label:    validationLabel(command),
				Name:     commandName(defaultValidationCommand),
				Provider: "local",
				CWD:      "[workspace]",
				Argv:     sanitizedValidationArgv(command, cfg.CWD, store.RunDir),
				Status:   validationStatusMissing,
				Summary:  validation.Summary,
			}}
			validation.CommandCount = len(validation.Commands)
			return persistValidationEvidence(store, validation, started)
		}
		return validation, err
	} else if info.IsDir() {
		validation.Status = validationStatusMissing
		validation.EvidenceStatus = validationEvidenceMissing
		validation.Reason = "validation command path is a directory"
		validation.Summary = "Raw validation evidence is missing because scripts/validate.sh is a directory."
		return persistValidationEvidence(store, validation, started)
	}

	validation.Ran = true
	validation.Skipped = false
	validation.EvidenceStatus = validationEvidenceRecorded
	cmdResult, err := executeValidationCommand(ctx, cfg, command, store, 1)
	if err != nil {
		return validation, err
	}
	validation.Commands = []ManifestValidationCommand{cmdResult}
	validation.CommandCount = 1
	if cmdResult.Status == validationStatusPassed {
		validation.Status = validationStatusPassed
		validation.PassedCount = 1
	} else {
		validation.Status = validationStatusFailed
		validation.FailedCount = 1
	}
	validation.Summary = validationSummarySentence(validation)
	return persistValidationEvidence(store, validation, started)
}

func validationCommandsFromTask(markdown string) []string {
	var commands []string
	seen := map[string]bool{}
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		field, value, ok := splitTaskField(strings.TrimPrefix(trimmed, "- "))
		if !ok || !strings.EqualFold(field, "Validation command") {
			continue
		}
		command := cleanValidationCommand(value)
		if command == "" || seen[command] {
			continue
		}
		seen[command] = true
		commands = append(commands, command)
	}
	return commands
}

func cleanValidationCommand(command string) string {
	command = strings.TrimSpace(command)
	for {
		next := strings.Trim(command, "`")
		next = strings.Trim(next, `"'`)
		next = strings.TrimSpace(next)
		if next == command {
			break
		}
		command = next
	}
	return command
}

func supportedValidationCommand(command string) (string, bool) {
	command = cleanValidationCommand(command)
	switch command {
	case "./scripts/validate.sh", "scripts/validate.sh":
		return defaultValidationCommand, true
	default:
		return command, false
	}
}

func sanitizedValidationArgv(command, cwd, runDir string) []string {
	fields := strings.Fields(cleanValidationCommand(command))
	return security.SanitizeCommandArgv(
		fields,
		security.CommandPathRoot{Path: runDir, Label: "[run]"},
		security.CommandPathRoot{Path: cwd, Label: "[workspace]"},
	)
}

func executeValidationCommand(ctx context.Context, cfg Config, command string, store artifact.Store, index int) (ManifestValidationCommand, error) {
	label := validationLabel(command)
	stdoutRel := fmt.Sprintf("validation/%03d-%s.stdout.txt", index, safeValidationName(label))
	stderrRel := fmt.Sprintf("validation/%03d-%s.stderr.txt", index, safeValidationName(label))
	executable, _ := supportedValidationCommand(command)
	result := ManifestValidationCommand{
		Label:      label,
		Name:       commandName(executable),
		Provider:   "local",
		CWD:        "[workspace]",
		RunID:      redactSecrets(cfg.RunID),
		Argv:       sanitizedValidationArgv(executable, cfg.CWD, store.RunDir),
		StdoutPath: stdoutRel,
		StderrPath: stderrRel,
	}

	var stdout, stderr bytes.Buffer
	commandCWD, err := security.ResolveCommandCWD(cfg.CWD)
	if err != nil {
		return result, err
	}
	commandPath, err := security.SafeJoin(commandCWD, "scripts/validate.sh", security.PathPolicy{})
	if err != nil {
		return result, err
	}
	cmdCtx, cancel := context.WithTimeout(commandContext(ctx), defaultValidationCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, commandPath)
	cmd.Dir = commandCWD
	cmd.Env = security.FilterEnv(os.Environ())
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	result.DurationMS = time.Since(start).Milliseconds()
	result.ExitCode = validationExitCode(err)
	if err == nil {
		result.Status = validationStatusPassed
	} else {
		result.Status = validationStatusFailed
		switch {
		case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
			result.Error = "validation command timed out"
		case errors.Is(cmdCtx.Err(), context.Canceled):
			result.Error = "validation command cancelled"
		default:
			result.Error = redactSecrets(err.Error())
		}
	}
	result.Summary = validationCommandSummary(result, len(stdout.Bytes()), len(stderr.Bytes()))

	roots := []security.CommandPathRoot{
		{Path: store.RunDir, Label: "[run]"},
		{Path: cfg.CWD, Label: "[workspace]"},
	}
	stdoutText, stdoutReport := security.SanitizeDisplayStringWithReport(stdout.String(), roots...)
	stderrText, stderrReport := security.SanitizeDisplayStringWithReport(stderr.String(), roots...)
	store.RecordRedactionReport(stdoutReport)
	store.RecordRedactionReport(stderrReport)

	if _, writeErr := store.WriteFile(stdoutRel, []byte(stdoutText)); writeErr != nil {
		return result, writeErr
	}
	if _, writeErr := store.WriteFile(stderrRel, []byte(stderrText)); writeErr != nil {
		return result, writeErr
	}
	return result, nil
}

func validationExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func validationLabel(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "validation"
	}
	label := filepath.Base(fields[0])
	label = strings.TrimSuffix(label, filepath.Ext(label))
	label = strings.TrimSpace(label)
	if label == "" || label == "." || label == string(filepath.Separator) {
		return "validation"
	}
	return label
}

var unsafeValidationName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeValidationName(label string) string {
	label = unsafeValidationName.ReplaceAllString(label, "-")
	label = strings.Trim(label, ".-")
	if label == "" {
		return "validation"
	}
	return label
}

func validationCommandSummary(result ManifestValidationCommand, stdoutBytes, stderrBytes int) string {
	return fmt.Sprintf("%s: %s (exit %d, %dms, stdout %d bytes, stderr %d bytes)", result.Label, result.Status, result.ExitCode, result.DurationMS, stdoutBytes, stderrBytes)
}

func validationSummarySentence(validation ManifestValidation) string {
	if len(validation.Commands) == 0 {
		return emptyFallback(validation.Summary, "Raw validation evidence was not recorded.")
	}
	parts := make([]string, 0, len(validation.Commands))
	for _, command := range validation.Commands {
		parts = append(parts, command.Summary)
	}
	return strings.Join(parts, "; ")
}

func persistValidationEvidence(store artifact.Store, validation ManifestValidation, started time.Time) (ManifestValidation, error) {
	finished := time.Now().UTC()
	if validation.Summary == "" {
		validation.Summary = validationSummarySentence(validation)
	}
	artifactValue := validationResultsArtifact{
		SchemaVersion:      manifestSchemaVersion,
		StartedAt:          started.Format(time.RFC3339),
		FinishedAt:         finished.Format(time.RFC3339),
		DurationMS:         finished.Sub(started).Milliseconds(),
		ManifestValidation: validation,
	}
	data, err := json.MarshalIndent(security.RedactJSONValue(artifactValue), "", "  ")
	if err != nil {
		return validation, err
	}
	if _, err := store.WriteFile(validation.ResultsPath, append(data, '\n')); err != nil {
		return validation, err
	}
	if _, err := store.WriteString(validation.SummaryPath, renderValidationSummary(validation)); err != nil {
		return validation, err
	}
	return validation, nil
}

func renderValidationSummary(validation ManifestValidation) string {
	var b strings.Builder
	b.WriteString("# Validation Evidence\n\n")
	b.WriteString("## Status\n\n")
	b.WriteString("- Status: " + emptyFallback(validation.Status, validationStatusSkipped) + "\n")
	b.WriteString("- Evidence: " + emptyFallback(validation.EvidenceStatus, validationEvidenceSkipped) + "\n")
	if validation.Reason != "" {
		b.WriteString("- Reason: " + redactSecrets(validation.Reason) + "\n")
	}
	if validation.Summary != "" {
		b.WriteString("- Summary: " + redactSecrets(validation.Summary) + "\n")
	}
	b.WriteString("\n## Commands\n\n")
	if len(validation.Commands) == 0 {
		b.WriteString("- (none)\n")
		return redactSecrets(b.String())
	}
	for _, command := range validation.Commands {
		b.WriteString("- ")
		b.WriteString(command.Label)
		b.WriteString(": ")
		b.WriteString(command.Status)
		if command.Command != "" {
			b.WriteString(" command `")
			b.WriteString(redactSecrets(command.Command))
			b.WriteString("`")
		}
		b.WriteString(fmt.Sprintf(" exit %d duration %dms", command.ExitCode, command.DurationMS))
		if command.StdoutPath != "" {
			b.WriteString(" stdout ")
			b.WriteString(command.StdoutPath)
		}
		if command.StderrPath != "" {
			b.WriteString(" stderr ")
			b.WriteString(command.StderrPath)
		}
		if command.Error != "" {
			b.WriteString(" error ")
			b.WriteString(redactSecrets(command.Error))
		}
		b.WriteByte('\n')
	}
	return redactSecrets(b.String())
}

func validationEvidenceMarkdown(validation ManifestValidation) []string {
	switch validation.EvidenceStatus {
	case validationEvidenceRecorded:
		items := []string{"Raw validation evidence recorded in " + emptyFallback(validation.ResultsPath, "validation/results.json") + "."}
		for _, command := range validation.Commands {
			item := command.Summary
			if item == "" {
				item = fmt.Sprintf("%s: %s", command.Label, command.Status)
			}
			if command.StdoutPath != "" || command.StderrPath != "" {
				item += fmt.Sprintf(" Evidence: stdout=%s stderr=%s.", command.StdoutPath, command.StderrPath)
			}
			items = append(items, item)
		}
		return items
	case validationEvidenceMissing:
		return []string{"Raw validation evidence missing: " + emptyFallback(validation.Reason, "validation output was not available") + "."}
	case validationEvidenceSkipped:
		return []string{"Raw validation evidence skipped: " + emptyFallback(validation.Reason, "validation did not run") + "."}
	default:
		return []string{"Raw validation evidence was not recorded."}
	}
}

func validationEvidenceForPrompt(validation ManifestValidation) string {
	return strings.Join(validationEvidenceMarkdown(validation), "\n")
}

func recordValidationArtifacts(validation ManifestValidation, recordRel func(string, string)) {
	if validation.ResultsPath != "" {
		recordRel("validation_results", validation.ResultsPath)
	}
	if validation.SummaryPath != "" {
		recordRel("validation_summary", validation.SummaryPath)
	}
	for i, command := range validation.Commands {
		prefix := fmt.Sprintf("validation_%03d", i+1)
		if command.StdoutPath != "" {
			recordRel(prefix+"_stdout", command.StdoutPath)
		}
		if command.StderrPath != "" {
			recordRel(prefix+"_stderr", command.StderrPath)
		}
	}
}
