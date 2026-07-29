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
	codexBinary := envOr("ARKEY_CODEX_BIN", envOr("CODEX_MOONBRIDGE_BIN", envOr("CODEX_BIN", filepath.Join(paths.Home, ".local", "bin", "codex-openai"))))
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
		MoonBridgeBinary: moonbridgeBinary, CodexBinary: codexBinary, ModelCatalog: modelCatalog,
		CandidateRoots: roots, CandidateServers: candidates,
		CatalogLock: arkeyruntime.FileLock{Path: filepath.Join(paths.LocalStateDir(), "model-catalog.lock")},
		Workspace:   workspace, config: cfg,
	}, nil
}

func (s *Services) Refresh(ctx context.Context) (app.Status, error) {
	cfg := s.snapshot()
	bridge := s.BridgeClient.Status(ctx, s.MoonBridgeBinary, cfg.MoonBridge.Config)
	status := app.Status{
		Workspace: s.workspaceLabel(), Runtime: executableStatus(s.CodexBinary),
		MoonBridge: string(bridge.State), ReducedMotion: cfg.UI.ReducedMotion,
		Route: app.Route{Mode: cfg.Mode, Backend: cfg.Frontier.Backend, Model: selectedModel(cfg), LocalRuntime: cfg.Local.Runtime, LocalModel: cfg.Local.Model},
	}
	status.GPU = s.gpuSummary(ctx, cfg)
	if cfg.Mode == "local" && cfg.Local.Model != "" {
		ready, err := s.Runtime.Status(ctx, runtimeConfig(cfg, s.Paths))
		if err == nil && ready {
			status.Runtime = "local loaded"
		} else if err == nil {
			status.Runtime = "local stopped"
		}
	}
	return status, nil
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
	if _, rollback, err := s.Runtime.Start(ctx, runtimeConfig(cfg, s.Paths)); err != nil {
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
		if _, rollback, err := s.Runtime.Start(ctx, runtimeConfig(cfg, s.Paths)); err != nil {
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

func (s *Services) SelectedModel() string { return selectedModel(s.snapshot()) }

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

func runtimeConfig(cfg config.Config, paths platform.Paths) arkeyruntime.Config {
	return arkeyruntime.Config{Server: cfg.Local.LlamaServer, Model: cfg.Local.Model, Vendor: cfg.Hardware.Vendor, Port: cfg.Local.Port, ContextSize: cfg.Local.ContextSize, LogPath: filepath.Join(paths.LogsDir(), "llama.log")}
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
