package crush

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIsolatesConfigAndData(t *testing.T) {
	stateHome := t.TempDir()

	plan, err := Build(BuildOptions{
		Binary:      "/bin/true",
		StateHome:   stateHome,
		Environment: []string{"CRUSH_GLOBAL_CONFIG=/somewhere/else", "PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := envMap(plan.Env)
	if env["CRUSH_GLOBAL_CONFIG"] != filepath.Join(stateHome, "config") {
		t.Errorf("CRUSH_GLOBAL_CONFIG = %q, want the Arkey state home", env["CRUSH_GLOBAL_CONFIG"])
	}
	if env["CRUSH_GLOBAL_DATA"] != filepath.Join(stateHome, "data") {
		t.Errorf("CRUSH_GLOBAL_DATA = %q", env["CRUSH_GLOBAL_DATA"])
	}
	if env["CRUSH_DISABLE_PROVIDER_AUTO_UPDATE"] != "1" {
		t.Error("provider auto-update must be disabled in the isolated harness")
	}
	if env["PATH"] != "/usr/bin" {
		t.Errorf("inherited environment was dropped: %q", env["PATH"])
	}
}

func TestBuildRequiresSnapshotAndStateHome(t *testing.T) {
	if _, err := Build(BuildOptions{StateHome: t.TempDir()}); err == nil {
		t.Error("Build() error = nil, want error for missing snapshot")
	}
	if _, err := Build(BuildOptions{Binary: "/bin/true"}); err == nil {
		t.Error("Build() error = nil, want error for missing state home")
	}
}

func TestWriteConfigRoutesThroughMoonBridge(t *testing.T) {
	stateHome := t.TempDir()

	if err := WriteConfig(stateHome, "http://127.0.0.1:38440/", "", "deepseek-v4-pro", 65536, 16384); err != nil {
		t.Fatal(err)
	}

	path := ConfigPath(stateHome)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", info.Mode().Perm())
	}

	config := readConfig(t, path)
	provider := config["providers"].(map[string]any)[ProviderID].(map[string]any)
	if provider["type"] != "openai-compat" {
		t.Errorf("provider type = %v, want openai-compat", provider["type"])
	}
	if provider["base_url"] != "http://127.0.0.1:38440/v1" {
		t.Errorf("base_url = %v", provider["base_url"])
	}
	if provider["api_key"] != ModelAlias {
		t.Errorf("api_key = %v, want the default bridge token", provider["api_key"])
	}

	model := provider["models"].([]any)[0].(map[string]any)
	if model["id"] != "deepseek-v4-pro" {
		t.Errorf("model id = %v, want the selected route model", model["id"])
	}
	if model["context_window"].(float64) != 65536 || model["default_max_tokens"].(float64) != 16384 {
		t.Errorf("model limits = %v / %v", model["context_window"], model["default_max_tokens"])
	}

	selection := config["models"].(map[string]any)
	for _, size := range []string{"large", "small"} {
		entry := selection[size].(map[string]any)
		if entry["model"] != "deepseek-v4-pro" || entry["provider"] != ProviderID {
			t.Errorf("models.%s = %v", size, entry)
		}
	}
}

func TestWriteConfigPreservesExistingSettings(t *testing.T) {
	stateHome := t.TempDir()
	if err := os.MkdirAll(ConfigDir(stateHome), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `{"options":{"tui":{"compact_mode":true}},"lsp":{"go":{"command":"gopls"}},
		"providers":{"other":{"name":"Other"}}}`
	if err := os.WriteFile(ConfigPath(stateHome), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteConfig(stateHome, "http://127.0.0.1:38440", "secret", "arkey-local-llama", 8192, 4096); err != nil {
		t.Fatal(err)
	}

	config := readConfig(t, ConfigPath(stateHome))
	if _, ok := config["lsp"]; !ok {
		t.Error("existing lsp settings were dropped")
	}
	providers := config["providers"].(map[string]any)
	if _, ok := providers["other"]; !ok {
		t.Error("existing provider was dropped")
	}
	if _, ok := providers[ProviderID]; !ok {
		t.Error("MoonBridge provider was not written")
	}
	options := config["options"].(map[string]any)
	if _, ok := options["tui"]; !ok {
		t.Error("existing options were dropped")
	}
	if options["disable_metrics"] != true {
		t.Error("metrics were not disabled")
	}
	if providers[ProviderID].(map[string]any)["api_key"] != "secret" {
		t.Error("bridge token was not written")
	}
}

func TestWriteConfigRejectsMalformedExistingConfig(t *testing.T) {
	stateHome := t.TempDir()
	if err := os.MkdirAll(ConfigDir(stateHome), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(stateHome), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteConfig(stateHome, "http://127.0.0.1:38440", "", "m", 1024, 512)
	if err == nil || !strings.Contains(err.Error(), "read existing Crush config") {
		t.Fatalf("WriteConfig() error = %v, want a decode failure", err)
	}
}

func TestWriteConfigRequiresModel(t *testing.T) {
	if err := WriteConfig(t.TempDir(), "http://127.0.0.1:38440", "", "", 1024, 512); err == nil {
		t.Error("WriteConfig() error = nil, want error for empty model")
	}
}

func readConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("generated config is not valid JSON: %v\n%s", err, data)
	}
	return config
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, found := strings.Cut(entry, "=")
		if found {
			out[key] = value
		}
	}
	return out
}
