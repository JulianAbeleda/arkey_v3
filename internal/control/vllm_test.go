package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/app"
	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

func TestVLLMSelectionAndLaunch(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		upstreamCalls++
		fmt.Fprint(w, `{"data":[{"id":"arkey-local-vllm","max_model_len":32768}]}`)
	}))
	defer upstream.Close()
	cfg.Local.VLLMURL = upstream.URL
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[{"id":"arkey-local-vllm"}]}`) }))
	defer bridge.Close()
	paths := platform.DefaultPaths(home)
	store := config.Store{Path: paths.ConfigFile(), Home: home}
	bc := moonbridge.Client{BaseURL: bridge.URL}
	s := &Services{Paths: paths, Store: store, config: cfg, BridgeClient: bc, Bridge: &BridgeManager{Client: bc}}
	// No llama controller is supplied: vLLM must never touch llama lifecycle.
	status, err := s.ActivateLocal(context.Background(), "vllm", app.ModelSummary{})
	if err != nil {
		t.Fatal(err)
	}
	if !status.LocalLoaded || status.LocalActive || status.Route.Model != "arkey-local-vllm" {
		t.Fatalf("unexpected status %#v", status)
	}
	saved, err := store.Load()
	if err != nil || saved.Local.Runtime != "vllm" {
		t.Fatalf("selection not saved: %v", err)
	}
	if s.ClientContextWindow() != 32768 {
		t.Fatal("wrong vLLM context")
	}
	if err := s.PrepareLaunch(context.Background(), s.SelectedModel()); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls < 3 {
		t.Fatal("launch did not recheck upstream")
	}
	if _, err := s.UnloadLocal(context.Background()); err == nil {
		t.Fatal("externally managed vLLM must not be stopped")
	}
}

func TestVLLMRejectsWrongModelAndContextWithoutSaving(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"other","max_model_len":32768}]}`, `{"data":[{"id":"arkey-local-vllm","max_model_len":8192}]}`, `{"data":[{"id":"arkey-local-vllm"}]}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer upstream.Close()
			cfg := config.Default(t.TempDir())
			cfg.Local.VLLMURL = upstream.URL
			s := &Services{config: cfg}
			if _, err := s.ActivateLocal(context.Background(), "vllm", app.ModelSummary{}); err == nil {
				t.Fatal("expected rejection")
			}
			if s.snapshot().Local.Runtime != "llama" {
				t.Fatal("failed activation changed selection")
			}
		})
	}
}
