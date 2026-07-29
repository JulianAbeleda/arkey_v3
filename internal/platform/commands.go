package platform

import (
	"context"
	"os/exec"
)

// Runner deliberately accepts an argv vector: callers never need a shell.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
