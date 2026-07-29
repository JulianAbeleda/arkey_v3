package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPathsHonorsExplicitCompatibilityDirectories(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "custom-config")
	stateDir := filepath.Join(home, "custom-state")
	t.Setenv("ARKEY_CONFIG_DIR", configDir)
	t.Setenv("ARKEY_LOCAL_STATE_DIR", stateDir)
	paths := DefaultPaths(home)
	if paths.ConfigDir != configDir || paths.LocalStateDir() != stateDir {
		t.Fatalf("unexpected paths %#v", paths)
	}
}

func TestRejectSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	if err := RejectSymlinkComponents(filepath.Join(linked, "state.json")); err == nil {
		t.Fatal("expected symlinked parent to be rejected")
	}
}
