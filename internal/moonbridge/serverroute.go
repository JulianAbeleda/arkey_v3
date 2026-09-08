package moonbridge

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"
)

// The server route in the MoonBridge config: a llama.cpp server on the
// network, spoken to as OpenAI Chat, offered to the client as one model.
const (
	ServerRoute    = "arkey-server-llama"
	ServerModel    = "arkey-server"
	ServerProvider = "llama-server"
)

const configLimit = 1 << 20

// SetServerRoute writes the server route into the MoonBridge config at path:
// the provider with its base URL, the model with the server's context window,
// and the route joining them. Everything else in the file, the frontier
// providers and their keys included, is kept as it is. Reports whether the
// file changed, so the caller knows MoonBridge must be restarted to see it.
func SetServerRoute(path, origin, upstreamModel string, contextWindow int) (bool, error) {
	if origin == "" || upstreamModel == "" || contextWindow <= 0 {
		return false, errors.New("server route needs an origin, an upstream model and a context window")
	}
	doc := map[string]any{}
	if file, err := os.Open(path); err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, configLimit))
		_ = file.Close()
		if readErr != nil {
			return false, readErr
		}
		if len(data) > 0 {
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return false, fmt.Errorf("MoonBridge config is not YAML: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	maxOutput := contextWindow / 4
	if maxOutput < 4096 {
		maxOutput = 4096
	}
	if maxOutput > 32768 {
		maxOutput = 32768
	}
	provider := map[string]any{
		"protocol": "openai-chat",
		"base_url": origin,
		"api_key":  "local",
		"offers":   []any{map[string]any{"model": ServerModel, "upstream_name": upstreamModel}},
	}
	model := map[string]any{
		"display_name":      "Arkey Server",
		"context_window":    contextWindow,
		"max_output_tokens": maxOutput,
	}
	route := map[string]any{"model": ServerModel, "provider": ServerProvider}
	changed := false
	for _, entry := range []struct {
		section, key string
		value        map[string]any
	}{
		{"providers", ServerProvider, provider},
		{"models", ServerModel, model},
		{"routes", ServerRoute, route},
	} {
		table, _ := doc[entry.section].(map[string]any)
		if table == nil {
			table = map[string]any{}
		}
		if !reflect.DeepEqual(table[entry.key], entry.value) {
			table[entry.key] = entry.value
			changed = true
		}
		doc[entry.section] = table
	}
	if !changed {
		return false, nil
	}
	encoded, err := yaml.Marshal(doc)
	if err != nil {
		return false, err
	}
	if err := writePrivate(path, encoded); err != nil {
		return false, err
	}
	return true, nil
}

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
