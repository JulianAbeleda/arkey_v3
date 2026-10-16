package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/dsh"
	"github.com/JulianAbeleda/arkey_v3/internal/gateway"
)

// GUIDeepSeekHarness is the GUI menu's DeepSeek Harness entry.
const GUIDeepSeekHarness = "deepseek-harness"

// gatewayUnit is the user service the gateway runs as once a GUI needs it.
const gatewayUnit = "arkey-gateway"

// dshDefaultPort is where `dsh web` listens unless told otherwise.
const dshDefaultPort = "127.0.0.1:3080"

func (s *Services) guiStatus() map[string]string {
	return map[string]string{GUIDeepSeekHarness: dsh.Status(s.snapshot().GUI.DeepSeekHarness)}
}

// PrepareGUI readies everything DeepSeek Harness's browser UI talks to and
// returns the exec that serves it: the selected local engine is loaded (a
// running, matching one is reused), the OpenAI-compatible gateway answers in
// front of it, and DeepSeek Harness's web profile names the gateway as its
// arkey-local provider, sized to the engine's context window.
func (s *Services) PrepareGUI(ctx context.Context, progress func(string)) (dsh.Plan, error) {
	cfg := s.snapshot()
	if status := dsh.Status(cfg.GUI.DeepSeekHarness); status != "ready" {
		return dsh.Plan{}, fmt.Errorf("DeepSeek Harness at %s is %s", cfg.GUI.DeepSeekHarness, status)
	}
	local := cfg
	local.Mode = "local"
	if (local.Local.Runtime == "vllm" && local.Local.VLLMCheckpoint == "") || (local.Local.Runtime != "vllm" && local.Local.Model == "") {
		return dsh.Plan{}, errors.New("no local model is selected; choose one in Config → Local first")
	}
	progress("Loading the local " + local.Local.Runtime + " engine (reused when already running)…")
	if _, rollback, err := s.startLocal(ctx, local); err != nil {
		if rollback != nil {
			return dsh.Plan{}, fmt.Errorf("local engine: %w; rollback: %v", err, rollback)
		}
		return dsh.Plan{}, fmt.Errorf("local engine: %w", err)
	}
	progress("Starting the OpenAI-compatible gateway on http://" + gateway.DefaultListen + "…")
	if err := ensureGateway(ctx); err != nil {
		return dsh.Plan{}, err
	}
	window := gatewayContext(ctx)
	if err := dsh.WriteConfig(dsh.PatchPath(dsh.Home(s.Paths.Home)), "http://"+gateway.DefaultListen+"/v1", window); err != nil {
		return dsh.Plan{}, err
	}
	progress(fmt.Sprintf("DeepSeek Harness → arkey-local provider (%d-token context). Starting the web UI…", window))
	return dsh.Build(cfg.GUI.DeepSeekHarness, os.Environ(), portTaken(dshDefaultPort))
}

func gatewayAnswers(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+gateway.DefaultListen+"/v1/models", nil)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// ensureGateway starts `arkey gateway` as a user service unless something
// already answers on its port. The service outlives this terminal, so the
// endpoint stays up for any other OpenAI client too.
func ensureGateway(ctx context.Context) error {
	if gatewayAnswers(ctx) {
		return nil
	}
	if portTaken(gateway.DefaultListen) {
		return fmt.Errorf("%s is taken by something that is not answering as the Arkey gateway", gateway.DefaultListen)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// A unit left failed by an earlier run blocks reusing its name.
	_ = exec.CommandContext(ctx, "systemctl", "--user", "reset-failed", gatewayUnit).Run()
	out, err := exec.CommandContext(ctx, "systemd-run", "--user", "--unit="+gatewayUnit, "--collect",
		"--property=Restart=on-failure", "--property=RestartSec=2", self, "gateway").CombinedOutput()
	if err != nil {
		return fmt.Errorf("start gateway service: %v: %s", err, out)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if gatewayAnswers(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("the gateway started but did not answer on " + gateway.DefaultListen + "; see `journalctl --user -u " + gatewayUnit + "`")
}

// gatewayContext reads the served context window through the gateway's /props,
// which it answers for vLLM as well as llama.cpp. Zero means unknown.
func gatewayContext(ctx context.Context) int {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+gateway.DefaultListen+"/props", nil)
	if err != nil {
		return 0
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	var props struct {
		Settings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if json.NewDecoder(res.Body).Decode(&props) != nil {
		return 0
	}
	return props.Settings.NCtx
}

func portTaken(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
