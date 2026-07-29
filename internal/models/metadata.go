package models

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

const LocalSlug = "arkey-local-llama"

func LocalMetadata() map[string]any {
	return map[string]any{"slug": LocalSlug, "display_name": "Arkey Local (llama.cpp)", "default_reasoning_level": "medium", "supported_reasoning_levels": []any{map[string]any{"effort": "low", "description": "Low reasoning effort"}, map[string]any{"effort": "medium", "description": "Medium reasoning effort"}, map[string]any{"effort": "high", "description": "High reasoning effort"}}, "shell_type": "unified_exec", "visibility": "list", "supported_in_api": true, "priority": 0, "additional_speed_tiers": []any{}, "availability_nux": nil, "upgrade": nil, "base_instructions": "You are a coding assistant running locally through llama.cpp. Work carefully with the provided tools.", "supports_reasoning_summaries": true, "default_reasoning_summary": "auto", "support_verbosity": false, "default_verbosity": nil, "apply_patch_tool_type": "freeform", "web_search_tool_type": "text", "truncation_policy": map[string]any{"mode": "tokens", "limit": 8000}, "supports_parallel_tool_calls": false, "supports_image_detail_original": false, "context_window": 32768, "max_context_window": 32768, "effective_context_window_percent": 90, "experimental_supported_tools": []any{}, "input_modalities": []string{"text"}, "supports_search_tool": false}
}
func UpdateCatalog(path string) error {
	if e := platform.RejectSymlinkComponents(path); e != nil {
		return e
	}
	fi, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("catalog must be a regular file")
	}
	if fi.Size() > 16<<20 {
		return errors.New("catalog exceeds 16 MiB safety limit")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var c map[string]json.RawMessage
	if e = json.Unmarshal(b, &c); e != nil {
		return e
	}
	var entries []json.RawMessage
	if raw := c["models"]; raw != nil {
		if e = json.Unmarshal(raw, &entries); e != nil {
			return e
		}
	}
	local, _ := json.Marshal(LocalMetadata())
	kept := entries[:0]
	for _, x := range entries {
		var id struct {
			Slug string `json:"slug"`
		}
		if json.Unmarshal(x, &id) == nil && id.Slug == LocalSlug {
			continue
		}
		kept = append(kept, x)
	}
	c["models"], _ = json.Marshal(append(kept, local))
	encoded, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	return atomic(path, append(encoded, '\n'), fi.Mode().Perm())
}
func atomic(path string, b []byte, mode os.FileMode) error {
	d := filepath.Dir(path)
	if e := platform.RejectSymlinkComponents(path); e != nil {
		return e
	}
	f, e := os.CreateTemp(d, ".arkey-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if err := os.Rename(n, path); err != nil {
		return err
	}
	if dir, err := os.Open(d); err == nil {
		defer dir.Close()
		_ = dir.Sync()
	}
	return nil
}
