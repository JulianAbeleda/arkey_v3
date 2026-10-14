package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/app"
	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/gpu"
	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

type fakeLocalRuntime struct {
	cfg            arkeyruntime.Config
	starts, stops  int
	err            error
	resolvedWindow int
	loaded         bool
}

func (f *fakeLocalRuntime) Start(_ context.Context, c arkeyruntime.Config) (arkeyruntime.State, error, error) {
	f.starts++
	if f.err != nil {
		return arkeyruntime.State{}, nil, f.err
	}
	f.cfg = c
	f.loaded = true
	window := c.ContextSize
	if f.resolvedWindow > 0 {
		window = f.resolvedWindow
	}
	return arkeyruntime.State{Model: c.Model, ContextSize: window}, nil, nil
}
func (f *fakeLocalRuntime) Loaded(context.Context, arkeyruntime.Config) (string, bool, error) {
	if !f.loaded {
		return "", false, nil
	}
	return f.cfg.Model, true, nil
}
func (f *fakeLocalRuntime) Stop(context.Context) error { f.stops++; f.loaded = false; return nil }

func TestVLLMUsesSharedModelLifecycle(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Local.VLLMContextSize = 32768
	cfg.Hardware.Vendor = "nvidia"
	checkpoint := filepath.Join(home, "checkpoint")
	os.Mkdir(checkpoint, 0700)
	os.WriteFile(filepath.Join(checkpoint, "config.json"), []byte(`{"model_type":"qwen3_5"}`), 0600)
	os.WriteFile(filepath.Join(checkpoint, "model.safetensors"), []byte("fixture"), 0600)
	cfg.Local.VLLMModelDirs = []string{home}
	paths := platform.DefaultPaths(home)
	store := config.Store{Path: paths.ConfigFile(), Home: home}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[{"id":"arkey-local-vllm"}]}`) }))
	defer server.Close()
	runtime := &fakeLocalRuntime{}
	s := &Services{Paths: paths, Store: store, config: cfg, Runtime: runtime, Detector: gpu.Detector{Runner: scanRunner{}}, BridgeClient: moonbridge.Client{BaseURL: server.URL}}
	discovered, err := s.DiscoverModels(context.Background(), "vllm")
	if err != nil || len(discovered) != 1 {
		t.Fatalf("discovery: %#v %v", discovered, err)
	}
	status, err := s.ActivateLocal(context.Background(), "vllm", discovered[0])
	if err != nil {
		t.Fatal(err)
	}
	if !status.LocalActive || !status.LocalLoaded || status.LoadedModel != checkpoint || status.Route.Model != "arkey-local-vllm" {
		t.Fatalf("bad status %#v", status)
	}
	saved, err := store.Load()
	if err != nil || saved.Local.VLLMCheckpoint != checkpoint || saved.Local.Runtime != "vllm" {
		t.Fatalf("selection not persisted: %v", err)
	}
	if runtime.cfg.Engine != "vllm" || runtime.cfg.ContextSize != 32768 || runtime.cfg.ServedModel != "arkey-local-vllm" {
		t.Fatalf("bad runtime config %#v", runtime.cfg)
	}
	if err = s.PrepareLaunch(context.Background(), s.SelectedModel()); err != nil || runtime.starts != 2 {
		t.Fatalf("launch did not use shared runtime: %v", err)
	}
	if _, err = s.UnloadLocal(context.Background()); err != nil || runtime.stops != 1 {
		t.Fatalf("unload: %v", err)
	}
	runtime.err = errors.New("start failed")
	if _, err = s.ActivateLocal(context.Background(), "llama", app.ModelSummary{Path: checkpoint}); err == nil {
		t.Fatal("expected failure")
	}
	if s.snapshot().Local.Runtime != "vllm" {
		t.Fatal("failed load changed selected engine")
	}
}

func TestExplicitRouteOverrideUsesItsOwnClientBudget(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Local.VLLMContextSize = 32768
	cfg.Mode = "local"
	cfg.Local.Runtime = "llama"
	cfg.Local.ContextSize = 262144
	s := &Services{config: cfg}
	if got := s.ClientContextWindowFor("arkey-local-vllm"); got != 32768 {
		t.Fatalf("vLLM inherited llama context: %d", got)
	}
	if got := s.ClientMaxOutputTokensFor("arkey-local-vllm"); got != 8192 {
		t.Fatalf("bad output budget: %d", got)
	}
	if got := s.ClientReasoningEffortFor("arkey-local-vllm"); got != "medium" {
		t.Fatalf("bad reasoning: %s", got)
	}
	cfg.Local.Runtime = "vllm"
	s.config = cfg
	if got := s.ClientContextWindowFor("arkey-local-llama"); got != 262144 {
		t.Fatalf("llama inherited vLLM context: %d", got)
	}
}

func TestClientCatalogFollowsCheckpointAndContextChanges(t *testing.T) {
	home := t.TempDir()
	cfg := config.Default(home)
	cfg.Local.VLLMContextSize = 32768
	cfg.Local.ContextSize = 16384
	cfg.Local.VLLMCheckpoint = filepath.Join(home, "Example-12B")
	cfg.Local.VLLMContextSize = 65536
	s := &Services{config: cfg, ModelCatalog: filepath.Join(home, "catalog.json"), CatalogLock: arkeyruntime.FileLock{Path: filepath.Join(home, "catalog.lock")}}
	check := func(wantName string, wantContext int) {
		t.Helper()
		if err := s.RegisterClientModel(context.Background(), "arkey-local-vllm"); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(s.ModelCatalog)
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Models []struct {
				Slug   string `json:"slug"`
				Name   string `json:"display_name"`
				Window int    `json:"context_window"`
			}
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range catalog.Models {
			if m.Slug == "arkey-local-vllm" {
				found = true
				if m.Name != wantName || m.Window != wantContext {
					t.Fatalf("stale metadata: %+v", m)
				}
			}
		}
		if !found || len(catalog.Models) != 2 {
			t.Fatalf("bad catalog: %+v", catalog)
		}
	}
	check("Example 12B", 65536)
	s.config.Local.VLLMCheckpoint = filepath.Join(home, "Different-8B")
	s.config.Local.VLLMContextSize = 32768
	check("Different 8B", 32768)
}

func TestVLLMAutoWindowFlowsIntoClientAfterStartup(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Local.Runtime = "vllm"
	cfg.Local.VLLMCheckpoint = "/models/any-checkpoint"
	cfg.Local.VLLMContextSize = 0
	s := &Services{config: cfg, Runtime: &fakeLocalRuntime{resolvedWindow: 98304}}
	if err := s.PrepareLaunch(context.Background(), "arkey-local-vllm"); err != nil {
		t.Fatal(err)
	}
	if n := s.ClientContextWindowFor("arkey-local-vllm"); n != 98304 {
		t.Fatal(n)
	}
	if n := s.ClientMaxOutputTokensFor("arkey-local-vllm"); n != 24576 {
		t.Fatal(n)
	}
	if s.snapshot().Local.VLLMContextSize != 0 {
		t.Fatal("auto setting became a fixed pin")
	}
}
