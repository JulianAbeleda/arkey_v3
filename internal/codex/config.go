package codex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/JulianAbeleda/arkey_v3/internal/harness"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	"github.com/pelletier/go-toml/v2"
)

// ProviderName is the model_provider every Arkey Codex launch selects.
const ProviderName = "moonbridge"

// WriteConfig makes sure the isolated Codex home names the MoonBridge
// provider: the OpenAI Responses ingress at <bridge>/v1. That table and the default provider
// selection are repaired when needed; other settings in the Arkey-owned
// config.toml are kept. A machine that never ran an
// earlier Arkey has no such file, and Codex without the provider table
// refuses `model_provider="moonbridge"` before a request is made.
// `model_catalog_json` names the catalog Arkey keeps the local and server
// model metadata in; without it this Codex reads only its own cache and
// warns that the model is unknown.
func WriteConfig(stateHome, bridgeURL, bridgeToken, modelCatalog string) error {
	if bridgeURL == "" {
		return errors.New("MoonBridge URL is required")
	}
	if bridgeToken == "" {
		bridgeToken = "arkey-moonbridge"
	}
	if err := platform.EnsurePrivateDir(stateHome); err != nil {
		return err
	}
	path := filepath.Join(stateHome, "config.toml")
	if err := platform.RejectSymlinkComponents(path); err != nil {
		return err
	}
	cfg, err := read(path)
	if err != nil {
		return err
	}
	providers, _ := cfg["model_providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	want := map[string]any{
		"name":     "MoonBridge",
		"base_url": strings.TrimRight(bridgeURL, "/") + "/v1",
		"wire_api": "responses",
		"env_key":  "ARKEY_MOONBRIDGE_TOKEN",
	}
	catalogSet := modelCatalog == "" || cfg["model_catalog_json"] == modelCatalog
	if current, _ := providers[ProviderName].(map[string]any); current != nil && current["base_url"] == want["base_url"] && current["wire_api"] == want["wire_api"] && catalogSet && cfg["model_provider"] == ProviderName {
		return nil
	}
	providers[ProviderName] = want
	cfg["model_providers"] = providers
	cfg["model_provider"] = ProviderName
	if modelCatalog != "" {
		cfg["model_catalog_json"] = modelCatalog
	}
	_ = bridgeToken
	return write(path, cfg)
}

// SetMCPServer registers server as [mcp_servers.<name>] in the isolated
// Codex home, or removes that table when server is nil. Other MCP servers
// and settings are kept.
func SetMCPServer(stateHome, name string, server *harness.Server) error {
	if err := platform.EnsurePrivateDir(stateHome); err != nil {
		return err
	}
	path := filepath.Join(stateHome, "config.toml")
	if err := platform.RejectSymlinkComponents(path); err != nil {
		return err
	}
	cfg, err := read(path)
	if err != nil {
		return err
	}
	servers, _ := cfg["mcp_servers"].(map[string]any)
	if server == nil {
		if _, ok := servers[name]; !ok {
			return nil
		}
		delete(servers, name)
		return write(path, cfg)
	}
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = map[string]any{
		"command":             server.Command,
		"args":                server.Args,
		"startup_timeout_sec": int(harness.StartupTimeout.Seconds()),
		"tool_timeout_sec":    int(harness.ToolTimeout.Seconds()),
	}
	cfg["mcp_servers"] = servers
	return write(path, cfg)
}

// read loads the Arkey-owned config.toml; a missing file is an empty one.
func read(path string) (map[string]any, error) {
	cfg := map[string]any{}
	info, statErr := os.Lstat(path)
	if os.IsNotExist(statErr) {
		return cfg, nil
	}
	if statErr != nil {
		return nil, statErr
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Codex config must be a regular file")
	}
	if info.Size() > 1<<20 {
		return nil, errors.New("Codex config exceeds 1 MiB safety limit")
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		if err := toml.Unmarshal(existing, &cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func write(path string, cfg map[string]any) error {
	encoded, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
