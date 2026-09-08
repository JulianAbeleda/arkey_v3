package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain canonicalizes TMPDIR so t.TempDir() yields symlink-free paths. On
// macOS the system temp dir resolves through /tmp -> /private/tmp (and /var ->
// /private/var), which the path-security guard rejects; real config/state
// directories under the user's home are not symlinked, so this only affects
// tests. It is a no-op where TMPDIR is already canonical (Linux).
func TestMain(m *testing.M) {
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		os.Setenv("TMPDIR", resolved)
	}
	os.Exit(m.Run())
}

func TestStoreMigrateLegacy(t *testing.T) {
	d := t.TempDir()
	m := filepath.Join(d, "x.gguf")
	_ = os.WriteFile(m, []byte("x"), 0600)
	_ = os.WriteFile(filepath.Join(d, "mode"), []byte("local\n"), 0600)
	_ = os.WriteFile(filepath.Join(d, "local-model"), []byte(m), 0600)
	s := Store{Path: filepath.Join(d, "config.toml"), Home: d}
	c, ok, e := s.MigrateLegacy(d)
	if e != nil || !ok || c.Mode != "local" {
		t.Fatalf("%v %v", e, ok)
	}
	i, e := os.Stat(s.Path)
	if e != nil || i.Mode().Perm() != 0600 {
		t.Fatal(i, e)
	}
}

func TestTOMLStrictDecodeAndQuotedComment(t *testing.T) {
	home := t.TempDir()
	cfg := Default(home)
	cfg.Hardware.Name = "GPU #1"
	encoded, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded, home)
	if err != nil || decoded.Hardware.Name != "GPU #1" {
		t.Fatalf("decoded %#v: %v", decoded, err)
	}
	bad := append(encoded, []byte("\nunknown = true\n")...)
	if _, err := Decode(bad, home); err == nil || !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("expected strict unknown-field error, got %v", err)
	}
}

func TestConfigWithoutClientDefaultsToCodex(t *testing.T) {
	home := t.TempDir()
	encoded, err := Encode(Default(home))
	if err != nil {
		t.Fatal(err)
	}
	withoutClient := strings.Replace(string(encoded), "client = 'codex'\n", "", 1)
	withoutClient = strings.Replace(withoutClient, "client = \"codex\"\n", "", 1)
	decoded, err := Decode([]byte(withoutClient), home)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Client != "codex" {
		t.Fatalf("client = %q, want codex", decoded.Client)
	}
}

func TestLegacyMoonBridgeConfigPathIsPreserved(t *testing.T) {
	home := t.TempDir()
	legacyConfig := filepath.Join(home, "moon-bridge", "config.yml")
	if err := os.MkdirAll(filepath.Dir(legacyConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyConfig, []byte("models: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: filepath.Join(home, ".config", "arkey", "config.toml"), Home: home}
	cfg, migrated, err := store.MigrateLegacy(filepath.Join(home, ".config", "arkey"))
	if err != nil || !migrated {
		t.Fatalf("migrate: %v, migrated=%v", err, migrated)
	}
	if cfg.MoonBridge.Config != legacyConfig {
		t.Fatalf("MoonBridge config = %q, want %q", cfg.MoonBridge.Config, legacyConfig)
	}
}

func TestServerModeNeedsAValidOrigin(t *testing.T) {
	home := t.TempDir()
	c := Default(home)
	c.Mode = "server"
	if err := c.Validate(); err == nil {
		t.Fatal("server mode without an origin must be refused")
	}
	c.Server = Server{Label: "Ubuntu", Origin: "http://100.106.46.126:8080", Model: "arkey-local", ContextSize: 262144}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Servers = []ServerEntry{{Label: "Ubuntu", Origin: "http://100.106.46.126:8080/v1"}}
	if err := c.Validate(); err == nil {
		t.Fatal("an origin with a path must be refused")
	}
	c.Servers = []ServerEntry{{Label: "", Origin: "http://100.106.46.126:8080"}}
	if err := c.Validate(); err == nil {
		t.Fatal("an entry without a label must be refused")
	}
	c.Servers = []ServerEntry{{Label: "Ubuntu", Origin: "http://100.106.46.126:8080"}}
	encoded, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded, home)
	if err != nil || decoded.Mode != "server" || decoded.Server.Origin != c.Server.Origin || len(decoded.Servers) != 1 {
		t.Fatalf("round trip: %#v %v", decoded, err)
	}
	for origin, ok := range map[string]bool{
		"http://127.0.0.1:8080": true, "https://ubuntu.tailnet.ts.net": true,
		"http://x:1/": false, "ftp://x:1": false, "127.0.0.1:8080": false, "http://user@x:1": false, "http://x:1?q": false,
	} {
		if ValidOrigin(origin) != ok {
			t.Errorf("ValidOrigin(%q) = %v", origin, !ok)
		}
	}
}
