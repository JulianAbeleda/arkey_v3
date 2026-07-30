// Package runtime manages the machine-local llama.cpp service.  It deliberately
// contains no terminal output; callers translate its typed errors into UI state.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

var (
	ErrInvalidModel  = errors.New("runtime: model must be a .gguf file")
	ErrUnaligned     = errors.New("runtime: llama server is not aligned to the GPU")
	ErrUnmanagedPort = errors.New("runtime: configured port is owned by an unmanaged process")
	ErrHealth        = errors.New("runtime: llama server did not become healthy")
	ErrAcceleration  = errors.New("runtime: expected GPU acceleration was not initialized")
)

type Config struct {
	Server, Model, Vendor string
	Port, ContextSize     int
	LogPath               string
}

func (c Config) validate() error {
	if !strings.HasSuffix(strings.ToLower(c.Model), ".gguf") || c.Model == "" {
		return ErrInvalidModel
	}
	if c.Server == "" || c.LogPath == "" || c.Port < 1 || c.Port > 65535 || c.ContextSize < 1 {
		return fmt.Errorf("runtime: invalid configuration")
	}
	if c.Vendor != "nvidia" && c.Vendor != "amd" {
		return fmt.Errorf("runtime: unsupported GPU vendor %q", c.Vendor)
	}
	return nil
}

// State is persisted only after a complete successful start. StartTime prevents
// a reused Linux PID from being mistaken for an Arkey-owned process.
type State struct {
	PID             int    `json:"pid"`
	Executable      string `json:"executable"`
	ArgsFingerprint string `json:"args_fingerprint"`
	Model           string `json:"model"`
	Port            int    `json:"port"`
	StartTime       uint64 `json:"start_time"`
	Server          string `json:"server"`
	Vendor          string `json:"vendor"`
	LogPath         string `json:"log_path"`
	ContextSize     int    `json:"context_size"`
	Manager         string `json:"manager"`
}
type Store interface {
	Load(context.Context) (State, error)
	Save(context.Context, State) error
	Clear(context.Context) error
}

var ErrNoState = errors.New("runtime: no state")

type Process struct {
	PID             int
	Executable      string
	ArgsFingerprint string
	StartTime       uint64
}
type Inspector interface {
	Process(context.Context, int) (Process, error)
	PortOwner(context.Context, int) (int, error)
}
type Launcher interface {
	StartDirect(context.Context, []string, string) (int, error)
}

// Service is optional. A usable user systemd manager returns true from Available.
type Service interface {
	Available(context.Context) bool
	Start(context.Context, string, []string, string) (int, error)
	Stop(context.Context, string) error
}
type PIDService interface {
	MainPID(context.Context, string) (int, error)
}
type LlamaInspector interface {
	LlamaProcess(context.Context, int, string, int) (Process, string, error)
}
type Health interface {
	LlamaHealthy(context.Context, int) (bool, error)
}
type MoonBridge interface{ EnsureLocalRoute(context.Context) error }
type Backend interface {
	Aligned(ctx context.Context, executable, vendor string) (bool, error)
	Accelerated(ctx context.Context, logPath, vendor string) (bool, error)
}
type Lock interface {
	Lock(context.Context) (func() error, error)
}
type Clock interface {
	After(time.Duration) <-chan time.Time
}

type Controller struct {
	Store      Store
	Inspector  Inspector
	Launcher   Launcher
	Service    Service
	Health     Health
	MoonBridge MoonBridge
	Backend    Backend
	Lock       Lock
	Clock      Clock
	Unit       string
	Attempts   int
	Interval   time.Duration
	mu         sync.Mutex
}

func (c *Controller) defaults() {
	if c.Unit == "" {
		c.Unit = "arkey-llama"
	}
	if c.Attempts == 0 {
		c.Attempts = 600
	}
	if c.Interval == 0 {
		c.Interval = 500 * time.Millisecond
	}
	if c.Clock == nil {
		c.Clock = realClock{}
	}
}
func (c *Controller) Start(ctx context.Context, cfg Config) (state State, rollback error, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaults()
	if err = cfg.validate(); err != nil {
		return State{}, nil, err
	}
	if c.Lock == nil {
		return State{}, nil, errors.New("runtime: missing operation lock")
	}
	unlock, e := c.Lock.Lock(ctx)
	if e != nil {
		return State{}, nil, e
	}
	defer unlock()
	if ok, e := c.Backend.Aligned(ctx, cfg.Server, cfg.Vendor); e != nil || !ok {
		if e != nil {
			return State{}, nil, e
		}
		return State{}, nil, ErrUnaligned
	}
	if e = c.MoonBridge.EnsureLocalRoute(ctx); e != nil {
		return State{}, nil, e
	}
	previous, loadErr := c.Store.Load(ctx)
	if loadErr != nil && !errors.Is(loadErr, ErrNoState) {
		return State{}, nil, loadErr
	}
	if c.matchesHealthy(ctx, previous, cfg) {
		return previous, nil, nil
	}
	if owner, e := c.Inspector.PortOwner(ctx, cfg.Port); e != nil {
		return State{}, nil, e
	} else if owner > 0 && !c.owns(ctx, previous) {
		return State{}, nil, ErrUnmanagedPort
	}
	if previous.PID > 0 {
		if e = c.stopOwned(ctx, previous); e != nil {
			return State{}, nil, e
		}
	}
	started, e := c.start(ctx, cfg)
	if e != nil {
		return State{}, nil, e
	}
	if e = c.waitHealthy(ctx, cfg.Port); e == nil {
		var ok bool
		ok, e = c.Backend.Accelerated(ctx, cfg.LogPath, cfg.Vendor)
		if e == nil && !ok {
			e = ErrAcceleration
		}
	}
	if e != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.stopOwned(cleanupCtx, started)
		rollback = c.restore(cleanupCtx, previous, cfg)
		return State{}, rollback, e
	}
	if e = c.Store.Save(ctx, started); e != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.stopOwned(cleanupCtx, started)
		rollback = c.restore(cleanupCtx, previous, cfg)
		return State{}, rollback, e
	}
	return started, nil, nil
}
func (c *Controller) Status(ctx context.Context, cfg Config) (bool, error) {
	c.defaults()
	s, e := c.Store.Load(ctx)
	if errors.Is(e, ErrNoState) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return c.matchesHealthy(ctx, s, cfg), nil
}

// Loaded returns the healthy model actually held by the local runtime. For a
// systemd restart it revalidates the current MainPID and argv instead of
// trusting a stale persisted PID.
func (c *Controller) Loaded(ctx context.Context, cfg Config) (string, bool, error) {
	c.defaults()
	s, err := c.Store.Load(ctx)
	if err != nil && !errors.Is(err, ErrNoState) {
		return "", false, err
	}
	if err == nil && c.owns(ctx, s) {
		ok, healthErr := c.Health.LlamaHealthy(ctx, s.Port)
		return s.Model, healthErr == nil && ok, nil
	}
	base := s
	if base.Server == "" {
		base.Server = cfg.Server
	}
	if base.Port == 0 {
		base.Port = cfg.Port
	}
	base.Manager = "systemd"
	current, recognized := c.currentSystemdState(ctx, base)
	if !recognized {
		return "", false, nil
	}
	ok, healthErr := c.Health.LlamaHealthy(ctx, current.Port)
	return current.Model, healthErr == nil && ok, nil
}

func (c *Controller) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaults()
	u, e := c.Lock.Lock(ctx)
	if e != nil {
		return e
	}
	defer u()
	s, e := c.Store.Load(ctx)
	if errors.Is(e, ErrNoState) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = c.stopOwned(ctx, s); e != nil {
		return e
	}
	return c.Store.Clear(ctx)
}
func (c *Controller) matchesHealthy(ctx context.Context, s State, cfg Config) bool {
	if s.Model != cfg.Model || s.Port != cfg.Port || !c.owns(ctx, s) {
		return false
	}
	ok, e := c.Health.LlamaHealthy(ctx, cfg.Port)
	return e == nil && ok
}
func (c *Controller) owns(ctx context.Context, s State) bool {
	if s.PID < 1 {
		return false
	}
	p, e := c.Inspector.Process(ctx, s.PID)
	return e == nil && p.Executable == s.Executable && p.ArgsFingerprint == s.ArgsFingerprint && p.StartTime == s.StartTime
}

func (c *Controller) currentSystemdState(ctx context.Context, base State) (State, bool) {
	pidService, pidOK := c.Service.(PIDService)
	llamaInspector, inspectOK := c.Inspector.(LlamaInspector)
	if base.Manager != "systemd" || !pidOK || !inspectOK || base.Server == "" || base.Port < 1 {
		return State{}, false
	}
	for attempt := 0; attempt < 3; attempt++ {
		pid, err := pidService.MainPID(ctx, c.Unit)
		if err != nil || pid < 1 {
			return State{}, false
		}
		process, model, err := llamaInspector.LlamaProcess(ctx, pid, base.Server, base.Port)
		if err == nil {
			base.PID = pid
			base.Executable = process.Executable
			base.ArgsFingerprint = process.ArgsFingerprint
			base.StartTime = process.StartTime
			base.Model = model
			return base, true
		}
		if ctx.Err() != nil {
			return State{}, false
		}
	}
	return State{}, false
}
func (c *Controller) start(ctx context.Context, cfg Config) (State, error) {
	if err := platform.EnsurePrivateDir(filepath.Dir(cfg.LogPath)); err != nil {
		return State{}, err
	}
	logFile, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return State{}, err
	}
	if err = logFile.Close(); err != nil {
		return State{}, err
	}
	args := llamaArgs(cfg)
	var pid int
	var e error
	manager := "direct"
	if c.Service != nil && c.Service.Available(ctx) {
		pid, e = c.Service.Start(ctx, c.Unit, args, cfg.LogPath)
		manager = "systemd"
	} else {
		pid, e = c.Launcher.StartDirect(ctx, args, cfg.LogPath)
	}
	if e != nil {
		return State{}, e
	}
	p, e := c.Inspector.Process(ctx, pid)
	if e != nil {
		c.cleanupStarted(manager, pid)
		return State{}, e
	}
	return State{PID: pid, Executable: p.Executable, ArgsFingerprint: p.ArgsFingerprint, StartTime: p.StartTime, Model: cfg.Model, Port: cfg.Port, Server: cfg.Server, Vendor: cfg.Vendor, LogPath: cfg.LogPath, ContextSize: cfg.ContextSize, Manager: manager}, nil
}

func (c *Controller) cleanupStarted(manager string, pid int) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if manager == "systemd" && c.Service != nil {
		_ = c.Service.Stop(cleanupCtx, c.Unit)
		return
	}
	if launcher, ok := c.Launcher.(interface {
		Terminate(context.Context, int) error
	}); ok {
		_ = launcher.Terminate(cleanupCtx, pid)
	}
}
// llamaArgs builds the llama-server command line.
//
// --parallel 1 is deliberate: llama-server's auto slot count (n_parallel = 4
// with kv_unified) segfaults on load for qwen35-arch models such as
// Qwen3.6-27B. Arkey serves one client at a time, so a single slot costs
// nothing and keeps the runtime from crashing on those models.
func llamaArgs(c Config) []string {
	return []string{c.Server, "--model", c.Model, "--alias", "arkey-local", "--host", "127.0.0.1", "--port", fmt.Sprint(c.Port), "--ctx-size", fmt.Sprint(c.ContextSize), "--gpu-layers", "all", "--parallel", "1"}
}
func (c *Controller) stopOwned(ctx context.Context, s State) error {
	if !c.owns(ctx, s) {
		current, recognized := c.currentSystemdState(ctx, s)
		if !recognized {
			return fmt.Errorf("runtime: refusing to stop unrecognized process %d", s.PID)
		}
		s = current
	}
	if s.Manager == "systemd" && c.Service != nil && c.Service.Available(ctx) {
		return c.Service.Stop(ctx, c.Unit)
	}
	if x, ok := c.Launcher.(interface {
		Terminate(context.Context, int) error
	}); ok {
		return x.Terminate(ctx, s.PID)
	}
	return errors.New("runtime: direct launcher cannot terminate process")
}
func (c *Controller) waitHealthy(ctx context.Context, port int) error {
	for i := 0; i < c.Attempts; i++ {
		ok, e := c.Health.LlamaHealthy(ctx, port)
		if e == nil && ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.Clock.After(c.Interval):
		}
	}
	return ErrHealth
}
func (c *Controller) restore(ctx context.Context, old State, cfg Config) error {
	if old.PID < 1 || old.Model == "" {
		return nil
	}
	cfg.Model = old.Model
	if old.Server != "" {
		cfg.Server = old.Server
	}
	if old.Vendor != "" {
		cfg.Vendor = old.Vendor
	}
	if old.LogPath != "" {
		cfg.LogPath = old.LogPath
	}
	if old.ContextSize > 0 {
		cfg.ContextSize = old.ContextSize
	}
	s, e := c.start(ctx, cfg)
	if e != nil {
		return e
	}
	if e = c.waitHealthy(ctx, cfg.Port); e != nil {
		_ = c.stopOwned(ctx, s)
		return e
	}
	if ok, accelerationErr := c.Backend.Accelerated(ctx, cfg.LogPath, cfg.Vendor); accelerationErr != nil {
		_ = c.stopOwned(ctx, s)
		return accelerationErr
	} else if !ok {
		_ = c.stopOwned(ctx, s)
		return ErrAcceleration
	}
	return c.Store.Save(ctx, s)
}
func ModelLabel(path string) string { return filepath.Base(path) }

type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
