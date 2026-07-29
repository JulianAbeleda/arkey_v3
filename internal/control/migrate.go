package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

// migrateLegacyRuntime adopts only a process whose persisted PID, executable,
// model, loopback host, and port all match the validated legacy configuration.
func migrateLegacyRuntime(ctx context.Context, paths platform.Paths, cfg config.Config, runner platform.Runner, inspector arkeyruntime.Inspector, store arkeyruntime.Store) error {
	if _, err := store.Load(ctx); err == nil {
		return nil
	} else if !errors.Is(err, arkeyruntime.ErrNoState) {
		return err
	}
	pid, err := readLegacyPID(filepath.Join(paths.LocalStateDir(), "llama.pid"))
	if err != nil {
		return nil
	}
	modelBytes, err := os.ReadFile(filepath.Join(paths.LocalStateDir(), "llama.model"))
	if err != nil {
		return nil
	}
	model := strings.TrimSpace(string(modelBytes))
	if model == "" || model != cfg.Local.Model || !legacyLlamaCommandMatches(pid, cfg) {
		return nil
	}
	process, err := inspector.Process(ctx, pid)
	if err != nil {
		return nil
	}
	server, err := filepath.EvalSymlinks(cfg.Local.LlamaServer)
	if err != nil || process.Executable != server {
		return nil
	}
	manager := "direct"
	if _, err = runner.Run(ctx, "systemctl", "--user", "is-active", "--quiet", "arkey-llama.service"); err == nil {
		manager = "systemd"
	}
	return store.Save(ctx, arkeyruntime.State{
		PID: pid, Executable: process.Executable, ArgsFingerprint: process.ArgsFingerprint,
		Model: model, Port: cfg.Local.Port, StartTime: process.StartTime,
		Server: server, Vendor: cfg.Hardware.Vendor,
		LogPath: filepath.Join(paths.LocalStateDir(), "llama.log"), ContextSize: cfg.Local.ContextSize,
		Manager: manager,
	})
}

func legacyLlamaCommandMatches(pid int, cfg config.Config) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	wants := map[string]string{"--model": cfg.Local.Model, "--host": "127.0.0.1", "--port": strconv.Itoa(cfg.Local.Port)}
	for i := 0; i+1 < len(parts); i++ {
		if expected, ok := wants[parts[i]]; ok && parts[i+1] == expected {
			delete(wants, parts[i])
			i++
		}
	}
	return len(wants) == 0
}

func readLegacyPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		return 0, errors.New("invalid legacy pid")
	}
	return pid, nil
}
