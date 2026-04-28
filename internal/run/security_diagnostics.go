package run

import (
	"errors"
	"sort"
	"strings"

	"github.com/jungju/jj/internal/security"
)

const securityDiagnosticsVersion = "1"

func newManifestSecurityDiagnosticsEnvelope() ManifestSecurity {
	return ManifestSecurity{
		RedactionApplied:           true,
		WorkspaceGuardrailsApplied: true,
		RedactionPolicy:            "shared redaction applied before artifact persistence and dashboard serving",
		PathPolicy:                 "workspace and run artifact paths are canonicalized and traversal, encoded traversal, hidden artifact segments, and symlink escapes are rejected",
		ServePolicy:                "dashboard serves only allowlisted workspace documents and manifest-listed run artifacts",
		CommandPolicy:              "child commands use argv-style execution and persist sanitized command metadata only",
		EnvironmentPolicy:          "child command environments are filtered and raw environment dumps are not persisted",
		Diagnostics:                newManifestSecurityDiagnostics(),
	}
}

func newManifestSecurityDiagnostics() ManifestSecurityDiagnostics {
	return ManifestSecurityDiagnostics{
		Version:  securityDiagnosticsVersion,
		Redacted: true,
		GuardedRoots: []ManifestSecurityRoot{
			{Label: "workspace", Path: "[workspace]"},
			{Label: "run_artifacts", Path: ".jj/runs"},
			{Label: "current_run", Path: "[run]"},
		},
		RootLabels:               []string{"workspace", "run_artifacts", "current_run"},
		DeniedPathCategories:     []string{},
		DeniedPathCategoryCounts: map[string]int{},
		FailureCategories:        []string{},
		FailureCategoryCounts:    map[string]int{},
		CommandCWDLabel:          "[workspace]",
		DryRunParityApplied:      true,
		DryRunParityStatus:       "equivalent",
	}
}

func refreshManifestSecurityDiagnostics(manifest *Manifest, redactionCount int64) {
	if manifest == nil {
		return
	}
	diag := manifest.Security.Diagnostics
	if strings.TrimSpace(diag.Version) == "" {
		diag = newManifestSecurityDiagnostics()
	}
	diag.Redacted = true
	diag.SecretMaterialPresent = redactionCount > 0
	diag.CommandRecordCount = manifestCommandRecordCount(*manifest)
	diag.CommandMetadataSanitized = manifestCommandMetadataSanitized(*manifest)
	diag.CommandArgvSanitized = diag.CommandMetadataSanitized
	diag.RawCommandTextPersisted = !diag.CommandMetadataSanitized
	diag.RawEnvironmentPersisted = false
	if diag.CommandMetadataSanitized {
		diag.CommandSanitizationStatus = "sanitized"
	} else {
		diag.CommandSanitizationStatus = "raw_command_text_present"
	}
	diag.CommandCWDLabel = "[workspace]"
	diag.DryRunParityApplied = true
	diag.DryRunParityStatus = "equivalent"
	manifest.Security.Diagnostics = sanitizeManifestSecurityDiagnostics(diag)
}

func sanitizeManifestSecurityDiagnostics(diag ManifestSecurityDiagnostics) ManifestSecurityDiagnostics {
	if strings.TrimSpace(diag.Version) == "" {
		diag.Version = securityDiagnosticsVersion
	}
	diag.Version = safeSecurityCategory(diag.Version, securityDiagnosticsVersion)
	diag.CommandSanitizationStatus = safeSecurityCategory(diag.CommandSanitizationStatus, "sanitized")
	diag.DryRunParityStatus = safeSecurityCategory(diag.DryRunParityStatus, "equivalent")
	diag.CommandCWDLabel = safeSecurityLabel(diag.CommandCWDLabel, "[workspace]")
	diag.RootLabels = sanitizeSecurityLabels(diag.RootLabels)
	if len(diag.RootLabels) == 0 {
		diag.RootLabels = []string{"workspace", "run_artifacts", "current_run"}
	}
	diag.GuardedRoots = sanitizeSecurityRoots(diag.GuardedRoots)
	if len(diag.GuardedRoots) == 0 {
		diag.GuardedRoots = newManifestSecurityDiagnostics().GuardedRoots
	}
	diag.DeniedPathCategoryCounts = sanitizeSecurityCategoryCounts(diag.DeniedPathCategoryCounts, "path_denied")
	diag.DeniedPathCategories = sortedSecurityCategories(diag.DeniedPathCategoryCounts)
	diag.DeniedPathCount = sumSecurityCounts(diag.DeniedPathCategoryCounts)
	diag.FailureCategoryCounts = sanitizeSecurityCategoryCounts(diag.FailureCategoryCounts, "security_failure")
	diag.FailureCategories = sortedSecurityCategories(diag.FailureCategoryCounts)
	return diag
}

func manifestCommandRecordCount(manifest Manifest) int {
	count := manifest.Validation.CommandCount
	if count == 0 {
		count = len(manifest.Validation.Commands)
	}
	if strings.TrimSpace(manifest.Codex.ExitPath) != "" {
		count++
	}
	return count
}

func manifestCommandMetadataSanitized(manifest Manifest) bool {
	if strings.TrimSpace(manifest.Codex.Error) != "" && strings.Contains(manifest.Codex.Error, security.RedactionMarker) {
		return true
	}
	for _, command := range manifest.Validation.Commands {
		if strings.TrimSpace(command.Command) != "" {
			return false
		}
		if strings.TrimSpace(command.CWD) != "" && strings.TrimSpace(command.CWD) != "[workspace]" {
			return false
		}
		for _, arg := range command.Argv {
			if strings.ContainsRune(arg, 0) || strings.TrimSpace(arg) == "" {
				return false
			}
		}
	}
	return true
}

func recordDeniedPathDiagnostic(diag *ManifestSecurityDiagnostics, category string) {
	if diag == nil {
		return
	}
	if diag.DeniedPathCategoryCounts == nil {
		diag.DeniedPathCategoryCounts = map[string]int{}
	}
	category = safeSecurityCategory(category, "path_denied")
	diag.DeniedPathCategoryCounts[category]++
	diag.DeniedPathCategories = sortedSecurityCategories(diag.DeniedPathCategoryCounts)
	diag.DeniedPathCount = sumSecurityCounts(diag.DeniedPathCategoryCounts)
}

func recordSecurityFailureDiagnostic(diag *ManifestSecurityDiagnostics, category string) {
	if diag == nil {
		return
	}
	if diag.FailureCategoryCounts == nil {
		diag.FailureCategoryCounts = map[string]int{}
	}
	category = safeSecurityCategory(category, "security_failure")
	diag.FailureCategoryCounts[category]++
	diag.FailureCategories = sortedSecurityCategories(diag.FailureCategoryCounts)
}

func setUntrackedDeniedPathDiagnostics(diag *ManifestSecurityDiagnostics, skipped []UntrackedSkippedFile) {
	if diag == nil {
		return
	}
	counts := map[string]int{}
	for _, item := range skipped {
		category := untrackedDeniedPathCategory(item.Reason)
		if category == "" {
			category = "untracked_path_skipped"
		}
		counts[safeSecurityCategory(category, "untracked_path_skipped")]++
	}
	diag.DeniedPathCategoryCounts = counts
	diag.DeniedPathCategories = sortedSecurityCategories(counts)
	diag.DeniedPathCount = sumSecurityCounts(counts)
}

func securityFailureDiagnosticCategory(err error) string {
	switch {
	case errors.Is(err, security.ErrOutsideWorkspace):
		return "outside_workspace"
	case errors.Is(err, security.ErrSymlinkOutside):
		return "symlink_outside_workspace"
	case errors.Is(err, security.ErrSymlinkPath):
		return "symlink_path"
	default:
		return ""
	}
}

func untrackedDeniedPathCategory(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(reason, "outside workspace"):
		return "untracked_outside_workspace"
	case strings.Contains(reason, "jj internal"):
		return "untracked_internal_artifact_path"
	case strings.Contains(reason, "git internal"):
		return "untracked_git_internal_path"
	case strings.Contains(reason, "nul"):
		return "untracked_nul_byte"
	case strings.Contains(reason, "control"):
		return "untracked_control_character"
	case strings.Contains(reason, "symlink"):
		return "untracked_symlink"
	case strings.Contains(reason, "oversized"):
		return "untracked_oversized_file"
	case strings.Contains(reason, "binary"):
		return "untracked_binary_file"
	case strings.Contains(reason, "non-utf-8"):
		return "untracked_non_utf8"
	case strings.Contains(reason, "directory"):
		return "untracked_directory"
	case strings.Contains(reason, "unreadable"):
		return "untracked_unreadable_file"
	case strings.Contains(reason, "deleted"):
		return "untracked_deleted"
	case strings.Contains(reason, "regular file"):
		return "untracked_not_regular_file"
	default:
		return ""
	}
}

func sanitizeSecurityLabels(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		label := safeSecurityCategory(item, "")
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

func sanitizeSecurityRoots(roots []ManifestSecurityRoot) []ManifestSecurityRoot {
	out := make([]ManifestSecurityRoot, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		label := safeSecurityCategory(root.Label, "")
		path := safeSecurityRootPath(root.Path)
		if label == "" || path == "" {
			continue
		}
		key := label + "\x00" + path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ManifestSecurityRoot{Label: label, Path: path})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label == out[j].Label {
			return out[i].Path < out[j].Path
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func sanitizeSecurityCategoryCounts(counts map[string]int, fallback string) map[string]int {
	out := map[string]int{}
	for key, count := range counts {
		if count <= 0 {
			continue
		}
		category := safeSecurityCategory(key, fallback)
		if category == "" {
			category = fallback
		}
		out[category] += count
	}
	return out
}

func sortedSecurityCategories(counts map[string]int) []string {
	categories := make([]string, 0, len(counts))
	for category, count := range counts {
		if strings.TrimSpace(category) != "" && count > 0 {
			categories = append(categories, category)
		}
	}
	sort.Strings(categories)
	return categories
}

func sumSecurityCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		if count > 0 {
			total += count
		}
	}
	return total
}

func safeSecurityRootPath(value string) string {
	value = strings.TrimSpace(redactSecrets(value))
	switch value {
	case "[workspace]", "[run]", ".jj/runs":
		return value
	default:
		return ""
	}
}

func safeSecurityLabel(value, fallback string) string {
	if strings.TrimSpace(value) == "[workspace]" || strings.TrimSpace(value) == "[run]" {
		return strings.TrimSpace(value)
	}
	return safeSecurityCategory(value, fallback)
}

func safeSecurityCategory(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(redactSecrets(value)))
	if value == "" || strings.Contains(value, security.RedactionMarker) {
		return fallback
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if r == '-' || r == '_' {
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return fallback
	}
	return out
}
