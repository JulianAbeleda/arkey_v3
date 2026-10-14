package models

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// DisplayName uses model metadata when available, then the checkpoint filename
// or directory. It does not infer a model family or parameter count from an
// architecture ID: many different checkpoints share the same architecture.
func DisplayName(path string) string {
	if path == "" {
		return ""
	}
	name := ""
	if strings.EqualFold(filepath.Ext(path), ".gguf") {
		if g, err := ReadGGUF(path); err == nil {
			name = g.Name
		}
	} else if f, err := os.Open(filepath.Join(path, "config.json")); err == nil {
		var metadata struct {
			Name string `json:"_name_or_path"`
		}
		if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&metadata) == nil {
			name = metadata.Name
		}
		f.Close()
	}
	if strings.TrimSpace(name) == "" {
		name = path
	}
	name = filepath.Base(strings.TrimRight(name, "/\\"))
	if strings.EqualFold(filepath.Ext(name), ".gguf") {
		name = name[:len(name)-5]
	}
	// Display text only; the routing slug and server model ID stay unchanged.
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		if r == '-' || r == '_' {
			return ' '
		}
		return r
	}, name)
	return strings.Join(strings.Fields(name), " ")
}

// EngineMetadata keeps the client catalog aligned with the selected checkpoint
// and the context actually requested from its engine.
func EngineMetadata(engine, path string, contextWindow int, effort string) map[string]any {
	meta := LocalMetadata()
	meta["slug"] = "arkey-local-" + engine
	if name := DisplayName(path); name != "" {
		meta["display_name"] = name
	} else {
		meta["display_name"] = "Arkey Local (" + engine + ")"
	}
	meta["context_window"] = contextWindow
	meta["max_context_window"] = contextWindow
	meta["base_instructions"] = "You are a coding assistant running locally. Work carefully with the provided tools."
	if effort != "" {
		meta["default_reasoning_level"] = effort
	}
	return meta
}
