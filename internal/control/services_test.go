package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/gpu"
	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

func TestBridgeExistingRouteDoesNotRestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"arkey-local-llama"}]}`))
	}))
	defer server.Close()
	manager := BridgeManager{Client: moonbridge.Client{BaseURL: server.URL}}
	if err := manager.EnsureLocalRoute(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSelectFrontierPersistsBeforeReturningStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths := platform.DefaultPaths(home)
	store := config.Store{Path: paths.ConfigFile(), Home: home}
	cfg := config.Default(home)
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.6-sol"}]}`))
	}))
	defer server.Close()
	services := &Services{
		Paths: paths, Store: store, CodexBinary: "/bin/true", Workspace: home,
		BridgeClient: moonbridge.Client{BaseURL: server.URL}, config: cfg,
	}
	status, err := services.SelectFrontier(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if status.Route.Backend != "codex" || status.Route.Model != "gpt-5.6-sol" {
		t.Fatalf("unexpected status %#v", status.Route)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Frontier.Backend != "codex" {
		t.Fatalf("persisted config %#v, %v", loaded, err)
	}
}

func TestSelectClientPersistsArkeySnapshot(t *testing.T) {
	home := t.TempDir()
	paths := platform.DefaultPaths(home)
	store := config.Store{Path: paths.ConfigFile(), Home: home}
	cfg := config.Default(home)
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	services := &Services{
		Paths: paths, Store: store, CodexBinary: "/bin/true", ClaudeBinary: "/bin/true", KimiBinary: "/bin/true",
		BridgeClient: moonbridge.Client{BaseURL: server.URL}, config: cfg,
	}
	status, err := services.SelectClient(context.Background(), "kimi")
	if err != nil {
		t.Fatal(err)
	}
	if status.Client != "kimi" || status.Clients["kimi"] != "ready" {
		t.Fatalf("unexpected client status: %#v", status)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Client != "kimi" {
		t.Fatalf("persisted client = %q, err=%v", loaded.Client, err)
	}
}

type scanRunner struct{}

func (scanRunner) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	if name == "nvidia-smi" {
		return []byte("Test NVIDIA GPU\n"), nil
	}
	return nil, nil
}

type scanInspector struct{}

func (scanInspector) Backend(context.Context, string) (gpu.Backend, error) {
	return gpu.CUDABackend, nil
}

func TestScanGPUCommitsOnlyAfterMetadataUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths := platform.DefaultPaths(home)
	store := config.Store{Path: paths.ConfigFile(), Home: home}
	cfg := config.Default(home)
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	device := filepath.Join(home, "nvidiactl")
	server := filepath.Join(home, "search", "bin", "llama-server")
	catalog := filepath.Join(home, "models_catalog.json")
	if err := os.MkdirAll(filepath.Dir(server), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{device: 0o600, server: 0o700} {
		if err := os.WriteFile(path, []byte("test"), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(catalog, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	services := &Services{
		Paths: paths, Store: store, Detector: gpu.Detector{Runner: scanRunner{}, NVIDIAControl: device},
		GPUInspector: scanInspector{}, ModelCatalog: catalog, CandidateRoots: []string{filepath.Join(home, "search")}, config: cfg,
		CatalogLock: arkeyruntime.FileLock{Path: filepath.Join(home, "catalog.lock")},
	}
	if _, err := services.ScanGPU(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Hardware.Vendor != "nvidia" || loaded.Local.LlamaServer != server {
		t.Fatalf("unexpected aligned config %#v", loaded)
	}
}

func TestFrontierModelEnvironmentOverrides(t *testing.T) {
	t.Setenv("ARKEY_DEEPSEEK_MODEL", "deepseek-test")
	t.Setenv("ARKEY_CODEX_MODEL", "codex-test")
	t.Setenv("ARKEY_CLAUDE_MODEL", "claude-test")
	for backend, want := range map[string]string{"deepseek": "deepseek-test", "codex": "codex-test", "claude": "claude-test"} {
		if got := frontierModel(backend); got != want {
			t.Fatalf("%s model = %q, want %q", backend, got, want)
		}
	}
}

func TestNewHonorsLegacyEnvironmentOverrides(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config-override")
	stateDir := filepath.Join(home, "state-override")
	bridgeConfig := filepath.Join(home, "bridge.yml")
	if err := os.WriteFile(bridgeConfig, []byte("listen: 127.0.0.1:39001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARKEY_CONFIG_DIR", configDir)
	t.Setenv("ARKEY_LOCAL_STATE_DIR", stateDir)
	t.Setenv("MOONBRIDGE_CONFIG", bridgeConfig)
	t.Setenv("MOONBRIDGE_ADDR", "127.0.0.1:39001")
	t.Setenv("ARKEY_LLAMA_PORT", "18080")
	t.Setenv("ARKEY_LLAMA_CANDIDATES", filepath.Join(home, "llama-server"))
	t.Setenv("CODEX_MOONBRIDGE_BIN", "/bin/true")
	services, err := New(home, home)
	if err != nil {
		t.Fatal(err)
	}
	if services.Paths.ConfigDir != configDir || services.Paths.LocalStateDir() != stateDir {
		t.Fatalf("path overrides not honored: %#v", services.Paths)
	}
	if services.config.MoonBridge.Config != bridgeConfig || services.config.MoonBridge.Address != "127.0.0.1:39001" || services.config.Local.Port != 18080 {
		t.Fatalf("configuration overrides not honored: %#v", services.config)
	}
	if services.CodexBinary != "/bin/true" || len(services.CandidateServers) != 1 {
		t.Fatalf("runtime overrides not honored: binary=%q candidates=%#v", services.CodexBinary, services.CandidateServers)
	}
}
