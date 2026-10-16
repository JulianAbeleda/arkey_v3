package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (c Config) validate() error {
	if !strings.HasSuffix(strings.ToLower(c.Model), ".gguf") || c.Model == "" {
		return ErrInvalidModel
	}
	if c.Server == "" || c.LogPath == "" || c.Port < 1 || c.Port > 65535 || c.ContextSize < 1 {
		return fmt.Errorf("runtime: invalid configuration")
	}
	if c.Vendor != "nvidia" && c.Vendor != "amd" && c.Vendor != "metal" {
		return fmt.Errorf("runtime: unsupported GPU vendor %q", c.Vendor)
	}
	return nil
}

// llamaArgs builds the llama-server command line.
//
// --parallel 1 is deliberate: llama-server's auto slot count (n_parallel = 4
// with kv_unified) segfaults on load for qwen35-arch models such as
// Qwen3.6-27B. Arkey serves one client at a time, so a single slot costs
// nothing and keeps the runtime from crashing on those models. Multiple slots
// also make llama-server reassign turns by prompt similarity, which invalidates
// context checkpoints and eventually fails to restore the KV cache mid-session.
//
// KVCacheType halves bytes-per-token against f16, which buys twice the context
// for the same VRAM at a quality cost small enough to be invisible in practice.
// Context length is the binding constraint for agent sessions, so that is the
// right trade. KVCacheBytesPerElement must describe KVCacheType: context sizing
// is computed from it, so the two drifting apart would size the window against
// a cache the server is not actually running.
const (
	KVCacheType            = "q8_0"
	KVCacheBytesPerElement = 1
)

// Current llama builds emit ggml GPU initialization at trace level (4).
// Preserve acceleration verification without enabling debug-level logging.
func llamaArgs(c Config) []string {
	args := []string{c.Server, "--model", c.Model, "--alias", "arkey-local", "--host", "127.0.0.1", "--port", fmt.Sprint(c.Port), "--ctx-size", fmt.Sprint(c.ContextSize), "--gpu-layers", "all", "--parallel", "1", "--cache-type-k", KVCacheType, "--cache-type-v", KVCacheType, "--log-verbosity", "4"}
	args = append(args, "--cache-ram", "0", "--metrics")
	if c.SlotsPath != "" {
		args = append(args, "--slot-save-path", c.SlotsPath)
	}
	if c.ChatTemplate != "" {
		args = append(args, "--chat-template-file", c.ChatTemplate)
	}
	return append(args, llamaDevices(c)...)
}

// llamaDevices pins llama-server to the first GPUs devices. Its default
// already splits layers across every visible GPU; naming them keeps the split
// matched to the count context sizing assumed, including a cap below the
// number installed.
func llamaDevices(c Config) []string {
	prefix := map[string]string{"amd": "ROCm", "nvidia": "CUDA"}[c.Vendor]
	if c.GPUs < 1 || prefix == "" {
		return nil
	}
	devices := make([]string, c.GPUs)
	for i := range devices {
		devices[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return []string{"--device", strings.Join(devices, ",")}
}

func recognizeLlama(ctx context.Context, inspector Inspector, pid int, base State) (Process, string, error) {
	i, ok := inspector.(LlamaInspector)
	if !ok {
		return Process{}, "", errors.New("runtime: no llama process recognizer")
	}
	return i.LlamaProcess(ctx, pid, base.Server, base.Port)
}
