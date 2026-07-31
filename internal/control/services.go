package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/app"
	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/gpu"
	"github.com/JulianAbeleda/arkey_v3/internal/models"
	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

const moonbridgeLocalRoute = "arkey-local-llama"

type Services struct {
	Paths            platform.Paths
	Store            config.Store
	Runner           platform.Runner
	Detector         gpu.Detector
	GPUInspector     gpu.Inspector
	BridgeClient     moonbridge.Client
	Bridge           *BridgeManager
	Runtime          *arkeyruntime.Controller
	MoonBridgeBinary string
	CodexBinary      string
	ClaudeBinary     string
	KimiBinary       string
	ModelCatalog     string
	CandidateRoots   []string
	CandidateServers []string
	CatalogLock      arkeyruntime.Lock
	Workspace        string
	mu               sync.RWMutex
	config           config.Config
}

func New(home, workspace string) (*Services, error) {
	paths := platform.DefaultPaths(home)
	if value := os.Getenv("ARKEY_MODEL_DIR"); value != "" {
		paths.ModelDir = value
	}
	store := config.Store{Path: paths.ConfigFile(), Home: paths.Home}
	cfg, created, err := store.MigrateLegacy(paths.ConfigDir)
	if err != nil {
		return nil, err
	}
	moonBridgeConfigOverride := os.Getenv("MOONBRIDGE_CONFIG")
	if created && moonBridgeConfigOverride == "" {
		cfg.MoonBridge.Config = paths.MoonBridgeConfig()
		if err := store.Save(cfg); err != nil {
			return nil, fmt.Errorf("initialize MoonBridge config path: %w", err)
		}
	}
	if value := moonBridgeConfigOverride; value != "" {
		cfg.MoonBridge.Config = value
	}
	if value := os.Getenv("MOONBRIDGE_ADDR"); value != "" {
		cfg.MoonBridge.Address = value
	}
	if value := os.Getenv("ARKEY_LLAMA_PORT"); value != "" {
		port, parseErr := strconv.Atoi(value)
		if parseErr != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid ARKEY_LLAMA_PORT %q", value)
		}
		cfg.Local.Port = port
	}
	legacyMoonBridgeConfig := filepath.Join(paths.Home, "moon-bridge", "config.yml")
	if moonBridgeConfigOverride == "" && !regularFile(cfg.MoonBridge.Config) && regularFile(legacyMoonBridgeConfig) {
		cfg.MoonBridge.Config = legacyMoonBridgeConfig
		if err := store.Save(cfg); err != nil {
			return nil, fmt.Errorf("migrate MoonBridge config path: %w", err)
		}
	}
	runner := platform.ExecRunner{}
	client := moonbridge.Client{BaseURL: "http://" + cfg.MoonBridge.Address, HTTP: &http.Client{Timeout: time.Second}}
	moonbridgeBinary := envOr("ARKEY_MOONBRIDGE_SERVER", filepath.Join(paths.Home, ".local", "libexec", "arkey", "moonbridge"))
	clientRoot := envOr("ARKEY_CLIENT_ROOT", filepath.Join(paths.Home, ".local", "libexec", "arkey", "clients"))
	codexBinary := envOr("ARKEY_CODEX_BIN", envOr("CODEX_MOONBRIDGE_BIN", envOr("CODEX_BIN", filepath.Join(clientRoot, "codex", "codex"))))
	claudeBinary := envOr("ARKEY_CLAUDE_BIN", filepath.Join(clientRoot, "claude", "claude"))
	kimiBinary := envOr("ARKEY_KIMI_BIN", filepath.Join(clientRoot, "kimi", "kimi"))
	modelCatalog := envOr("ARKEY_MODEL_CATALOG", filepath.Join(paths.Home, ".codex-moonbridge", "models_catalog.json"))
	inspector := arkeyruntime.LinuxInspector{}
	launcher := arkeyruntime.DirectLauncher{}
	systemd := arkeyruntime.SystemdService{Runner: runner}
	bridge := &BridgeManager{
		Client: client, Binary: moonbridgeBinary, Config: cfg.MoonBridge.Config,
		LogPath:   filepath.Join(paths.LogsDir(), "moonbridge.log"),
		Store:     arkeyruntime.FileStore{Path: filepath.Join(paths.LocalStateDir(), "moonbridge.json")},
		Inspector: inspector, Launcher: launcher, Service: systemd,
	}
	runtimeController := &arkeyruntime.Controller{
		Store:     arkeyruntime.FileStore{Path: filepath.Join(paths.LocalStateDir(), "runtime.json")},
		Inspector: inspector, Launcher: launcher, Service: systemd,
		Health:     arkeyruntime.HTTPHealth{Client: &http.Client{Timeout: time.Second}},
		MoonBridge: bridge, Backend: arkeyruntime.CommandBackend{Runner: runner},
		Lock: arkeyruntime.FileLock{Path: filepath.Join(paths.LocalStateDir(), "operation.lock")},
	}
	if err := migrateLegacyRuntime(context.Background(), paths, cfg, runner, inspector, runtimeController.Store); err != nil {
		return nil, fmt.Errorf("migrate legacy local runtime: %w", err)
	}
	roots := []string{filepath.Join(paths.Home, "env"), filepath.Join(paths.Home, "worktrees"), filepath.Join(paths.Home, "llama.cpp")}
	if value := os.Getenv("ARKEY_LLAMA_SEARCH_ROOTS"); value != "" {
		roots = filepath.SplitList(value)
	}
	var candidates []string
	if value := os.Getenv("ARKEY_LLAMA_CANDIDATES"); value != "" {
		candidates = filepath.SplitList(value)
	}
	return &Services{
		Paths: paths, Store: store, Runner: runner,
		Detector: gpu.Detector{Runner: runner}, GPUInspector: gpu.LDDInspector{Runner: runner},
		BridgeClient: client, Bridge: bridge, Runtime: runtimeController,
		MoonBridgeBinary: moonbridgeBinary, CodexBinary: codexBinary, ClaudeBinary: claudeBinary, KimiBinary: kimiBinary, ModelCatalog: modelCatalog,
		CandidateRoots: roots, CandidateServers: candidates,
		CatalogLock: arkeyruntime.FileLock{Path: filepath.Join(paths.LocalStateDir(), "model-catalog.lock")},
		Workspace:   workspace, config: cfg,
	}, nil
}

func (s *Services) Refresh(ctx context.Context) (app.Status, error) {
	cfg := s.snapshot()
	bridge := s.BridgeClient.Status(ctx, s.MoonBridgeBinary, cfg.MoonBridge.Config)
	claudeStatus := executableStatus(s.ClaudeBinary)
	if claudeStatus == "ready" {
		claudeStatus = "bridge ingress pending"
	}
	status := app.Status{
		Workspace: s.workspaceLabel(), Runtime: executableStatus(s.clientBinary(cfg.Client)),
		MoonBridge: string(bridge.State), ReducedMotion: cfg.UI.ReducedMotion,
		Client: cfg.Client, Clients: map[string]string{
			"codex": executableStatus(s.CodexBinary), "claude": claudeStatus, "kimi": executableStatus(s.KimiBinary),
		},
		Route: app.Route{Mode: cfg.Mode, Backend: cfg.Frontier.Backend, Model: selectedModel(cfg), LocalRuntime: cfg.Local.Runtime, LocalModel: cfg.Local.Model},
	}
	status.GPU = s.gpuSummary(ctx, cfg)
	if cfg.Local.Model != "" {
		loadedModel, ready, err := s.Runtime.Loaded(ctx, runtimeConfig(cfg, s.Paths, s.localContextSize(ctx, cfg)))
		if err == nil && loadedModel != "" {
			status.LocalActive = true
			status.LoadedModel = loadedModel
			if ready {
				status.LocalLoaded = true
				status.Runtime = "local loaded"
			} else {
				status.Runtime = "local starting"
			}
		} else if err == nil && cfg.Mode == "local" {
			status.Runtime = "local stopped"
		}
	}
	return status, nil
}

func (s *Services) SelectClient(ctx context.Context, client string) (app.Status, error) {
	client = strings.ToLower(client)
	if err := ctx.Err(); err != nil {
		return app.Status{}, err
	}
	if err := s.ValidateClient(client); err != nil {
		return app.Status{}, err
	}
	if err := s.updateConfig(func(cfg *config.Config) { cfg.Client = client }); err != nil {
		return app.Status{}, err
	}
	return s.Refresh(ctx)
}

func (s *Services) ValidateClient(client string) error {
	if client != "codex" && client != "claude" && client != "kimi" {
		return fmt.Errorf("unknown TUI client %q", client)
	}
	if executableStatus(s.clientBinary(client)) != "ready" {
		return fmt.Errorf("official %s client has not been snapshotted", client)
	}
	if client == "claude" {
		return errors.New("Arkey Claude is snapshotted, but MoonBridge Anthropic ingress is not implemented yet")
	}
	return nil
}

// UnloadLocal releases the active model from memory without forgetting the
// selection. The next Arkey Codex launch can load the same model again.
func (s *Services) UnloadLocal(ctx context.Context) (app.Status, error) {
	if err := s.Runtime.Stop(ctx); err != nil {
		return app.Status{}, fmt.Errorf("unload local model: %w", err)
	}
	return s.Refresh(ctx)
}

func (s *Services) DiscoverModels(ctx context.Context) ([]app.ModelSummary, error) {
	discovery, err := models.Discover(ctx, []string{s.Paths.ModelDir})
	if err != nil {
		return nil, err
	}
	out := make([]app.ModelSummary, 0, len(discovery.Models))
	for _, model := range discovery.Models {
		out = append(out, app.ModelSummary{Path: model.Path, Name: model.Name, Detail: relativeParent(s.Paths.ModelDir, model.Parent) + " · " + byteSize(model.Size)})
	}
	return out, nil
}

func (s *Services) SelectFrontier(ctx context.Context, backend string) (app.Status, error) {
	backend = strings.ToLower(backend)
	if frontierModel(backend) == "" {
		return app.Status{}, fmt.Errorf("unknown frontier backend %q", backend)
	}
	if err := ctx.Err(); err != nil {
		return app.Status{}, err
	}
	if err := s.updateConfig(func(cfg *config.Config) {
		cfg.Mode = "frontier"
		cfg.Frontier.Backend = backend
	}); err != nil {
		return app.Status{}, err
	}
	return s.Refresh(ctx)
}

func (s *Services) ActivateLocal(ctx context.Context, runtimeName string, model app.ModelSummary) (app.Status, error) {
	if runtimeName != "llama" {
		return app.Status{}, errors.New("tinygrad local serving is in development; use llama.cpp")
	}
	canonical, err := filepath.EvalSymlinks(model.Path)
	if err != nil {
		return app.Status{}, fmt.Errorf("local model: %w", err)
	}
	cfg := s.snapshot()
	cfg.Local.Model = canonical
	if _, rollback, err := s.Runtime.Start(ctx, runtimeConfig(cfg, s.Paths, s.localContextSize(ctx, cfg))); err != nil {
		if rollback != nil {
			return app.Status{}, fmt.Errorf("load failed: %w; previous-model rollback failed: %v", err, rollback)
		}
		return app.Status{}, err
	}
	if err := s.updateConfig(func(current *config.Config) {
		current.Mode = "local"
		current.Local.Runtime = "llama"
		current.Local.Model = canonical
	}); err != nil {
		return app.Status{}, err
	}
	return s.Refresh(ctx)
}

func (s *Services) ScanGPU(ctx context.Context) (app.Status, error) {
	detected, err := s.Detector.Detect(ctx)
	if err != nil {
		return app.Status{}, err
	}
	if override := os.Getenv("ARKEY_GPU_VENDOR_OVERRIDE"); override != "" {
		detected.Vendor = gpu.Vendor(strings.ToLower(override))
		detected.Name = envOr("ARKEY_GPU_NAME_OVERRIDE", string(detected.Vendor)+" GPU")
		if detected.Vendor != gpu.NVIDIA && detected.Vendor != gpu.AMD {
			return app.Status{}, fmt.Errorf("invalid ARKEY_GPU_VENDOR_OVERRIDE %q", override)
		}
	}
	if detected.Vendor != gpu.NVIDIA && detected.Vendor != gpu.AMD {
		return app.Status{}, errors.New("no supported NVIDIA or AMD compute GPU was detected")
	}
	candidates := append([]string(nil), s.CandidateServers...)
	if len(candidates) == 0 {
		candidates, err = gpu.CandidateServers(ctx, s.CandidateRoots)
		if err != nil {
			return app.Status{}, err
		}
	}
	server := ""
	if override := os.Getenv("ARKEY_LLAMA_BACKEND_OVERRIDE"); override != "" && override == string(detected.Vendor) && len(candidates) > 0 {
		server = candidates[0]
	} else {
		server, err = gpu.FindAligned(ctx, detected.Vendor, candidates, s.GPUInspector)
	}
	if err != nil {
		return app.Status{}, fmt.Errorf("no %s-enabled llama-server was found", detected.Vendor)
	}
	server, err = filepath.EvalSymlinks(server)
	if err != nil {
		return app.Status{}, err
	}
	if s.CatalogLock == nil {
		return app.Status{}, errors.New("model catalog lock is not configured")
	}
	unlock, lockErr := s.CatalogLock.Lock(ctx)
	if lockErr != nil {
		return app.Status{}, fmt.Errorf("lock model catalog: %w", lockErr)
	}
	err = models.UpdateCatalog(s.ModelCatalog)
	unlockErr := unlock()
	if err != nil {
		return app.Status{}, fmt.Errorf("register local Codex model metadata: %w", err)
	}
	if unlockErr != nil {
		return app.Status{}, fmt.Errorf("unlock model catalog: %w", unlockErr)
	}
	if err = s.updateConfig(func(cfg *config.Config) {
		cfg.Hardware.Vendor = string(detected.Vendor)
		cfg.Hardware.Name = detected.Name
		cfg.Local.LlamaServer = server
	}); err != nil {
		return app.Status{}, err
	}
	return s.Refresh(ctx)
}

func (s *Services) PrepareLaunch(ctx context.Context, model string) error {
	cfg := s.snapshot()
	if model == moonbridgeLocalRoute || cfg.Mode == "local" && model == selectedModel(cfg) {
		if cfg.Local.Model == "" {
			return errors.New("no local GGUF is selected")
		}
		if _, rollback, err := s.Runtime.Start(ctx, runtimeConfig(cfg, s.Paths, s.localContextSize(ctx, cfg))); err != nil {
			if rollback != nil {
				return fmt.Errorf("local startup failed: %w; rollback failed: %v", err, rollback)
			}
			return err
		}
		return nil
	}
	if err := s.Bridge.EnsureRoute(ctx, model); err != nil {
		return err
	}
	catalog, err := s.BridgeClient.Catalog(ctx)
	if err != nil {
		return err
	}
	if model != "" && !catalog.HasRoute(model) {
		return fmt.Errorf("MoonBridge route is not configured: %s", model)
	}
	return nil
}

func (s *Services) SelectedModel() string  { return selectedModel(s.snapshot()) }
func (s *Services) SelectedClient() string { return s.snapshot().Client }

func (s *Services) MoonBridgeURL() string {
	address := s.snapshot().MoonBridge.Address
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return strings.TrimRight(address, "/")
	}
	return "http://" + strings.TrimRight(address, "/")
}

// ClientContextWindow reports the window the client should plan against. On the
// local route this must be the window llama-server is actually given, derived
// the same way, or the client compacts against a budget that does not exist.
func (s *Services) ClientContextWindow() int {
	cfg := s.snapshot()
	if cfg.Mode == "local" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.localContextSize(ctx, cfg)
	}
	return 262144
}

// ClientMaxOutputTokens derives a max-output budget from ClientContextWindow: a
// max-output larger than the context window is incoherent and causes the client
// to plan against a budget that cannot exist.
func (s *Services) ClientMaxOutputTokens() int {
	tokens := s.ClientContextWindow() / 4
	if tokens < 4096 {
		return 4096
	}
	if tokens > 32768 {
		return 32768
	}
	return tokens
}

func (s *Services) ClientBinary(client string) string { return s.clientBinary(client) }

func (s *Services) clientBinary(client string) string {
	switch client {
	case "claude":
		return s.ClaudeBinary
	case "kimi":
		return s.KimiBinary
	default:
		return s.CodexBinary
	}
}

func (s *Services) snapshot() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Services) updateConfig(change func(*config.Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	change(&next)
	if err := s.Store.Save(next); err != nil {
		return err
	}
	s.config = next
	return nil
}

func (s *Services) gpuSummary(ctx context.Context, cfg config.Config) string {
	if cfg.Hardware.Vendor == "unknown" || cfg.Local.LlamaServer == "" {
		return "not scanned"
	}
	detected, err := s.Detector.Detect(ctx)
	if err != nil || string(detected.Vendor) != cfg.Hardware.Vendor {
		return "rescan required"
	}
	backend, err := s.GPUInspector.Backend(ctx, cfg.Local.LlamaServer)
	if err != nil || string(backend) != cfg.Hardware.Vendor {
		return "rescan required"
	}
	return cfg.Hardware.Name + " · aligned"
}

func selectedModel(cfg config.Config) string {
	if cfg.Mode == "local" {
		return moonbridgeLocalRoute
	}
	return frontierModel(cfg.Frontier.Backend)
}

func frontierModel(backend string) string {
	switch backend {
	case "deepseek":
		return envOr("ARKEY_DEEPSEEK_MODEL", "deepseek-v4-pro")
	case "codex":
		return envOr("ARKEY_CODEX_MODEL", "gpt-5.6-sol")
	case "claude":
		return envOr("ARKEY_CLAUDE_MODEL", "claude-sonnet-4-6")
	default:
		return ""
	}
}

func runtimeConfig(cfg config.Config, paths platform.Paths, contextSize int) arkeyruntime.Config {
	return arkeyruntime.Config{Server: cfg.Local.LlamaServer, Model: cfg.Local.Model, Vendor: cfg.Hardware.Vendor, Port: cfg.Local.Port, ContextSize: contextSize, LogPath: filepath.Join(paths.LogsDir(), "llama.log"), ChatTemplate: chatTemplateFor(cfg.Local.Model, paths)}
}

// qwen35 ships a chat template whose assistant branch renders prior tool-call
// arguments through Jinja's `| safe` filter. Replayed tool calls come back
// malformed, so the model sees corrupted examples of its own tool-calling and
// degrades as a session accumulates calls: it starts announcing an action
// ("Now let me read the file") instead of emitting the call, which ends the
// agent loop. Measured on Qwen3.6-27B: with the stock template a resumed turn
// produced 0 tool calls; with `| safe` removed the same task produced 14, 8, 13
// and 18 across successive turns and completed. See llama.cpp#20837.
//
// Applied only to the affected architecture. Overriding the template for a model
// that does not need it would be worse than the bug.
func chatTemplateFor(model string, paths platform.Paths) string {
	if model == "" {
		return ""
	}
	g, err := models.ReadGGUF(model)
	if err != nil || g.Architecture != "qwen35" {
		return ""
	}
	override := filepath.Join(paths.Home, ".local", "libexec", "arkey", "templates", "qwen35-toolcall-fix.jinja")
	if info, statErr := os.Stat(override); statErr != nil || !info.Mode().IsRegular() {
		return ""
	}
	return override
}

// fallbackContextSize is used only when the window cannot be derived: no GPU
// reading, unreadable model metadata, or a card too small. It is deliberately
// modest — a wrong-but-small window degrades a session, a wrong-but-large one
// corrupts it, because the client plans against capacity that does not exist.
const fallbackContextSize = 32768

// localContextSize resolves the llama context window. A positive configured
// value is an explicit operator pin and is always honoured. Zero means derive
// it from this machine: total VRAM, the model's own size, and the KV cost per
// token implied by the model's architecture.
func (s *Services) localContextSize(ctx context.Context, cfg config.Config) int {
	if cfg.Local.ContextSize > 0 {
		return cfg.Local.ContextSize
	}
	if cfg.Local.Model == "" {
		return fallbackContextSize
	}
	detected, err := s.Detector.Detect(ctx)
	if err != nil {
		return fallbackContextSize
	}
	if n := models.DeriveContextSizeForModel(cfg.Local.Model, detected.TotalVRAMBytes, arkeyruntime.KVCacheBytesPerElement); n > 0 {
		return n
	}
	return fallbackContextSize
}

func (s *Services) workspaceLabel() string {
	if s.Workspace == s.Paths.Home {
		return "~"
	}
	if strings.HasPrefix(s.Workspace, s.Paths.Home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(s.Workspace, s.Paths.Home)
	}
	return s.Workspace
}

func executableStatus(path string) string {
	if regularExecutable(path) {
		return "ready"
	}
	return "incomplete"
}

func relativeParent(root, parent string) string {
	value, err := filepath.Rel(root, parent)
	if err != nil || value == "." {
		return "."
	}
	return value
}

func byteSize(size int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

var _ app.Services = (*Services)(nil)
