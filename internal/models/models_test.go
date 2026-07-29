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
