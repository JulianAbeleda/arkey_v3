package models

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisplayNameUsesMetadataAndFallsBackWithoutFamilyRules(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"_name_or_path":"ExampleOrg/Granite-8B-Instruct"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := DisplayName(dir); got != "Granite 8B Instruct" {
		t.Fatal(got)
	}
	b := newGGUFBuilder(3, 0)
	b.addString("general.name", "Example Model 12B")
	if got := DisplayName(writeTempGGUF(t, b.bytes())); got != "Example Model 12B" {
		t.Fatal(got)
	}
	for path, want := range map[string]string{
		"/missing/Qwen3.8-27B-NVFP4-NVIDIA": "Qwen3.8 27B NVFP4 NVIDIA",
		"/missing/Llama-3.1-8B-Q4_K_M.gguf": "Llama 3.1 8B Q4 K M",
		"":                                  "",
	} {
		if got := DisplayName(path); got != want {
			t.Fatalf("%s: %q != %q", path, got, want)
		}
	}
}

func TestEngineMetadataKeepsIdentitySeparateFromRoute(t *testing.T) {
	for _, engine := range []string{"llama", "vllm"} {
		meta := EngineMetadata(engine, "/models/Example-12B", 65536, "medium")
		if meta["slug"] != "arkey-local-"+engine || meta["display_name"] != "Example 12B" || meta["context_window"] != 65536 || meta["max_context_window"] != 65536 {
			t.Fatalf("%+v", meta)
		}
	}
}
