package config

import (
	"os"
	"path/filepath"
	"strings"
)

// MigrateLegacy imports one-line Bash state only when TOML is absent.
func (s Store) MigrateLegacy(legacyDir string) (Config, bool, error) {
	if _, e := os.Lstat(s.Path); e == nil {
		c, e := s.Load()
		return c, false, e
	} else if !os.IsNotExist(e) {
		return Config{}, false, e
	}
	c := Default(s.Home)
	read := func(n string) string {
		b, e := os.ReadFile(filepath.Join(legacyDir, n))
		if e != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	if x := read("mode"); x == "frontier" || x == "local" {
		c.Mode = x
	}
	if x := read("backend"); x == "deepseek" || x == "codex" || x == "claude" {
		c.Frontier.Backend = x
	}
	if x := read("local-runtime"); x == "llama" {
		c.Local.Runtime = x
	}
	if x := read("local-model"); validGGUF(x) {
		c.Local.Model = x
	}
	if x := read("gpu-vendor"); x == "nvidia" || x == "amd" {
		c.Hardware.Vendor = x
	}
	c.Hardware.Name = read("gpu-name")
	if x := read("llama-server"); isExecutable(x) {
		c.Local.LlamaServer = x
	}
	legacyMoonBridge := filepath.Join(s.Home, "moon-bridge", "config.yml")
	if info, err := os.Stat(legacyMoonBridge); err == nil && info.Mode().IsRegular() {
		c.MoonBridge.Config = legacyMoonBridge
	}
	return c, true, s.Save(c)
}
func validGGUF(p string) bool {
	i, e := os.Stat(p)
	return e == nil && i.Mode().IsRegular() && strings.EqualFold(filepath.Ext(p), ".gguf")
}
func isExecutable(p string) bool {
	i, e := os.Stat(p)
	return e == nil && i.Mode().IsRegular() && i.Mode().Perm()&0111 != 0
}
