package run

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jungju/jj/internal/artifact"
	"github.com/jungju/jj/internal/codex"
	"github.com/jungju/jj/internal/security"
)

func TestSecurityRegressionDryRunAndFullRunRedactPersistedSurfaces(t *testing.T) {
	cases := []struct {
		name   string
		dryRun bool
	}{
		{name: "dry-run", dryRun: true},
		{name: "full-run", dryRun: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if !tc.dryRun {
				dir = initGit(t)
				prepareCommittedWorkspace(t, dir)
			}
			secret := "security-regression-secret-value"
			openAIKey := "sk-proj-securityregression1234567890"
			privateKeyBody := "private-key-body-security-regression"
			t.Setenv("JJ_SECURITY_REGRESSION_TOKEN", secret)
			writePlan(t, dir, "plan.md")
			writeSecurityRegressionPlan(t, dir, secret, openAIKey, privateKeyBody)
			writeJJRC(t, dir, `{"codex_model":"`+secret+`","codex_bin":"`+secret+`"}`)

			codexRunner := &fakeCodexRunner{
				secret: secret,
				mutate: !tc.dryRun,
				files: map[string][]byte{
					"generated-secret.txt": []byte("api_key=" + secret + "\n"),
				},
			}
			result, err := Execute(context.Background(), Config{
				PlanPath:               filepath.Join(dir, "plan.md"),
				CWD:                    dir,
				ConfigSearchDir:        dir,
				RunID:                  "security-regression-" + strings.ReplaceAll(tc.name, "-", ""),
				PlanningAgents:         1,
				PlanningAgentsExplicit: true,
				OpenAIModel:            "test-model",
				AllowNoGit:             true,
				AllowNoGitExplicit:     true,
				DryRun:                 tc.dryRun,
				DryRunExplicit:         true,
				AdditionalPlanContext:  "authorization=Bearer " + secret + "\nlegacy [REDACTED] placeholder",
				Stdout:                 io.Discard,
				Stderr:                 io.Discard,
				Planner:                &fakePlanner{secret: secret},
				CodexRunner:            codexRunner,
			})
			if err != nil {
				t.Fatalf("execute security regression run: %v", err)
			}
			if tc.dryRun && codexRunner.called {
				t.Fatal("dry-run should not invoke implementation Codex")
			}
			if !tc.dryRun && !codexRunner.called {
				t.Fatal("full-run should invoke implementation Codex")
			}

			forbidden := []string{
				secret,
				openAIKey,
				privateKeyBody,
				"Bearer [jj-omitted]",
				"bearer/[jj-omitted]",
				"[REDACTED]",
				"[redacted]",
				"[omitted]",
				"<hidden>",
				"{removed}",
			}
			assertTreeCleanOfSecurityLeaks(t, result.RunDir, forbidden)
			assertTreeContains(t, result.RunDir, security.RedactionMarker)

			for _, rel := range []string{DefaultSpecStatePath, DefaultTasksStatePath} {
				path := filepath.Join(dir, filepath.FromSlash(rel))
				if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
					if tc.dryRun {
						continue
					}
					t.Fatalf("expected full-run workspace state %s to exist", rel)
				}
				assertFileCleanOfSecurityLeaks(t, path, forbidden)
			}

			manifest := readManifest(t, filepath.Join(result.RunDir, "manifest.json"))
			if !manifest.RedactionApplied || !manifest.WorkspaceGuardrailsApplied || !manifest.Security.RedactionApplied || !manifest.Security.WorkspaceGuardrailsApplied {
				t.Fatalf("manifest missing redaction/boundary metadata: %#v", manifest.Security)
			}
			if manifest.RedactionCount == 0 || manifest.Security.RedactionCount == 0 || len(manifest.RedactionKinds) == 0 || len(manifest.Security.RedactionKinds) == 0 {
				t.Fatalf("manifest should report redaction metadata: top=%d/%#v security=%d/%#v", manifest.RedactionCount, manifest.RedactionKinds, manifest.Security.RedactionCount, manifest.Security.RedactionKinds)
			}
			diag := manifest.Security.Diagnostics
			if diag.Version != securityDiagnosticsVersion || !diag.Redacted || !diag.SecretMaterialPresent {
				t.Fatalf("manifest missing safe security diagnostics: %#v", diag)
			}
			if diag.CommandSanitizationStatus != "sanitized" || !diag.CommandMetadataSanitized || !diag.CommandArgvSanitized || diag.RawCommandTextPersisted || diag.RawEnvironmentPersisted {
				t.Fatalf("manifest command diagnostics should be sanitized metadata only: %#v", diag)
			}
			if !diag.DryRunParityApplied || diag.DryRunParityStatus != "equivalent" {
				t.Fatalf("manifest should record dry-run parity diagnostics: %#v", diag)
			}
			if !containsString(diag.RootLabels, "workspace") || !containsString(diag.RootLabels, "run_artifacts") || !containsString(diag.RootLabels, "current_run") {
				t.Fatalf("manifest should expose guarded root labels only: %#v", diag.RootLabels)
			}
		})
	}
}

func TestSecurityRegressionDiagnosticsNormalizeStringsWithoutLeaks(t *testing.T) {
	secret := "diagnostic-secret-value"
	t.Setenv("JJ_DIAGNOSTIC_SECRET", secret)
	diag := ManifestSecurityDiagnostics{
		Version:                   "1",
		RootLabels:                []string{"workspace", "token=" + secret},
		GuardedRoots:              []ManifestSecurityRoot{{Label: "workspace", Path: "[workspace]"}, {Label: "outside", Path: "/tmp/" + secret}},
		DeniedPathCategoryCounts:  map[string]int{"outside workspace " + secret: 1},
		FailureCategoryCounts:     map[string]int{"failed with " + secret: 1},
		CommandSanitizationStatus: "token=" + secret,
		CommandCWDLabel:           "[workspace]",
		DryRunParityStatus:        "equivalent",
	}

	got := sanitizeManifestSecurityDiagnostics(diag)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}
	text := string(data)
	for _, leaked := range []string{secret, security.RedactionMarker, "/tmp/"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("diagnostics leaked %q:\n%s", leaked, text)
		}
	}
	if got.CommandSanitizationStatus != "sanitized" || !containsString(got.RootLabels, "workspace") || containsString(got.RootLabels, "token") {
		t.Fatalf("diagnostics should fall back to safe labels/categories: %#v", got)
	}
	if got.DeniedPathCount != 1 || !containsString(got.DeniedPathCategories, "path_denied") || !containsString(got.FailureCategories, "security_failure") {
		t.Fatalf("diagnostics should retain sanitized counts/categories: %#v", got)
	}
}

func TestSecurityRegressionCommandRecordsAreMetadataOnly(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".jj", "runs", "command-record")
	secret := "command-record-secret-value"
	t.Setenv("JJ_COMMAND_RECORD_TOKEN", secret)

	record := codexExitArtifact("command-record", runDir, codex.Request{
		Bin:               "codex",
		CWD:               root,
		Model:             secret,
		Prompt:            "prompt includes " + secret,
		EventsPath:        filepath.Join(runDir, "codex", "events.jsonl"),
		OutputLastMessage: filepath.Join(runDir, "codex", "summary.md"),
		AllowNoGit:        true,
	}, "failed", codex.Result{ExitCode: 9, DurationMS: 42}, errors.New("failed with token="+secret))

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal command record: %v", err)
	}
	text := string(data)
	for _, leaked := range []string{secret, root, filepath.ToSlash(root), "prompt includes", "JJ_COMMAND_RECORD_TOKEN"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("command record leaked %q:\n%s", leaked, text)
		}
	}
	for _, forbiddenKey := range []string{`"env"`, `"environment"`, `"prompt"`, `"stdin"`, `"stdout"`, `"stderr"`} {
		if strings.Contains(text, forbiddenKey) {
			t.Fatalf("command record persisted forbidden payload key %s:\n%s", forbiddenKey, text)
		}
	}
	if record.CWD != "[workspace]" || record.Status != "failed" || record.ExitCode != 9 || record.DurationMS != 42 || len(record.Argv) == 0 {
		t.Fatalf("command record lost required metadata: %#v", record)
	}
	joinedArgv := strings.Join(record.Argv, " ")
	if !strings.Contains(joinedArgv, "[workspace]") || !strings.Contains(joinedArgv, "[run]/codex/summary.md") || !strings.Contains(joinedArgv, "--output-last-message") {
		t.Fatalf("sanitized argv missing expected metadata: %#v", record.Argv)
	}
}

func TestSecurityRegressionValidationRecordsOmitCommandText(t *testing.T) {
	root := t.TempDir()
	store, err := artifact.NewStore(root, "validation-command-record")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	secret := "validation-command-secret-value"
	t.Setenv("JJ_VALIDATION_COMMAND_TOKEN", secret)

	validation, err := runValidationEvidenceCommands(context.Background(), Config{
		CWD:   root,
		RunID: "validation-command-record",
	}, store, []string{"./scripts/validate.sh --token " + secret})
	if err != nil {
		t.Fatalf("record validation evidence: %v", err)
	}
	if len(validation.Commands) != 1 || validation.Commands[0].Command != "" {
		t.Fatalf("validation command text should not be retained: %#v", validation.Commands)
	}

	results := readFile(t, filepath.Join(store.RunDir, "validation", "results.json"))
	summary := readFile(t, filepath.Join(store.RunDir, "validation", "summary.md"))
	for _, text := range []string{results, summary} {
		if strings.Contains(text, secret) || strings.Contains(text, "JJ_VALIDATION_COMMAND_TOKEN") || strings.Contains(text, `"command":`) {
			t.Fatalf("validation command record leaked command payload:\n%s", text)
		}
	}
	if !strings.Contains(results, `"argv"`) || !strings.Contains(results, security.RedactionMarker) {
		t.Fatalf("validation command record should retain sanitized argv metadata:\n%s", results)
	}
}

func TestSecurityRegressionCodexArtifactSymlinkRejectedBeforeRead(t *testing.T) {
	dir := t.TempDir()
	runID := "codex-artifact-symlink"
	writePlan(t, dir, "plan.md")
	outside := t.TempDir()
	secret := "codex-artifact-symlink-secret"
	outsideEvents := filepath.Join(outside, "events.jsonl")
	if err := os.WriteFile(outsideEvents, []byte("outside "+secret+"\n"), 0o644); err != nil {
		t.Fatalf("write outside events: %v", err)
	}
	probeLink := filepath.Join(outside, "probe-link")
	if err := os.Symlink(outsideEvents, probeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_ = os.Remove(probeLink)

	result, err := Execute(context.Background(), Config{
		PlanPath:               filepath.Join(dir, "plan.md"),
		CWD:                    dir,
		RunID:                  runID,
		PlanningAgents:         1,
		PlanningAgentsExplicit: true,
		OpenAIModel:            "test-model",
		AllowNoGit:             true,
		AllowNoGitExplicit:     true,
		Stdout:                 io.Discard,
		Stderr:                 io.Discard,
		Planner:                &fakePlanner{},
		CodexRunner: maliciousCodexSymlinkRunner{
			eventsTarget: outsideEvents,
		},
	})
	if err == nil {
		t.Fatal("expected symlinked Codex artifact to fail")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), outside) || strings.Contains(err.Error(), filepath.ToSlash(outside)) {
		t.Fatalf("codex symlink rejection leaked unsafe value: %v", err)
	}
	if result == nil {
		t.Fatal("expected partial result with run directory")
	}

	manifestPath := filepath.Join(result.RunDir, "manifest.json")
	manifestData := readFile(t, manifestPath)
	for _, leaked := range []string{secret, outside, filepath.ToSlash(outside), outsideEvents, filepath.ToSlash(outsideEvents)} {
		if strings.Contains(manifestData, leaked) {
			t.Fatalf("manifest leaked symlink target data %q:\n%s", leaked, manifestData)
		}
	}
	manifest := readManifest(t, manifestPath)
	if manifest.Security.Diagnostics.DeniedPathCategoryCounts["symlink_path"] == 0 {
		t.Fatalf("expected symlink_path diagnostic, got %#v", manifest.Security.Diagnostics)
	}
}

type maliciousCodexSymlinkRunner struct {
	eventsTarget string
}

func (m maliciousCodexSymlinkRunner) Run(_ context.Context, req codex.Request) (codex.Result, error) {
	if err := os.MkdirAll(filepath.Dir(req.EventsPath), 0o755); err != nil {
		return codex.Result{}, err
	}
	if err := os.Remove(req.EventsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return codex.Result{}, err
	}
	if err := os.Symlink(m.eventsTarget, req.EventsPath); err != nil {
		return codex.Result{}, err
	}
	if err := os.WriteFile(req.OutputLastMessage, []byte("summary\n"), 0o644); err != nil {
		return codex.Result{}, err
	}
	return codex.Result{Summary: "summary", ExitCode: 0, DurationMS: 1}, nil
}

func writeSecurityRegressionPlan(t *testing.T, dir, secret, openAIKey, privateKeyBody string) {
	t.Helper()
	privateKey := "-----BEGIN PRIVATE KEY-----\n" + privateKeyBody + "\n-----END PRIVATE KEY-----"
	content := strings.Join([]string{
		"# Security regression plan",
		"Authorization: Bearer " + secret,
		"standalone Bearer " + secret,
		"callback bearer/" + secret,
		"api_key=" + secret,
		openAIKey,
		privateKey,
		"provider returned [omitted] and <hidden>",
		"provider returned {removed}",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write security plan: %v", err)
	}
}

func assertTreeCleanOfSecurityLeaks(t *testing.T, root string, forbidden []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		assertFileCleanOfSecurityLeaks(t, path, forbidden)
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
}

func assertFileCleanOfSecurityLeaks(t *testing.T, path string, forbidden []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("%s leaked forbidden material %q:\n%s", path, needle, text)
		}
	}
}

func assertTreeContains(t *testing.T, root, needle string) {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || found {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	if !found {
		t.Fatalf("expected at least one persisted artifact under %s to contain %q", root, needle)
	}
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
