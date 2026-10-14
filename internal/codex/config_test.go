package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteConfigAddsTheProviderAndKeepsTheRest(t *testing.T) {
	// macOS's TMPDIR is a symlink (/var -> /private/var) and the writer
	// refuses symlinked components, as every Arkey writer does; resolve it.
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		t.Setenv("TMPDIR", resolved)
	}
	home := filepath.Join(t.TempDir(), "codex-home")
	if err := WriteConfig(home, "http://127.0.0.1:38440/", "", "/tmp/catalog.json"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(string(first), `model_provider = 'moonbridge'`) || !strings.Contains(string(first), `base_url = 'http://127.0.0.1:38440/v1'`) || !strings.Contains(string(first), `wire_api = 'responses'`) || !strings.Contains(string(first), `model_catalog_json = '/tmp/catalog.json'`) {
		t.Fatalf("config:\n%s", first)
	}
	extra := string(first) + "\n[projects.\"/x\"]\ntrust_level = 'trusted'\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(extra), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(home, "http://127.0.0.1:38440", "", "/tmp/catalog.json"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(string(second), "trust_level") {
		t.Fatalf("other tables were lost:\n%s", second)
	}
	if err := WriteConfig(home, "http://127.0.0.1:39000", "", "/tmp/catalog.json"); err != nil {
		t.Fatal(err)
	}
	third, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(string(third), "39000/v1") || !strings.Contains(string(third), "trust_level") {
		t.Fatalf("a moved bridge was not written or lost the rest:\n%s", third)
	}
}
