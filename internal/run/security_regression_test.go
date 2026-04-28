package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

func TestSecurityRegressionPlannerAndImplementationHandoffsAreSanitized(t *testing.T) {
	secret := "handoff-regression-secret-value"
	openAIKey := "sk-proj-handoffregression1234567890"
	tokenLike := "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890QwErTy"
	t.Setenv("JJ_HANDOFF_REGRESSION_TOKEN", secret)
	for _, tc := range []struct {
		name   string
		dryRun bool
	}{
		{name: "dry-run", dryRun: true},
		{name: "full-run", dryRun: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writePlan(t, dir, "plan.md")
			if !tc.dryRun {
				writeValidationScript(t, dir, "printf 'ok\\n'")
			}
			hostile := hostileHandoffPayload(t, secret, openAIKey, tokenLike)
			if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("Plan safely.\n"+hostile+"\n"), 0o644); err != nil {
				t.Fatalf("write hostile plan: %v", err)
			}
			taskJSON := `{"version":1,"tasks":[{"title":"Handoff task","mode":"security","priority":"high","status":"queued","reason":` +
				strconv.Quote(hostile) + `,"acceptance_criteria":[` + strconv.Quote(hostile) + `],"validation_command":"./scripts/validate.sh"}]}`
			planner := &fakePlanner{secret: secret, mergeTask: taskJSON}
			codexRunner := &fakeCodexRunner{}

			result, err := Execute(context.Background(), Config{
				PlanPath:               filepath.Join(dir, "plan.md"),
				CWD:                    dir,
				RunID:                  "handoff-regression-" + strings.ReplaceAll(tc.name, "-", ""),
				PlanningAgents:         1,
				PlanningAgentsExplicit: true,
				OpenAIModel:            "test-model",
				AllowNoGit:             true,
				AllowNoGitExplicit:     true,
				DryRun:                 tc.dryRun,
				DryRunExplicit:         true,
				AdditionalPlanContext:  hostile,
				Stdout:                 io.Discard,
				Stderr:                 io.Discard,
				Planner:                planner,
				CodexRunner:            codexRunner,
			})
			if err != nil {
				t.Fatalf("execute handoff regression run: %v", err)
			}
			for _, req := range planner.draftRequests {
				assertHandoffClean(t, "draft request", req.Plan, secret, openAIKey, tokenLike)
			}
			assertHandoffClean(t, "merge request", planner.lastMergeRequest.Plan, secret, openAIKey, tokenLike)
			if tc.dryRun {
				if codexRunner.called {
					t.Fatal("dry-run should not invoke implementation Codex")
				}
			} else {
				if !codexRunner.called {
					t.Fatal("full-run should invoke implementation Codex")
				}
				assertHandoffClean(t, "implementation prompt", codexRunner.lastRequest.Prompt, secret, openAIKey, tokenLike)
				if len(planner.reconcileRequests) != 1 {
					t.Fatalf("expected one sanitized reconcile request, got %d", len(planner.reconcileRequests))
				}
				reconcile := planner.reconcileRequests[0]
				assertHandoffClean(t, "reconcile codex summary", reconcile.CodexSummary, secret, openAIKey, tokenLike)
				assertHandoffClean(t, "reconcile git diff", reconcile.GitDiffSummary, secret, openAIKey, tokenLike)
				assertHandoffClean(t, "reconcile validation", reconcile.ValidationSummary, secret, openAIKey, tokenLike)
			}
			assertTreeCleanOfSecurityLeaks(t, result.RunDir, []string{
				secret,
				openAIKey,
				tokenLike,
				"-----BEGIN PRIVATE KEY-----",
				"handoff-private-key-body",
				"./scripts/deploy --token",
				"PATH=/tmp/handoff-env",
				"diff --git",
				"+api_key=",
				"../../" + secret,
			})
		})
	}
}

func TestSecurityRegressionCodexFallbackPlannerPromptsAreSanitized(t *testing.T) {
	secret := "fallback-handoff-secret-value"
	openAIKey := "sk-proj-fallbackhandoff1234567890"
	tokenLike := "QwErTyUiOpAsDfGhJkLzXcVbNm1234567890AaBb"
	t.Setenv("JJ_FALLBACK_HANDOFF_TOKEN", secret)
	for _, tc := range []struct {
		name   string
		dryRun bool
	}{
		{name: "dry-run", dryRun: true},
		{name: "full-run", dryRun: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writePlan(t, dir, "plan.md")
			if !tc.dryRun {
				writeValidationScript(t, dir, "printf 'ok\\n'")
			}
			hostile := hostileHandoffPayload(t, secret, openAIKey, tokenLike)
			if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("Fallback planning.\n"+hostile+"\n"), 0o644); err != nil {
				t.Fatalf("write hostile fallback plan: %v", err)
			}
			plannerRunner := &scriptedCodexPlannerRunner{}
			implementationRunner := &fakeCodexRunner{}

			result, err := Execute(context.Background(), Config{
				PlanPath:               filepath.Join(dir, "plan.md"),
				CWD:                    dir,
				RunID:                  "fallback-handoff-" + strings.ReplaceAll(tc.name, "-", ""),
				PlanningAgents:         1,
				PlanningAgentsExplicit: true,
				CodexModel:             "codex-test-model",
				AllowNoGit:             true,
				AllowNoGitExplicit:     true,
				DryRun:                 tc.dryRun,
				DryRunExplicit:         true,
				AdditionalPlanContext:  hostile,
				Stdout:                 io.Discard,
				Stderr:                 io.Discard,
				PlannerCodexRunner:     plannerRunner,
				CodexRunner:            implementationRunner,
			})
			if err != nil {
				t.Fatalf("execute fallback handoff run: %v", err)
			}
			plannerRunner.mu.Lock()
			calls := append([]codex.Request(nil), plannerRunner.calls...)
			plannerRunner.mu.Unlock()
			if len(calls) == 0 {
				t.Fatal("expected Codex fallback planner calls")
			}
			for _, call := range calls {
				assertHandoffClean(t, filepath.Base(call.OutputLastMessage), call.Prompt, secret, openAIKey, tokenLike)
			}
			if !tc.dryRun {
				if !implementationRunner.called {
					t.Fatal("full-run should invoke implementation Codex")
				}
				assertHandoffClean(t, "implementation prompt", implementationRunner.lastRequest.Prompt, secret, openAIKey, tokenLike)
			}
			assertTreeCleanOfSecurityLeaks(t, result.RunDir, []string{
				secret,
				openAIKey,
				tokenLike,
				"-----BEGIN PRIVATE KEY-----",
				"handoff-private-key-body",
				"./scripts/deploy --token",
				"PATH=/tmp/handoff-env",
				"diff --git",
				"+api_key=",
				"../../" + secret,
			})
		})
	}
}

func TestSecurityReleaseGateGuardedPlanInputsAcceptedInDryRunAndFullRun(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		for _, pathMode := range []string{"relative", "process-cwd", "absolute"} {
			name := pathMode
			if dryRun {
				name += "-dry"
			} else {
				name += "-full"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				invocation := t.TempDir()
				planRel := filepath.Join("plans", "release.md")
				planPath := filepath.Join(dir, planRel)
				if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
					t.Fatalf("mkdir plan dir: %v", err)
				}
				secret := "release-plan-secret-value"
				openAIKey := "sk-proj-releaseplan1234567890"
				privateKeyBody := "release-plan-private-key-body"
				t.Setenv("JJ_RELEASE_PLAN_SECRET", secret)
				planContent := strings.Join([]string{
					"# Release plan",
					"Authorization: Bearer " + secret,
					openAIKey,
					"-----BEGIN PRIVATE KEY-----",
					privateKeyBody,
					"-----END PRIVATE KEY-----",
					"legacy placeholder [REDACTED]",
					"",
				}, "\n")
				if err := os.WriteFile(planPath, []byte(planContent), 0o644); err != nil {
					t.Fatalf("write plan: %v", err)
				}

				planArg := filepath.ToSlash(planRel)
				cwd := dir
				cwdExplicit := true
				chdir := dir
				switch pathMode {
				case "process-cwd":
					cwd = ""
					cwdExplicit = false
				case "absolute":
					planArg = planPath
					chdir = invocation
				}
				oldWD, err := os.Getwd()
				if err != nil {
					t.Fatalf("getwd: %v", err)
				}
				t.Cleanup(func() {
					if err := os.Chdir(oldWD); err != nil {
						t.Fatalf("restore cwd: %v", err)
					}
				})
				if err := os.Chdir(chdir); err != nil {
					t.Fatalf("chdir: %v", err)
				}

				var stdout, stderr bytes.Buffer
				codexRunner := &fakeCodexRunner{secret: secret}
				result, err := Execute(context.Background(), Config{
					PlanPath:               planArg,
					CWD:                    cwd,
					CWDExplicit:            cwdExplicit,
					ConfigSearchDir:        dir,
					RunID:                  "release-plan-" + strings.ReplaceAll(name, "_", "-"),
					PlanningAgents:         1,
					PlanningAgentsExplicit: true,
					OpenAIModel:            "test-model",
					AllowNoGit:             true,
					AllowNoGitExplicit:     true,
					DryRun:                 dryRun,
					DryRunExplicit:         true,
					AdditionalPlanContext:  "Authorization: Bearer " + secret + "\nlegacy [omitted] placeholder",
					Stdout:                 &stdout,
					Stderr:                 &stderr,
					Planner:                &fakePlanner{secret: secret},
					CodexRunner:            codexRunner,
				})
				if err != nil {
					t.Fatalf("execute accepted plan input: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
				}
				if result == nil {
					t.Fatal("expected run result")
				}
				if dryRun && codexRunner.called {
					t.Fatal("dry-run accepted plan input should not run implementation Codex")
				}
				if !dryRun && !codexRunner.called {
					t.Fatal("full-run accepted plan input should run implementation Codex")
				}

				manifest := readManifest(t, filepath.Join(result.RunDir, "manifest.json"))
				if manifest.InputSource != PlanInputSourceFile || manifest.DryRun != dryRun || !manifest.RedactionApplied || !manifest.WorkspaceGuardrailsApplied {
					t.Fatalf("accepted plan input manifest lost security metadata: %#v", manifest)
				}
				forbidden := []string{
					secret,
					openAIKey,
					privateKeyBody,
					"-----BEGIN PRIVATE KEY-----",
					"-----END PRIVATE KEY-----",
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
			})
		}
	}
}

func TestSecurityReleaseGatePlanInputDenialsDoNotPersistArtifactsOrEchoPayloads(t *testing.T) {
	secret := "release-plan-denial-secret-value"
	t.Setenv("JJ_RELEASE_PLAN_DENIAL_SECRET", secret)

	target := t.TempDir()
	invocation := t.TempDir()
	outside := t.TempDir()
	outsidePlan := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(outsidePlan, []byte("outside "+secret+"\n"), 0o644); err != nil {
		t.Fatalf("write outside plan: %v", err)
	}
	invocationPlan := filepath.Join(invocation, "external.md")
	if err := os.WriteFile(invocationPlan, []byte("external "+secret+"\n"), 0o644); err != nil {
		t.Fatalf("write invocation plan: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(target, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.Mkdir(filepath.Join(target, "directory.md"), 0o755); err != nil {
		t.Fatalf("mkdir directory plan: %v", err)
	}
	secretName := "sk-proj-releasegateplan1234567890.md"
	if err := os.WriteFile(filepath.Join(target, secretName), []byte("secret path\n"), 0o644); err != nil {
		t.Fatalf("write secret-looking plan: %v", err)
	}

	cases := []struct {
		name    string
		chdir   string
		planArg string
		leaks   []string
	}{
		{name: "external-relative", chdir: invocation, planArg: "external.md", leaks: []string{invocationPlan, filepath.ToSlash(invocationPlan), "external " + secret}},
		{name: "relative-traversal", chdir: target, planArg: "../outside.md", leaks: []string{"../outside.md"}},
		{name: "encoded-traversal", chdir: target, planArg: "docs%2f..%2foutside.md", leaks: []string{"docs%2f..%2foutside.md"}},
		{name: "absolute-outside", chdir: target, planArg: outsidePlan, leaks: []string{outsidePlan, filepath.ToSlash(outsidePlan), outside, filepath.ToSlash(outside)}},
		{name: "missing", chdir: target, planArg: "missing.md", leaks: []string{"missing.md"}},
		{name: "non-regular", chdir: target, planArg: "directory.md", leaks: []string{filepath.Join(target, "directory.md"), filepath.ToSlash(filepath.Join(target, "directory.md"))}},
		{name: "malformed-control", chdir: target, planArg: "bad\npath.md", leaks: []string{"bad\npath.md"}},
		{name: "secret-looking", chdir: target, planArg: secretName, leaks: []string{secretName, "sk-proj-releasegateplan1234567890"}},
		{name: "token-like", chdir: target, planArg: "AbCdEfGhIjKlMnOpQrStUvWxYz12345678901234.md", leaks: []string{"AbCdEfGhIjKlMnOpQrStUvWxYz12345678901234"}},
	}
	linkPath := filepath.Join(target, "docs", "link.md")
	if err := os.Symlink(outsidePlan, linkPath); err == nil {
		cases = append(cases, struct {
			name    string
			chdir   string
			planArg string
			leaks   []string
		}{name: "symlink-escape", chdir: target, planArg: "docs/link.md", leaks: []string{outsidePlan, filepath.ToSlash(outsidePlan), secret}})
	} else {
		t.Logf("symlink unavailable; skipping symlink denial case: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Chdir(tc.chdir); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			runID := "release-denied-" + strings.ReplaceAll(tc.name, "_", "-")
			var stdout, stderr bytes.Buffer
			result, err := Execute(context.Background(), Config{
				PlanPath:               tc.planArg,
				CWD:                    target,
				CWDExplicit:            true,
				ConfigSearchDir:        target,
				RunID:                  runID,
				PlanningAgents:         1,
				PlanningAgentsExplicit: true,
				OpenAIModel:            "test-model",
				AllowNoGit:             true,
				AllowNoGitExplicit:     true,
				DryRun:                 true,
				DryRunExplicit:         true,
				Stdout:                 &stdout,
				Stderr:                 &stderr,
				Planner:                &fakePlanner{},
				CodexRunner:            &fakeCodexRunner{},
			})
			if err == nil {
				t.Fatalf("expected plan input denial, got result %#v", result)
			}
			if !IsValidationError(err) {
				t.Fatalf("plan input denial should be a validation error, got %T %v", err, err)
			}
			combined := err.Error() + "\n" + stdout.String() + "\n" + stderr.String()
			for _, leaked := range append(tc.leaks,
				secret,
				target,
				filepath.ToSlash(target),
				security.RedactionMarker,
				"[REDACTED]",
				"[redacted]",
				"[omitted]",
				"<hidden>",
				"{removed}",
			) {
				if leaked != "" && strings.Contains(combined, leaked) {
					t.Fatalf("plan input denial leaked %q:\n%s", leaked, combined)
				}
			}
			runDir := filepath.Join(target, ".jj", "runs", runID)
			if _, statErr := os.Stat(runDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("denied plan input persisted run artifacts at %s: %v", runDir, statErr)
			}
		})
	}
}

func TestSecurityReleaseGateValidationFailureOutputIsSanitized(t *testing.T) {
	dir := t.TempDir()
	secret := "release-validation-secret-value"
	openAIKey := "sk-proj-releasevalidation1234567890"
	privateKeyBody := "release-validation-private-key-body"
	t.Setenv("JJ_RELEASE_VALIDATION_SECRET", secret)
	writePlan(t, dir, "plan.md")
	unsafeAbs := filepath.Join(t.TempDir(), "unsafe-"+secret+".txt")
	writeValidationScript(t, dir, strings.Join([]string{
		`printf 'workspace=%s/file.txt\n' "$PWD"`,
		"printf 'secret=" + secret + "\\n'",
		"printf 'path=" + unsafeAbs + "\\n'",
		"printf 'OPENAI_API_KEY=" + openAIKey + "\\n' >&2",
		"printf '%s\\n' '-----BEGIN PRIVATE KEY-----' '" + privateKeyBody + "' '-----END PRIVATE KEY-----' >&2",
		"printf 'legacy [omitted] placeholder\\n' >&2",
		"exit 7",
	}, "\n"))

	result, err := Execute(context.Background(), Config{
		PlanPath:               filepath.Join(dir, "plan.md"),
		CWD:                    dir,
		ConfigSearchDir:        dir,
		RunID:                  "release-validation-failure",
		PlanningAgents:         1,
		PlanningAgentsExplicit: true,
		OpenAIModel:            "test-model",
		AllowNoGit:             true,
		AllowNoGitExplicit:     true,
		Stdout:                 io.Discard,
		Stderr:                 io.Discard,
		Planner: &fakePlanner{mergeTask: strings.Join([]string{
			"# TASK",
			"",
			"- Validation command: ./scripts/validate.sh",
			"",
		}, "\n")},
		CodexRunner: &fakeCodexRunner{},
	})
	if err != nil {
		t.Fatalf("validation failure should complete as partial run: %v", err)
	}
	if result == nil {
		t.Fatal("expected run result")
	}

	manifest := readManifest(t, filepath.Join(result.RunDir, "manifest.json"))
	if manifest.Status != StatusPartial || manifest.Validation.Status != validationStatusFailed || manifest.Validation.FailedCount != 1 || manifest.Validation.PassedCount != 0 {
		t.Fatalf("expected sanitized validation failure counts, got status=%q validation=%#v", manifest.Status, manifest.Validation)
	}
	if len(manifest.Validation.Commands) != 1 {
		t.Fatalf("expected one validation command record, got %#v", manifest.Validation.Commands)
	}
	command := manifest.Validation.Commands[0]
	if command.Command != "" || command.CWD != "[workspace]" || command.Status != validationStatusFailed || command.ExitCode != 7 || command.StdoutPath == "" || command.StderrPath == "" {
		t.Fatalf("validation command record should be sanitized metadata only: %#v", command)
	}
	if strings.Contains(strings.Join(command.Argv, " "), secret) || strings.Contains(strings.Join(command.Argv, " "), dir) {
		t.Fatalf("validation argv leaked raw values: %#v", command.Argv)
	}
	diag := manifest.Security.Diagnostics
	if !diag.CommandMetadataSanitized || !diag.CommandArgvSanitized || diag.RawCommandTextPersisted || diag.RawEnvironmentPersisted || diag.CommandCWDLabel != "[workspace]" {
		t.Fatalf("validation failure diagnostics exposed unsafe command state: %#v", diag)
	}

	stdout := readFile(t, filepath.Join(result.RunDir, filepath.FromSlash(command.StdoutPath)))
	stderr := readFile(t, filepath.Join(result.RunDir, filepath.FromSlash(command.StderrPath)))
	if !strings.Contains(stdout, "workspace=[workspace]/file.txt") || !strings.Contains(stdout, "path=[path]") || !strings.Contains(stdout, security.RedactionMarker) {
		t.Fatalf("validation stdout should retain only safe labels/redaction evidence:\n%s", stdout)
	}
	if !strings.Contains(stderr, security.RedactionMarker) {
		t.Fatalf("validation stderr should retain redaction evidence:\n%s", stderr)
	}
	forbidden := []string{
		secret,
		openAIKey,
		privateKeyBody,
		unsafeAbs,
		filepath.ToSlash(unsafeAbs),
		"-----BEGIN PRIVATE KEY-----",
		"-----END PRIVATE KEY-----",
		"Bearer [jj-omitted]",
		"bearer/[jj-omitted]",
		"[REDACTED]",
		"[redacted]",
		"[omitted]",
		"<hidden>",
		"{removed}",
	}
	assertTreeCleanOfSecurityLeaks(t, result.RunDir, forbidden)
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

func hostileHandoffPayload(t *testing.T, secret, openAIKey, tokenLike string) string {
	t.Helper()
	privateKey := "-----BEGIN PRIVATE KEY-----\nhandoff-private-key-body\n-----END PRIVATE KEY-----"
	unsafeAbs := filepath.Join(t.TempDir(), "outside", "secret.txt")
	return strings.Join([]string{
		"Keep safe handoff context.",
		"Authorization: Bearer " + secret,
		"api_key=" + secret,
		openAIKey,
		tokenLike,
		privateKey,
		"command=./scripts/deploy --token " + secret,
		"PATH=/tmp/handoff-env-" + secret,
		"validation_output=panic at " + unsafeAbs,
		"manifest={\"run_id\":\"attack\",\"error\":\"" + secret + "\"}",
		"diff --git a/config.txt b/config.txt",
		"@@ -1 +1 @@",
		"+api_key=" + secret,
		"denied_path=../../" + secret,
		"unsafe_path=" + unsafeAbs,
	}, "\n")
}

func assertHandoffClean(t *testing.T, label, text, secret, openAIKey, tokenLike string) {
	t.Helper()
	for _, leaked := range []string{
		secret,
		openAIKey,
		tokenLike,
		"-----BEGIN PRIVATE KEY-----",
		"handoff-private-key-body",
		"./scripts/deploy --token",
		"PATH=/tmp/handoff-env",
		"diff --git",
		"+api_key=",
		"../../" + secret,
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("%s leaked %q:\n%s", label, leaked, text)
		}
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
