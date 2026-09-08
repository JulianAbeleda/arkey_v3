package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/gpu"
	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

// TestMain canonicalizes TMPDIR so t.TempDir() yields symlink-free paths. On
// macOS the system temp dir resolves through /tmp -> /private/tmp (and /var ->
// /private/var), which the path-security guard rejects; real config/state
// directories under the user's home are not symlinked, so this only affects
// tests. It is a no-op where TMPDIR is already canonical (Linux).
func TestMain(m *testing.M) {
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		os.Setenv("TMPDIR", resolved)
	}
	os.Exit(m.Run())
}

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

func TestClientMaxOutputTokensClamps(t *testing.T) {
	cases := []struct {
		contextSize int
		want        int
	}{
		{contextSize: 1000, want: 4096},
		{contextSize: 32768, want: 8192},
		{contextSize: 1000000, want: 32768},
	}
	for _, tc := range cases {
		cfg := config.Default("/home")
		cfg.Mode = "local"
		cfg.Local.ContextSize = tc.contextSize
		services := &Services{config: cfg}
		if got := services.ClientMaxOutputTokens(); got != tc.want {
			t.Fatalf("ClientMaxOutputTokens() with ContextSize=%d = %d, want %d", tc.contextSize, got, tc.want)
		}
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
	// A snapshotted client only needs to be a regular executable file (checked
	// by os.Stat), so a hermetic 0755 file works on any OS; the hardcoded
	// /bin/true does not exist on macOS.
	bin := filepath.Join(home, "client")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	services := &Services{
		Paths: paths, Store: store, CodexBinary: bin, ClaudeBinary: bin, KimiBinary: bin, CrushBinary: bin,
		BridgeClient: moonbridge.Client{BaseURL: server.URL}, config: cfg,
	}
	for _, client := range []string{"kimi", "crush"} {
		status, err := services.SelectClient(context.Background(), client)
		if err != nil {
			t.Fatalf("SelectClient(%q): %v", client, err)
		}
		if status.Client != client || status.Clients[client] != "ready" {
			t.Fatalf("unexpected client status for %q: %#v", client, status)
		}
		loaded, err := store.Load()
		if err != nil || loaded.Client != client {
			t.Fatalf("persisted client = %q, err=%v", loaded.Client, err)
		}
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

func TestSelectServerProbesWritesTheRouteAndPersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths := platform.DefaultPaths(home)
	store := config.Store{Path: paths.ConfigFile(), Home: home}
	cfg := config.Default(home)
	cfg.MoonBridge.Config = filepath.Join(home, "moonbridge.yml")
	cfg.Servers = []config.ServerEntry{{Label: "Ubuntu", Origin: ""}}
	llama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-served"}]}`))
		case "/props":
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":65536}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer llama.Close()
	cfg.Servers[0].Origin = llama.URL
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(home, "models_catalog.json")
	if err := os.WriteFile(catalog, []byte(`{"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"arkey-server-llama"}]}`))
	}))
	defer bridge.Close()
	services := &Services{
		Paths: paths, Store: store, CodexBinary: "/bin/true", Workspace: home,
		BridgeClient: moonbridge.Client{BaseURL: bridge.URL}, ModelCatalog: catalog,
		ServerHTTP: llama.Client(), config: cfg,
	}
	status, err := services.SelectServer(context.Background(), llama.URL)
	if err != nil {
		t.Fatal(err)
	}
	if status.Route.Mode != "server" || status.Route.Model != "arkey-server-llama" || status.Route.ServerModel != "qwen-served" || status.Route.ServerLabel != "Ubuntu" {
		t.Fatalf("route = %#v", status.Route)
	}
	if status.Runtime != "server reachable" || len(status.Servers) != 1 || !status.Servers[0].Selected {
		t.Fatalf("status = %#v", status)
	}
	if got := services.ClientContextWindow(); got != 65536 {
		t.Fatalf("client context window = %d", got)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Mode != "server" || loaded.Server.ContextSize != 65536 {
		t.Fatalf("persisted %#v, %v", loaded, err)
	}
	yml, _ := os.ReadFile(cfg.MoonBridge.Config)
	if !strings.Contains(string(yml), llama.URL) || !strings.Contains(string(yml), "upstream_name: qwen-served") {
		t.Fatalf("MoonBridge config:\n%s", yml)
	}
	registered, _ := os.ReadFile(catalog)
	if !strings.Contains(string(registered), `"slug": "arkey-server-llama"`) || !strings.Contains(string(registered), `"context_window": 65536`) {
		t.Fatalf("catalog:\n%s", registered)
	}
	if !services.serverChanged {
		t.Fatal("a rewritten MoonBridge config must ask for a restart at launch")
	}
	if _, err := services.SelectServer(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Fatal("an unreachable server must be refused")
	}
}
