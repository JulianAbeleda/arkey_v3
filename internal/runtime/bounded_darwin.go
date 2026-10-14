//go:build darwin

package runtime

import (
	"context"
	"errors"
)

type BoundedService struct {
	Runner      CommandRunner
	MemoryBytes uint64
}

func (BoundedService) Available(context.Context) bool { return false }
func (BoundedService) Start(context.Context, string, []string, string) (int, error) {
	return 0, errors.New("managed vLLM requires Linux systemd")
}
func (BoundedService) Stop(context.Context, string) error {
	return errors.New("managed vLLM requires Linux systemd")
}
func MemoryCap(requested uint64) (uint64, error) { return requested, nil }
