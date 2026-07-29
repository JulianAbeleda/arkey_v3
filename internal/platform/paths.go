// Package platform contains OS-facing primitives kept outside the UI.
package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct{ Home, ConfigDir, StateDir, ModelDir, LocalState string }

func DefaultPaths(home string) Paths {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	configDir := filepath.Join(config, "arkey")
	if override := os.Getenv("ARKEY_CONFIG_DIR"); override != "" {
		configDir = override
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	return Paths{Home: home, ConfigDir: configDir, StateDir: filepath.Join(state, "arkey"), ModelDir: filepath.Join(home, "models"), LocalState: os.Getenv("ARKEY_LOCAL_STATE_DIR")}
}
func (p Paths) ConfigFile() string       { return filepath.Join(p.ConfigDir, "config.toml") }
func (p Paths) MoonBridgeConfig() string { return filepath.Join(p.ConfigDir, "moonbridge.yml") }
func (p Paths) LocalStateDir() string {
	if p.LocalState != "" {
		return p.LocalState
	}
	return filepath.Join(p.StateDir, "local")
}
func (p Paths) LogsDir() string { return filepath.Join(p.StateDir, "logs") }

// RejectSymlinkComponents rejects every existing path component. This keeps
// private state and configuration writes from escaping through a parent link.
func RejectSymlinkComponents(path string) error {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := string(filepath.Separator)
	if volume != "" {
		current = volume + string(filepath.Separator)
	}
	rel := strings.TrimPrefix(abs, current)
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing symlinked path component: " + current)
		}
	}
	return nil
}

func EnsurePrivateDir(path string) error {
	if err := RejectSymlinkComponents(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := RejectSymlinkComponents(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Never try to change a shared sticky parent such as /tmp. Files created
	// there still use private modes and reject symlink targets.
	if info.Mode()&os.ModeSticky != 0 {
		return nil
	}
	return os.Chmod(path, 0o700)
}
