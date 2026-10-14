package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHFDiscoveryUsesMetadataAndResolvesRelocatedRoot(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "actual")
	os.Mkdir(models, 0700)
	for _, name := range []string{"random-awq-name", "random-fp4-name", "incomplete"} {
		p := filepath.Join(models, name)
		os.Mkdir(p, 0700)
		os.WriteFile(filepath.Join(p, "config.json"), []byte(`{"model_type":"qwen3_5"}`), 0600)
		if name != "incomplete" {
			os.WriteFile(filepath.Join(p, "weights.safetensors"), []byte("fixture"), 0600)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(models, link); err != nil {
		t.Fatal(err)
	}
	found, err := DiscoverFor(context.Background(), []string{link, models}, "vllm")
	if err != nil || len(found.Models) != 2 {
		t.Fatalf("got %+v err %v", found, err)
	}
	if found.Models[0].Path != filepath.Join(models, "random-awq-name") {
		t.Fatal("did not canonicalize root")
	}
}
