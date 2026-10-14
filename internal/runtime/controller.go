// Package runtime manages the machine-local inference services.  It deliberately
// contains no terminal output; callers translate its typed errors into UI state.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

var (
	ErrInvalidModel  = errors.New("runtime: model must be a .gguf file")
	ErrUnaligned     = errors.New("runtime: server is not aligned to the GPU")
	ErrUnmanagedPort = errors.New("runtime: configured port is owned by an unmanaged process")
	ErrHealth        = errors.New("runtime: server did not become healthy")
	ErrAcceleration  = errors.New("runtime: expected GPU acceleration was not initialized")
)

type Config struct {
	Engine                string            `json:"engine,omitempty"`
	Environment           map[string]string `json:"environment,omitempty"`
	ExtraArgs             []string          `json:"extra_args,omitempty"`
	ServedModel           string            `json:"served_model,omitempty"`
	Server, Model, Vendor string
	Port, ContextSize     int
	LogPath               string
	// ChatTemplate overrides the template embedded in the GGUF. Empty means use
	// the model's own. Set only where the embedded template is known broken; see
	// llamaArgs.
	ChatTemplate string
}

// State is persisted only after a complete successful start. StartTime prevents
// a reused Linux PID from being mistaken for an Arkey-owned process.
type State struct {
	LaunchConfig    *Config `json:"launch_config,omitempty"`
	PID             int     `json:"pid"`
	Executable      string  `json:"executable"`
	ArgsFingerprint string  `json:"args_fingerprint"`
	Model           string  `json:"model"`
	Port            int     `json:"port"`
	StartTime       uint64  `json:"start_time"`
	Server          string  `json:"server"`
	Vendor          string  `json:"vendor"`
	LogPath         string  `json:"log_path"`
	ContextSize     int     `json:"context_size"`
	Manager         string  `json:"manager"`
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
	Healthy(context.Context, int) (bool, error)
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

// Engine supplies the parts of lifecycle management that differ by backend.
type Engine struct {
	Validate       func(Config) error
	Args           func(Config) []string
	Backend        Backend
	Service        Service
	Health         func(Config) Health
	Unit, Route    string
	RequireService bool
	Recognize      func(context.Context, Inspector, int, State) (Process, string, error)
}

type ConfiguredService interface {
	StartConfigured(context.Context, string, []string, string, map[string]string) (int, error)
}

type Controller struct {
	Engines map[string]Engine

	Store        Store
	Inspector    Inspector
	Launcher     Launcher
	Service      Service
	Health       Health
	MoonBridge   MoonBridge
	Backend      Backend
	Lock         Lock
	Clock        Clock
	Unit         string
	Attempts     int
	Interval     time.Duration
	mu           sync.Mutex
	defaultsOnce sync.Once
}

func (c *Controller) engine(name string) Engine {
	if name != "" && name != "llama" {
		return c.Engines[name]
	}
	return Engine{Validate: Config.validate, Args: llamaArgs, Backend: c.Backend, Service: c.Service, Health: func(Config) Health { return c.Health }, Unit: c.Unit, Recognize: recognizeLlama}
}
func stateEngine(s State) string {
	if s.LaunchConfig != nil {
		return s.LaunchConfig.Engine
	}
	return ""
}
func (c *Controller) ensureRoute(ctx context.Context, e Engine) error {
	if e.Route != "" {
		bridge, ok := c.MoonBridge.(interface {
			EnsureRoute(context.Context, string) error
		})
		if !ok {
			return errors.New("runtime: bridge does not support engine routes")
		}
		return bridge.EnsureRoute(ctx, e.Route)
	}
	return c.MoonBridge.EnsureLocalRoute(ctx)
}

func (c *Controller) defaults() {
	c.defaultsOnce.Do(func() {
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
	})
}
func (c *Controller) Start(ctx context.Context, cfg Config) (state State, rollback error, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaults()
	engine := c.engine(cfg.Engine)
	if engine.Validate == nil || engine.Args == nil || engine.Backend == nil || engine.Health == nil {
		return State{}, nil, fmt.Errorf("runtime: unknown engine %q", cfg.Engine)
	}
	if err = engine.Validate(cfg); err != nil {
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
	if engine.RequireService && (engine.Service == nil || !engine.Service.Available(ctx)) {
		return State{}, nil, errors.New("runtime: required bounded service is unavailable; refusing an uncontained start")
	}
	if ok, e := engine.Backend.Aligned(ctx, cfg.Server, cfg.Vendor); e != nil || !ok {
		if e != nil {
			return State{}, nil, e
		}
		return State{}, nil, ErrUnaligned
	}
	if e = c.ensureRoute(ctx, engine); e != nil {
		return State{}, nil, e
	}
	previous, loadErr := c.Store.Load(ctx)
	if loadErr != nil && !errors.Is(loadErr, ErrNoState) {
		return State{}, nil, loadErr
	}
	// A systemd unit may have restarted after its state was persisted. Adopt
	// the replacement PID before checking the configured port; otherwise our
	// own restarted llama-server is incorrectly rejected as unmanaged.
	if loadErr == nil && !c.owns(ctx, previous) {
		if current, recognized := c.currentSystemdState(ctx, previous); recognized {
			previous = current
		}
	}
	if c.matchesHealthy(ctx, previous, cfg) {
		return previous, nil, nil
	}
	if owner, e := c.Inspector.PortOwner(ctx, cfg.Port); e != nil {
		return State{}, nil, e
	} else if owner > 0 && (!c.owns(ctx, previous) || owner != previous.PID) {
		return State{}, nil, ErrUnmanagedPort
	}
	if previous.PID > 0 {
		if e = c.stopOwned(ctx, previous); e != nil {
			return State{}, nil, e
		}
	}
	started, e := c.start(ctx, cfg)
	if e != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		return State{}, c.restore(cleanupCtx, previous, cfg), e
	}
	if e = c.waitHealthy(ctx, cfg, started); e == nil {
		var ok bool
		ok, e = c.engine(cfg.Engine).Backend.Accelerated(ctx, cfg.LogPath, cfg.Vendor)
		if e == nil && !ok {
			e = ErrAcceleration
		}
	}
	if e != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		_ = c.stopOwned(cleanupCtx, started)
		rollback = c.restore(cleanupCtx, previous, cfg)
		return State{}, rollback, e
	}
	if e = c.Store.Save(ctx, started); e != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
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
		ok, healthErr := c.engine(stateEngine(s)).Health(launchConfigForState(s, cfg)).Healthy(ctx, s.Port)
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
	ok, healthErr := c.engine(stateEngine(current)).Health(launchConfigForState(current, cfg)).Healthy(ctx, current.Port)
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

// ContextSize participates in the match: a running server cannot change its
// window, so reusing one started with a different --ctx-size would silently
// leave the client planning against a window the server does not have.
func (c *Controller) matchesHealthy(ctx context.Context, s State, cfg Config) bool {
	if stateEngine(s) != cfg.Engine || (s.LaunchConfig != nil && !sameLaunchConfig(*s.LaunchConfig, cfg)) {
		return false
	}
	if s.Model != cfg.Model || s.Port != cfg.Port || s.ContextSize != cfg.ContextSize || !c.owns(ctx, s) {
		return false
	}
	ok, e := c.engine(cfg.Engine).Health(cfg).Healthy(ctx, cfg.Port)
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
	engine := c.engine(stateEngine(base))
	pidService, pidOK := engine.Service.(PIDService)
	if base.Manager != "systemd" || !pidOK || engine.Recognize == nil || base.Server == "" || base.Port < 1 {
		return State{}, false
	}
	for attempt := 0; attempt < 3; attempt++ {
		pid, err := pidService.MainPID(ctx, engine.Unit)
		if err != nil || pid < 1 {
			return State{}, false
		}
		process, model, err := engine.Recognize(ctx, c.Inspector, pid, base)
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
	engine := c.engine(cfg.Engine)
	args := engine.Args(cfg)
	var pid int
	var e error
	manager := "direct"
	if engine.Service != nil && engine.Service.Available(ctx) {
		if service, ok := engine.Service.(ConfiguredService); ok {
			pid, e = service.StartConfigured(ctx, engine.Unit, args, cfg.LogPath, cfg.Environment)
		} else {
			pid, e = engine.Service.Start(ctx, engine.Unit, args, cfg.LogPath)
		}
		manager = "systemd"
	} else {
		if engine.RequireService {
			return State{}, errors.New("runtime: required bounded service became unavailable")
		}
		pid, e = c.Launcher.StartDirect(ctx, args, cfg.LogPath)
	}
	if e != nil {
		return State{}, e
	}
	p, e := c.Inspector.Process(ctx, pid)
	if e != nil {
		c.cleanupStarted(manager, pid, cfg.Engine)
		return State{}, e
	}
	savedConfig := cfg
	return State{LaunchConfig: &savedConfig, PID: pid, Executable: p.Executable, ArgsFingerprint: p.ArgsFingerprint, StartTime: p.StartTime, Model: cfg.Model, Port: cfg.Port, Server: cfg.Server, Vendor: cfg.Vendor, LogPath: cfg.LogPath, ContextSize: cfg.ContextSize, Manager: manager}, nil
}

func (c *Controller) cleanupStarted(manager string, pid int, name string) {
	engine := c.engine(name)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if manager == "systemd" && engine.Service != nil {
		_ = engine.Service.Stop(cleanupCtx, engine.Unit)
		return
	}
	if launcher, ok := c.Launcher.(interface {
		Terminate(context.Context, int) error
	}); ok {
		_ = launcher.Terminate(cleanupCtx, pid)
	}
}

// ProcessLiveness answers "is this PID running at all", separately from
// "is it ours". An Inspector that implements it lets the controller tell a
// record that outlived its process from a stranger holding the port.
type ProcessLiveness interface {
	Alive(pid int) bool
}

// recordIsStale reports that the PID in a stored State no longer exists, so
// there is nothing left to stop.
//
// When liveness cannot be determined at all, this returns false: refusing
// loudly is the safe failure. Silently clearing a record we cannot vouch for
// would be a way to orphan a live server.
func (c *Controller) recordIsStale(pid int) bool {
	if pid < 1 {
		return true
	}
	live, ok := c.Inspector.(ProcessLiveness)
	if !ok {
		return false
	}
	return !live.Alive(pid)
}

func (c *Controller) stopOwned(ctx context.Context, s State) error {
	if !c.owns(ctx, s) {
		// Order matters. A recorded PID that is gone can mean two things, and
		// systemd's is the recoverable one: the unit restarted under a new PID
		// and currentSystemdState finds it. Ask that first, or a perfectly
		// live server gets abandoned as "stale".
		current, recognized := c.currentSystemdState(ctx, s)
		switch {
		case recognized:
			s = current
		case c.recordIsStale(s.PID):
			// The record outlived the process -- killed, rebooted, or started
			// outside Arkey and since stopped. There is nothing to stop, and
			// refusing wedges the controller permanently: Stop() returns this
			// error before reaching Store.Clear, so the stale record survives
			// and every later Start trips over the same record and the same
			// refusal. The only escape was deleting the state file by hand.
			return nil
		default:
			return fmt.Errorf("runtime: refusing to stop process %d: it is running but is not the server this record describes (started outside Arkey, or the PID was reused)", s.PID)
		}
	}
	engine := c.engine(stateEngine(s))
	if s.Manager == "systemd" {
		if engine.Service == nil || !engine.Service.Available(ctx) {
			return errors.New("runtime: service manager unavailable; cannot safely stop owned unit")
		}
		return engine.Service.Stop(ctx, engine.Unit)
	}
	if x, ok := c.Launcher.(interface {
		Terminate(context.Context, int) error
	}); ok {
		return x.Terminate(ctx, s.PID)
	}
	return errors.New("runtime: direct launcher cannot terminate process")
}
func (c *Controller) waitHealthy(ctx context.Context, cfg Config, state State) error {
	for i := 0; i < c.Attempts; i++ {
		if c.engine(cfg.Engine).Recognize == nil && !c.owns(ctx, state) {
			return fmt.Errorf("%w: process exited; see %s", ErrHealth, cfg.LogPath)
		}
		ok, e := c.engine(cfg.Engine).Health(cfg).Healthy(ctx, cfg.Port)
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
	if old.LaunchConfig != nil {
		cfg = *old.LaunchConfig
	} else {
		cfg.Engine = ""
		cfg.Environment = nil
		cfg.ExtraArgs = nil
		cfg.ServedModel = ""
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
		cfg.Port = old.Port
	}
	if err := c.ensureRoute(ctx, c.engine(cfg.Engine)); err != nil {
		return err
	}

	s, e := c.start(ctx, cfg)
	if e != nil {
		return e
	}
	if e = c.waitHealthy(ctx, cfg, s); e != nil {
		_ = c.stopOwned(ctx, s)
		return e
	}
	if ok, accelerationErr := c.engine(cfg.Engine).Backend.Accelerated(ctx, cfg.LogPath, cfg.Vendor); accelerationErr != nil {
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

func sameLaunchConfig(a, b Config) bool {
	return a.Engine == b.Engine && a.Server == b.Server && a.Model == b.Model && a.Vendor == b.Vendor && a.Port == b.Port && a.ContextSize == b.ContextSize && a.LogPath == b.LogPath && a.ChatTemplate == b.ChatTemplate && a.ServedModel == b.ServedModel && maps.Equal(a.Environment, b.Environment) && slices.Equal(a.ExtraArgs, b.ExtraArgs)
}
func launchConfigForState(s State, fallback Config) Config {
	if s.LaunchConfig != nil {
		return *s.LaunchConfig
	}
	fallback.Engine = ""
	fallback.Port = s.Port
	fallback.ContextSize = s.ContextSize
	return fallback
}
