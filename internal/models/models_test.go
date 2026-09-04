package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverAndMetadata(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "b.gguf"), []byte("x"), 0600)
	r, e := Discover(context.Background(), []string{d})
	if e != nil || len(r.Models) != 1 {
		t.Fatal(e, r)
	}
	p := filepath.Join(d, "catalog.json")
	_ = os.WriteFile(p, []byte(`{"other":1,"models":[{"slug":"arkey-local-llama"}]}`), 0640)
	if e = UpdateCatalog(p); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"other": 1`) || strings.Count(string(b), LocalSlug) != 1 {
		t.Fatal(string(b))
	}
}

func TestDiscoverResolvesSymlinkedRoot(t *testing.T) {
	parent := t.TempDir()
	modelsDir := filepath.Join(parent, "storage", "models")
	if err := os.MkdirAll(modelsDir, 0700); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(modelsDir, "linked.gguf")
	if err := os.WriteFile(modelPath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "models")
	if err := os.Symlink(modelsDir, link); err != nil {
		t.Fatal(err)
	}

	discovery, err := Discover(context.Background(), []string{link})
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Models) != 1 {
		t.Fatalf("got %d models, want 1: %#v", len(discovery.Models), discovery.Models)
	}
	if discovery.Models[0].Path != modelPath {
		t.Fatalf("model path = %q, want %q", discovery.Models[0].Path, modelPath)
	}
}
