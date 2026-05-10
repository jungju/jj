package jjctl

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

func runCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return stdout.String(), &commandError{Err: err, Stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.String(), err
}

type commandError struct {
	Err    error
	Stderr string
}

func (e *commandError) Error() string {
	return e.Err.Error() + ": " + e.Stderr
}

func (e *commandError) Unwrap() error {
	return e.Err
}
