package run

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestLoadPlan(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	planPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(planPath, []byte("build jj\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	content, abs, err := LoadPlan("plan.md", dir)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	if content != "build jj\n" {
		t.Fatalf("unexpected content %q", content)
	}
	if abs != planPath {
		t.Fatalf("unexpected path %s", abs)
	}
}

func TestLoadNextIntentMissingOrEmptyIsInactive(t *testing.T) {
	dir := t.TempDir()

	missing, err := LoadNextIntent(dir)
	if err != nil {
		t.Fatalf("missing next intent should be ignored: %v", err)
	}
	if missing.Active() {
		t.Fatalf("missing next intent should be inactive: %#v", missing)
	}

	path := filepath.Join(dir, ".jj", "next-intent.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	if err := os.WriteFile(path, []byte(" \n\t\n"), 0o644); err != nil {
		t.Fatalf("write empty next intent: %v", err)
	}
	empty, err := LoadNextIntent(dir)
	if err != nil {
		t.Fatalf("empty next intent should be ignored: %v", err)
	}
	if empty.Active() || empty.Content != "" {
		t.Fatalf("empty next intent should be inactive: %#v", empty)
	}
}

func TestLoadNextIntentLoadsMultilineRedactedMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".jj", "next-intent.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	secret := "sk-proj-prioritysecret1234567890"
	content := "# Next intent\n\nImprove the web UI only.\nOPENAI_API_KEY=" + secret + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write next intent: %v", err)
	}

	intent, err := LoadNextIntent(dir)
	if err != nil {
		t.Fatalf("load next intent: %v", err)
	}
	if !intent.Active() || !strings.Contains(intent.Content, "Improve the web UI only.") {
		t.Fatalf("next intent content missing expected text: %#v", intent)
	}
	if strings.Contains(intent.Content, secret) || !strings.Contains(intent.Content, "[jj-omitted]") {
		t.Fatalf("next intent content should be redacted:\n%s", intent.Content)
	}
}

func TestLoadNextIntentRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".jj"), 0o755); err != nil {
		t.Fatalf("mkdir .jj: %v", err)
	}
	outsidePath := filepath.Join(outside, "next-intent.md")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("write outside next intent: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(dir, ".jj", "next-intent.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := LoadNextIntent(dir)
	if err == nil || !strings.Contains(err.Error(), "symlinked path") {
		t.Fatalf("expected next intent symlink rejection, got %v", err)
	}
	if strings.Contains(err.Error(), outsidePath) || strings.Contains(err.Error(), filepath.ToSlash(outsidePath)) {
		t.Fatalf("next intent rejection leaked outside path: %v", err)
	}
}

func TestLoadNextIntentRejectsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".jj", "next-intent.md")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir next intent path: %v", err)
	}

	_, err := LoadNextIntent(dir)
	if err == nil || !strings.Contains(err.Error(), "read .jj/next-intent.md") {
		t.Fatalf("expected unreadable next intent rejection, got %v", err)
	}
	if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), filepath.ToSlash(path)) {
		t.Fatalf("next intent read error leaked absolute path: %v", err)
	}
}

func TestLoadPlanRejectsInvocationPlanOutsideTargetCWD(t *testing.T) {
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	target := filepath.Join(root, "playground", "workspace")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	planPath := filepath.Join(root, "playground", "plan.md")
	if err := os.WriteFile(planPath, []byte("from invocation cwd\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	_, abs, err := LoadPlan("playground/plan.md", target)
	if err == nil {
		t.Fatal("expected invocation-directory plan outside --cwd to be rejected")
	}
	if abs != "" {
		t.Fatalf("unsafe plan should not return an absolute path, got %s", abs)
	}
	if strings.Contains(err.Error(), planPath) || strings.Contains(err.Error(), filepath.ToSlash(planPath)) {
		t.Fatalf("outside plan rejection leaked path value: %v", err)
	}
}

func TestLoadPlanDoesNotFallBackToTargetCWD(t *testing.T) {
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	target := filepath.Join(root, "workspace")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	planPath := filepath.Join(target, "plan.md")
	if err := os.WriteFile(planPath, []byte("from target cwd\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	_, abs, err := LoadPlan("plan.md", target)
	if err == nil {
		t.Fatal("expected invocation-directory path outside target cwd to be rejected")
	}
	if abs != "" {
		t.Fatalf("unsafe plan should not return an absolute path, got %s", abs)
	}
}

func TestLoadPlanRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	for _, path := range []string{"../plan.md", "docs/../plan.md", "docs%2f..%2fplan.md", "docs/%2e%2e/plan.md", "docs/%252e%252e/plan.md", `docs\plan.md`} {
		if _, _, err := LoadPlan(path, root); err == nil {
			t.Fatalf("expected unsafe plan path %q to be rejected", path)
		}
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "plan.md"), []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("write outside plan: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "plan.md"), filepath.Join(root, "docs", "plan.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := LoadPlan("docs/plan.md", root); err == nil || !strings.Contains(err.Error(), "symlink outside workspace") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestLoadPlanAllowsInternalSymlinkAsCanonicalTarget(t *testing.T) {
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	targetDir := filepath.Join(root, "targets")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir targets: %v", err)
	}
	targetPlan := filepath.Join(targetDir, "plan.md")
	if err := os.WriteFile(targetPlan, []byte("inside symlink\n"), 0o644); err != nil {
		t.Fatalf("write target plan: %v", err)
	}
	link := filepath.Join(root, "docs", "plan.md")
	if err := os.Symlink(filepath.Join("..", "targets", "plan.md"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	content, abs, err := LoadPlan("docs/plan.md", root)
	if err != nil {
		t.Fatalf("load internal symlink plan: %v", err)
	}
	if content != "inside symlink\n" {
		t.Fatalf("unexpected symlink plan content: %q", content)
	}
	if abs != targetPlan {
		t.Fatalf("expected canonical target path %s, got %s", targetPlan, abs)
	}
}

func TestLoadPlanRejectsSymlinkSwapBeforeReadback(t *testing.T) {
	root := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	insidePlan := filepath.Join(root, "inside.md")
	outsidePlan := filepath.Join(outside, "outside-secret-token-1234567890.md")
	if err := os.WriteFile(insidePlan, []byte("inside\n"), 0o644); err != nil {
		t.Fatalf("write inside plan: %v", err)
	}
	if err := os.WriteFile(outsidePlan, []byte("outside secret\n"), 0o644); err != nil {
		t.Fatalf("write outside plan: %v", err)
	}
	link := filepath.Join(root, "docs", "plan.md")
	if err := os.Symlink(insidePlan, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	guarded, err := guardPlanPath("docs/plan.md", root)
	if err != nil {
		t.Fatalf("guard internal symlink: %v", err)
	}
	if guarded.Path != insidePlan {
		t.Fatalf("expected guarded target %s, got %#v", insidePlan, guarded)
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if err := os.Symlink(outsidePlan, link); err != nil {
		t.Fatalf("swap link: %v", err)
	}

	_, err = revalidateGuardedPlanPath(guarded)
	if err == nil || !strings.Contains(err.Error(), "symlink outside workspace") {
		t.Fatalf("expected symlink swap rejection, got %v", err)
	}
	if strings.Contains(err.Error(), outsidePlan) || strings.Contains(err.Error(), "outside-secret-token") {
		t.Fatalf("symlink swap rejection leaked outside path: %v", err)
	}
}

func TestLoadPlanAbsolutePathMustStayInAllowedRoots(t *testing.T) {
	invocation := t.TempDir()
	target := t.TempDir()
	outside := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(invocation); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	invocationPlan := filepath.Join(invocation, "plan.md")
	targetPlan := filepath.Join(target, "target.md")
	outsidePlan := filepath.Join(outside, "unsafe-secret-token-1234567890.md")
	for _, path := range []string{invocationPlan, targetPlan, outsidePlan} {
		if err := os.WriteFile(path, []byte("plan\n"), 0o644); err != nil {
			t.Fatalf("write plan %s: %v", path, err)
		}
	}

	if _, _, err := LoadPlan(invocationPlan, target); err == nil {
		t.Fatal("expected absolute invocation-root plan outside target cwd to be rejected")
	}
	if _, abs, err := LoadPlan(targetPlan, target); err != nil || abs != targetPlan {
		t.Fatalf("absolute target-root plan should load, abs=%s err=%v", abs, err)
	}
	if _, _, err := LoadPlan(outsidePlan, target); err == nil {
		t.Fatal("expected absolute outside plan path to be rejected")
	} else if strings.Contains(err.Error(), outsidePlan) || strings.Contains(err.Error(), "unsafe-secret-token-1234567890") {
		t.Fatalf("outside plan rejection leaked path value: %v", err)
	}
	encodedPlan := filepath.Join(invocation, "docs%2f..%2fplan.md")
	if _, _, err := LoadPlan(encodedPlan, target); err == nil || strings.Contains(err.Error(), encodedPlan) {
		t.Fatalf("expected encoded absolute plan path to be rejected without echoing path, got %v", err)
	}
}

func TestLoadPlanMissing(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	_, _, err = LoadPlan("missing.md", dir)
	if err == nil || !strings.Contains(err.Error(), "read plan file") {
		t.Fatalf("expected missing file error, got %v", err)
	}
	if strings.Contains(err.Error(), filepath.Join(dir, "missing.md")) || strings.Contains(err.Error(), filepath.ToSlash(filepath.Join(dir, "missing.md"))) {
		t.Fatalf("missing file error leaked path: %v", err)
	}
}

func TestLoadPlanRejectsUnreadableFileWithoutLeakingPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission mode behavior differs on Windows")
	}
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	path := filepath.Join(dir, "unreadable.md")
	if err := os.WriteFile(path, []byte("plan\n"), 0o644); err != nil {
		t.Fatalf("write unreadable plan: %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod unreadable: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0o644)
	})
	_, _, err = LoadPlan("unreadable.md", dir)
	if err == nil {
		t.Skip("unreadable file was readable in this environment")
	}
	if !strings.Contains(err.Error(), "read plan file") {
		t.Fatalf("expected read plan file error, got %v", err)
	}
	if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), filepath.ToSlash(path)) {
		t.Fatalf("unreadable file error leaked path: %v", err)
	}
}

func TestLoadPlanRejectsSecretLookingPlanPathWithoutLeaks(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	for _, name := range []string{
		"sk-proj-plansecret1234567890.md",
		"[redacted].md",
		"[jj-omitted].md",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("plan\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		_, _, err := LoadPlan(name, dir)
		if err == nil || !strings.Contains(err.Error(), "plan path is not allowed") {
			t.Fatalf("expected secret-looking path rejection for %q, got %v", name, err)
		}
		if strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "sk-proj") || strings.Contains(err.Error(), "redacted") || strings.Contains(err.Error(), "jj-omitted") {
			t.Fatalf("secret-looking path rejection leaked input %q: %v", name, err)
		}
	}
}

func TestLoadPlanRejectsDirectoryAndNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "directory.md"), 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if _, _, err := LoadPlan("directory.md", dir); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected directory plan rejection, got %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fifo := filepath.Join(dir, "fifo.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if _, _, err := LoadPlan("fifo.md", dir); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected fifo plan rejection, got %v", err)
	}
}

func TestLoadPlanRejectsStdin(t *testing.T) {
	_, _, err := LoadPlan("-", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "stdin input is not supported") {
		t.Fatalf("expected stdin rejection, got %v", err)
	}
}

func TestLoadPlanRejectsNonMarkdownLikePath(t *testing.T) {
	_, _, err := LoadPlan("plan.txt", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "Markdown-like") {
		t.Fatalf("expected Markdown-like validation error, got %v", err)
	}
}
