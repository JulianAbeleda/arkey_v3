package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

type BridgeManager struct {
	Client    moonbridge.Client
	Binary    string
	Config    string
	LogPath   string
	Store     arkeyruntime.Store
	Inspector arkeyruntime.Inspector
	Launcher  arkeyruntime.Launcher
	Service   arkeyruntime.Service
	Unit      string
	Attempts  int
	Interval  time.Duration
	mu        sync.Mutex
}

func (m *BridgeManager) EnsureLocalRoute(ctx context.Context) error {
	return m.EnsureRoute(ctx, moonbridgeLocalRoute)
}

func (m *BridgeManager) EnsureRoute(ctx context.Context, route string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if catalog, err := m.Client.Catalog(ctx); err == nil {
		if route == "" || catalog.HasRoute(route) {
			return nil
		}
	}
	if !regularExecutable(m.Binary) {
		return fmt.Errorf("pinned MoonBridge binary is missing or not executable: %s", m.Binary)
	}
	if !regularFile(m.Config) {
		return fmt.Errorf("MoonBridge config is missing: %s", m.Config)
	}
	if m.Unit == "" {
		m.Unit = "arkey-moonbridge"
	}
	if m.Attempts == 0 {
		m.Attempts = 80
	}
	if m.Interval == 0 {
		m.Interval = 250 * time.Millisecond
	}

	previous, err := m.Store.Load(ctx)
	if err != nil && !errors.Is(err, arkeyruntime.ErrNoState) {
		return err
	}
	online := false
	if _, err = m.Client.Catalog(ctx); err == nil {
		online = true
	}
	if previous.PID > 0 {
		if !m.owns(ctx, previous) {
			return fmt.Errorf("refusing to replace unrecognized MoonBridge process %d", previous.PID)
		}
		if err = m.stop(ctx, previous); err != nil {
			return err
		}
		_ = m.Store.Clear(ctx)
	} else if online {
		return errors.New("MoonBridge is online without the required local route and is not owned by Arkey")
	}

	if err = platform.EnsurePrivateDir(filepath.Dir(m.LogPath)); err != nil {
		return err
	}
	args := []string{m.Binary, "--config", m.Config}
	manager := "direct"
	var pid int
	if m.Service != nil && m.Service.Available(ctx) {
		pid, err = m.Service.Start(ctx, m.Unit, args, m.LogPath)
		manager = "systemd"
	} else {
		pid, err = m.Launcher.StartDirect(ctx, args, m.LogPath)
	}
	if err != nil {
		return err
	}
	process, err := m.Inspector.Process(ctx, pid)
	if err != nil {
		m.cleanupStarted(manager, pid)
		return err
	}
	state := arkeyruntime.State{PID: pid, Executable: process.Executable, ArgsFingerprint: process.ArgsFingerprint, StartTime: process.StartTime, Server: m.Binary, Manager: manager, LogPath: m.LogPath}
	if err = m.Store.Save(ctx, state); err != nil {
		_ = m.stop(ctx, state)
		return err
	}
	for i := 0; i < m.Attempts; i++ {
		catalog, catalogErr := m.Client.Catalog(ctx)
		if catalogErr == nil {
			if route == "" || catalog.HasRoute(route) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = m.stop(cleanupCtx, state)
			cancel()
			return ctx.Err()
		case <-time.After(m.Interval):
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = m.stop(cleanupCtx, state)
	cancel()
	if route == "" {
		return fmt.Errorf("MoonBridge is unavailable; log: %s", m.LogPath)
	}
	return fmt.Errorf("MoonBridge route %s is unavailable; log: %s", route, m.LogPath)
}

func (m *BridgeManager) cleanupStarted(manager string, pid int) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if manager == "systemd" && m.Service != nil {
		_ = m.Service.Stop(cleanupCtx, m.Unit)
		return
	}
	if launcher, ok := m.Launcher.(interface {
		Terminate(context.Context, int) error
	}); ok {
		_ = launcher.Terminate(cleanupCtx, pid)
	}
}

func (m *BridgeManager) owns(ctx context.Context, state arkeyruntime.State) bool {
	process, err := m.Inspector.Process(ctx, state.PID)
	return err == nil && process.Executable == state.Executable && process.ArgsFingerprint == state.ArgsFingerprint && process.StartTime == state.StartTime
}

func (m *BridgeManager) stop(ctx context.Context, state arkeyruntime.State) error {
	if !m.owns(ctx, state) {
		return fmt.Errorf("refusing to stop unrecognized MoonBridge process %d", state.PID)
	}
	if state.Manager == "systemd" && m.Service != nil && m.Service.Available(ctx) {
		return m.Service.Stop(ctx, m.Unit)
	}
	if launcher, ok := m.Launcher.(interface {
		Terminate(context.Context, int) error
	}); ok {
		return launcher.Terminate(ctx, state.PID)
	}
	return errors.New("MoonBridge launcher cannot terminate its direct process")
}

func regularExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
