// Package client contains the shared process boundary for Arkey-owned client snapshots.
package client

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	Codex  = "codex"
	Claude = "claude"
	Kimi   = "kimi"
)

type Plan struct {
	Binary string
	Args   []string
	Env    []string
}

func (p Plan) Validate(label string) error {
	info, err := os.Stat(p.Binary)
	if err != nil {
		return fmt.Errorf("Arkey %s snapshot: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("Arkey %s snapshot is not executable: %s", label, p.Binary)
	}
	return nil
}

func SetEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i := range env {
		if len(env[i]) >= len(prefix) && env[i][:len(prefix)] == prefix {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func StateHome(home, name string) string {
	switch name {
	case Claude:
		return filepath.Join(home, ".claude-arkey")
	case Kimi:
		return filepath.Join(home, ".kimi-arkey")
	default:
		return filepath.Join(home, ".codex-moonbridge")
	}
}
