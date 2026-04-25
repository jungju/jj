package codex

import (
	"reflect"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	args := BuildArgs(Request{
		CWD:               "/tmp/repo",
		Model:             "gpt-5.4-mini",
		OutputLastMessage: "/tmp/run/codex-summary.md",
		AllowNoGit:        true,
	})
	want := []string{
		"exec",
		"--cd", "/tmp/repo",
		"--json",
		"--output-last-message", "/tmp/run/codex-summary.md",
		"--sandbox", "workspace-write",
		"--ask-for-approval", "never",
		"--model", "gpt-5.4-mini",
		"--skip-git-repo-check",
		"-",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args mismatch\nwant: %#v\n got: %#v", want, args)
	}
}
