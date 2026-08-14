package crush

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

// WriteConfig generates the Crush configuration that routes every request
// through MoonBridge.
//
// Any settings the operator already put in the isolated config (theme, LSPs,
// MCP servers, permissions) are preserved; only the MoonBridge provider and the
// large/small model selections are rewritten.
func WriteConfig(stateHome, bridgeURL, bridgeToken, model string, contextWindow, maxOutputTokens int) error {
	if model == "" {
		return fmt.Errorf("selected model is required")
	}
	if contextWindow < 1 {
		contextWindow = 32768
	}
	if maxOutputTokens < 1 {
		maxOutputTokens = 8192
	}
	if bridgeToken == "" {
		bridgeToken = ModelAlias
	}

	path := ConfigPath(stateHome)
	if err := platform.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := platform.RejectSymlinkComponents(path); err != nil {
		return err
	}

	config := map[string]any{}
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Crush config must be a regular file")
		}
		if info.Size() > 1<<20 {
			return fmt.Errorf("Crush config exceeds 1 MiB safety limit")
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if len(existing) > 0 {
			if unmarshalErr := json.Unmarshal(existing, &config); unmarshalErr != nil {
				return fmt.Errorf("read existing Crush config: %w", unmarshalErr)
			}
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	config["$schema"] = "https://charm.land/crush.json"
	providers, err := object(config, "providers")
	if err != nil {
		return err
	}
	// type "openai-compat" is Crush's generic Chat Completions client: it POSTs
	// to <base_url>/chat/completions, which is the MoonBridge ingress.
	providers[ProviderID] = map[string]any{
		"name":     "Arkey via MoonBridge",
		"type":     "openai-compat",
		"base_url": strings.TrimRight(bridgeURL, "/") + "/v1",
		"api_key":  bridgeToken,
		// The model id is the MoonBridge route alias: it is the name Crush puts
		// in the request body, and the name MoonBridge routes on.
		"models": []any{map[string]any{
			"id":                     model,
			"name":                   "Arkey via MoonBridge",
			"context_window":         contextWindow,
			"default_max_tokens":     maxOutputTokens,
			"can_reason":             true,
			"supports_attachments":   true,
			"cost_per_1m_in":         0,
			"cost_per_1m_out":        0,
			"cost_per_1m_in_cached":  0,
			"cost_per_1m_out_cached": 0,
		}},
	}

	models, err := object(config, "models")
	if err != nil {
		return err
	}
	selection := map[string]any{"model": model, "provider": ProviderID}
	models["large"] = selection
	models["small"] = selection

	options, err := object(config, "options")
	if err != nil {
		return err
	}
	options["disable_metrics"] = true
	options["disable_provider_auto_update"] = true

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writePrivate(path, data)
}

// writePrivate replaces path atomically with mode 0600 content.
func writePrivate(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".crush.*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

// object returns config[key] as a JSON object, creating it when absent.
func object(config map[string]any, key string) (map[string]any, error) {
	if existing, ok := config[key]; ok {
		value, valid := existing.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("Crush config %s must be an object", key)
		}
		return value, nil
	}
	value := map[string]any{}
	config[key] = value
	return value, nil
}
