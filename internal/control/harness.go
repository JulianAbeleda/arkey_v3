package control

import (
	"context"
	"fmt"
	"os"

	"github.com/JulianAbeleda/arkey_v3/internal/app"
	"github.com/JulianAbeleda/arkey_v3/internal/client"
	"github.com/JulianAbeleda/arkey_v3/internal/codex"
	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/crush"
	"github.com/JulianAbeleda/arkey_v3/internal/dsh"
	"github.com/JulianAbeleda/arkey_v3/internal/gateway"
	"github.com/JulianAbeleda/arkey_v3/internal/harness"
	"github.com/JulianAbeleda/arkey_v3/internal/kimi"
)

// harnessStatus is the Harness menu's state per entry: "on", "off", or "on"
// with the reason the checkout cannot serve it yet.
func (s *Services) harnessStatus() map[string]string {
	cfg := s.snapshot()
	state := "off"
	if cfg.Harness.DeepSeekMCP {
		state = "on"
		if ready := dsh.Status(cfg.GUI.DeepSeekHarness); ready != "ready" {
			state = "on · DeepSeek Harness " + ready
		}
	}
	return map[string]string{harness.DeepSeekMCP: state}
}

// ToggleHarness switches a shared harness entry on or off for every later
// TUI launch.
func (s *Services) ToggleHarness(ctx context.Context, name string) (app.Status, error) {
	if name != harness.DeepSeekMCP {
		return app.Status{}, fmt.Errorf("unknown harness %q", name)
	}
	if err := s.updateConfig(func(cfg *config.Config) { cfg.Harness.DeepSeekMCP = !cfg.Harness.DeepSeekMCP }); err != nil {
		return app.Status{}, err
	}
	return s.Refresh(ctx)
}

// PrepareHarness registers the shared harness in the launching client's MCP
// config, or removes it there when it is off or cannot run. It runs after
// PrepareLaunch, so a local route's engine is already up; the gateway in
// front of it is started when missing. On any other route the harness loads
// only while a local engine already answers through the gateway, since the
// gateway serves local engines alone.
func (s *Services) PrepareHarness(ctx context.Context, selectedClient, stateHome string, progress func(string)) error {
	server, err := s.harnessServer(ctx, progress)
	if err != nil {
		return err
	}
	switch selectedClient {
	case client.Codex:
		return codex.SetMCPServer(stateHome, harness.DeepSeekMCP, server)
	case client.Kimi:
		return kimi.SetMCPServer(stateHome, harness.DeepSeekMCP, server)
	case client.Crush:
		return crush.SetMCPServer(stateHome, harness.DeepSeekMCP, server)
	}
	return nil
}

func (s *Services) harnessServer(ctx context.Context, progress func(string)) (*harness.Server, error) {
	cfg := s.snapshot()
	if !cfg.Harness.DeepSeekMCP {
		return nil, nil
	}
	if ready := dsh.Status(cfg.GUI.DeepSeekHarness); ready != "ready" {
		progress(fmt.Sprintf("DeepSeek MCP not loaded: DeepSeek Harness at %s is %s.", cfg.GUI.DeepSeekHarness, ready))
		return nil, nil
	}
	if !gatewayAnswers(ctx) {
		if cfg.Mode != "local" {
			progress("DeepSeek MCP not loaded: no local model is answering through the gateway on " + gateway.DefaultListen + ".")
			return nil, nil
		}
		if err := ensureGateway(ctx); err != nil {
			return nil, fmt.Errorf("DeepSeek MCP gateway: %w", err)
		}
	}
	patch := harness.PatchPath(s.Paths.LocalStateDir())
	window := gatewayContext(ctx)
	if err := dsh.WriteConfig(patch, "http://"+gateway.DefaultListen+"/v1", window); err != nil {
		return nil, fmt.Errorf("DeepSeek MCP patch: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	server := harness.Command(self, cfg.GUI.DeepSeekHarness, patch)
	progress(fmt.Sprintf("DeepSeek MCP loaded: shared harness on the local model (%d-token context).", window))
	return &server, nil
}
