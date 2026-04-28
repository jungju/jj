package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const untrackedEvidenceMaxBytes int64 = 1024 * 1024

type UntrackedEvidence struct {
	Available bool
	Files     []string
	Captured  []UntrackedCapturedFile
	Skipped   []UntrackedSkippedFile
	Patch     string
	Summary   string
}

type UntrackedCapturedFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

type UntrackedSkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func CaptureUntrackedEvidence(ctx context.Context, cwd string, available bool, runners ...GitRunner) (UntrackedEvidence, error) {
	evidence := UntrackedEvidence{Available: available}
	if !available {
		evidence.Summary = "Untracked evidence unavailable because git metadata is unavailable.\n"
		return evidence, nil
	}

	out, err := chooseGitRunner(runners...).Output(ctx, cwd, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return evidence, fmt.Errorf("git ls-files untracked: %w", err)
	}
	paths := parseNULPaths(out)
	sort.Strings(paths)

	seenFiles := map[string]bool{}
	var patch strings.Builder
	for _, raw := range paths {
		cleanRel, absPath, reason := resolveUntrackedPath(cwd, raw)
		displayPath := displayUntrackedPath(firstNonEmptyString(cleanRel, raw))
		if reason != "" {
			evidence.Skipped = append(evidence.Skipped, UntrackedSkippedFile{Path: displayPath, Reason: redactSecrets(reason)})
			continue
		}
		if !seenFiles[cleanRel] {
			evidence.Files = append(evidence.Files, displayUntrackedPath(cleanRel))
			seenFiles[cleanRel] = true
		}
		data, reason := readUntrackedText(absPath)
		if reason != "" {
			evidence.Skipped = append(evidence.Skipped, UntrackedSkippedFile{Path: displayPath, Reason: redactSecrets(reason)})
			continue
		}
		redactedContent := redactSecrets(string(data))
		writeSyntheticUntrackedPatch(&patch, displayPath, redactedContent)
		evidence.Captured = append(evidence.Captured, UntrackedCapturedFile{
			Path:  displayPath,
			Bytes: int64(len(data)),
		})
	}
	evidence.Patch = patch.String()
	evidence.Summary = renderUntrackedSummary(evidence)
	return evidence, nil
}

func parseNULPaths(out string) []string {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			paths = append(paths, part)
		}
	}
	return paths
}

func resolveUntrackedPath(cwd, raw string) (string, string, string) {
	if raw == "" {
		return "", "", "empty path"
	}
	if strings.ContainsRune(raw, 0) {
		return "", "", "path contains NUL byte"
	}
	slash := filepath.ToSlash(raw)
	if hasControlPathChar(slash) {
		return "", "", "path contains unsupported control character"
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(slash, "/") {
		return "", "", "path is outside workspace"
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(slash)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return clean, "", "path is outside workspace"
	}
	if clean == ".jj" || strings.HasPrefix(clean, ".jj/") {
		return clean, "", "jj internal artifact path"
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return clean, "", "git internal path"
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return clean, "", "workspace path cannot be resolved"
	}
	absPath, err := filepath.Abs(filepath.Join(absCWD, filepath.FromSlash(clean)))
	if err != nil {
		return clean, "", "path cannot be resolved"
	}
	rel, err := filepath.Rel(absCWD, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return clean, "", "path is outside workspace"
	}
	return clean, absPath, ""
}

func readUntrackedText(path string) ([]byte, string) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, "deleted during capture"
	}
	if os.IsPermission(err) {
		return nil, "unreadable file"
	}
	if err != nil {
		return nil, "unreadable file"
	}
	if info.IsDir() {
		return nil, "directory"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, "symlink"
	}
	if !info.Mode().IsRegular() {
		return nil, "not a regular file"
	}
	if info.Size() > untrackedEvidenceMaxBytes {
		return nil, fmt.Sprintf("oversized file (%d bytes exceeds %d byte limit)", info.Size(), untrackedEvidenceMaxBytes)
	}

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, "deleted during capture"
	}
	if os.IsPermission(err) {
		return nil, "unreadable file"
	}
	if err != nil {
		return nil, "unreadable file"
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, untrackedEvidenceMaxBytes+1))
	if err != nil {
		return nil, "unreadable file"
	}
	if int64(len(data)) > untrackedEvidenceMaxBytes {
		return nil, fmt.Sprintf("oversized file exceeds %d byte limit", untrackedEvidenceMaxBytes)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, "binary file"
	}
	if !utf8.Valid(data) {
		return nil, "non-UTF-8 text"
	}
	return data, ""
}

func writeSyntheticUntrackedPatch(b *strings.Builder, displayPath, content string) {
	lines := splitPatchLines(content)
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString("diff --git a/")
	b.WriteString(displayPath)
	b.WriteString(" b/")
	b.WriteString(displayPath)
	b.WriteByte('\n')
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	b.WriteString("+++ b/")
	b.WriteString(displayPath)
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
	for _, line := range lines {
		b.WriteByte('+')
		b.WriteString(strings.TrimSuffix(line, "\n"))
		b.WriteByte('\n')
	}
}

func splitPatchLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func renderUntrackedSummary(e UntrackedEvidence) string {
	if !e.Available {
		return "Untracked evidence unavailable because git metadata is unavailable.\n"
	}
	var b strings.Builder
	b.WriteString("Untracked evidence: available\n")
	b.WriteString(fmt.Sprintf("Listed paths: %d\n", len(e.Files)))
	b.WriteString(fmt.Sprintf("Captured text files: %d\n", len(e.Captured)))
	b.WriteString(fmt.Sprintf("Skipped paths: %d\n", len(e.Skipped)))
	if len(e.Files) == 0 && len(e.Skipped) == 0 {
		b.WriteString("\nNo untracked files reported by git.\n")
	}
	if len(e.Captured) > 0 {
		b.WriteString("\nCaptured:\n")
		for _, item := range e.Captured {
			b.WriteString("- ")
			b.WriteString(item.Path)
			b.WriteString(fmt.Sprintf(" (%d bytes)\n", item.Bytes))
		}
	}
	if len(e.Skipped) > 0 {
		b.WriteString("\nSkipped:\n")
		for _, item := range e.Skipped {
			b.WriteString("- ")
			b.WriteString(item.Path)
			b.WriteString(": ")
			b.WriteString(item.Reason)
			b.WriteByte('\n')
		}
	}
	return redactSecrets(b.String())
}

func (e UntrackedEvidence) Markdown() string {
	var b strings.Builder
	b.WriteString("## git untracked files\n")
	b.WriteString(emptyAsNone(strings.Join(e.Files, "\n")))
	b.WriteString("\n\n## git untracked summary\n")
	b.WriteString(emptyAsNone(strings.TrimSpace(e.Summary)))
	b.WriteString("\n\n## git untracked patch\n")
	b.WriteString(emptyAsNone(strings.TrimSpace(e.Patch)))
	b.WriteByte('\n')
	return redactSecrets(b.String())
}

func displayUntrackedPath(path string) string {
	path = filepath.ToSlash(path)
	replacer := strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`, "\x00", `\0`)
	return redactSecrets(replacer.Replace(path))
}

func hasControlPathChar(path string) bool {
	return strings.ContainsAny(path, "\n\r\t")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
