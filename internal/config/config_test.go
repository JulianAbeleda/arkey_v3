package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
