package kimi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	"github.com/pelletier/go-toml/v2"
)

func WriteConfig(stateHome, bridgeURL, bridgeToken, model string, contextWindow int) error {
	if model == "" {
		return fmt.Errorf("selected model is required")
	}
	if contextWindow < 1 {
		contextWindow = 32768
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
	cfg := map[string]any{}
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Kimi config must be a regular file")
		}
		if info.Size() > 1<<20 {
			return fmt.Errorf("Kimi config exceeds 1 MiB safety limit")
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if len(existing) > 0 {
			if unmarshalErr := toml.Unmarshal(existing, &cfg); unmarshalErr != nil {
				return fmt.Errorf("read existing Kimi config: %w", unmarshalErr)
			}
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	cfg["default_model"] = ModelAlias
	cfg["telemetry"] = false
	providers, err := table(cfg, "providers")
	if err != nil {
		return err
	}
	providers[ModelAlias] = map[string]any{"type": "openai_responses", "base_url": strings.TrimRight(bridgeURL, "/") + "/v1", "api_key": bridgeToken}
	models, err := table(cfg, "models")
	if err != nil {
		return err
	}
	models[ModelAlias] = map[string]any{
		"provider": ModelAlias, "model": model, "max_context_size": contextWindow,
		"capabilities": []string{"thinking", "tool_use"}, "display_name": "Arkey via MoonBridge",
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(stateHome, ".config.*")
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

func table(config map[string]any, key string) (map[string]any, error) {
	if existing, ok := config[key]; ok {
		value, valid := existing.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("Kimi config %s must be a table", key)
		}
		return value, nil
	}
	value := map[string]any{}
	config[key] = value
	return value, nil
}
