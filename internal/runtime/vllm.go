package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JulianAbeleda/arkey_v3/internal/models"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ValidateVLLM accepts a local Hugging Face checkpoint directory. Quantization
// is read by vLLM from the checkpoint, not inferred from its name.
func ValidateVLLM(c Config) error {
	if c.Server == "" || c.LogPath == "" || c.Port < 1 || c.Port > 65535 || (c.ContextSize != 0 && c.ContextSize < 4096) || c.ServedModel == "" {
		return errors.New("runtime: incomplete vLLM configuration")
	}
	if c.Vendor != "nvidia" && c.Vendor != "amd" {
		return errors.New("runtime: vLLM requires an NVIDIA or AMD GPU")
	}
	if _, err := models.HFModelType(c.Model); err != nil {
		return err
	}
	reserved := map[string]bool{"--model": true, "--host": true, "--port": true, "--served-model-name": true, "--max-model-len": true, "-m": true}
	for _, arg := range c.ExtraArgs {
		key, _, _ := strings.Cut(arg, "=")
		if reserved[key] {
			return fmt.Errorf("runtime: vLLM option %s is managed by Arkey", key)
		}
	}
	return nil
}

func VLLMArgs(c Config) []string {
	length := strconv.Itoa(c.ContextSize)
	if c.ContextSize == 0 {
		length = "auto"
	}
	args := []string{c.Server, "-m", "vllm.entrypoints.openai.api_server", "--model", c.Model, "--host", "127.0.0.1", "--port", strconv.Itoa(c.Port), "--served-model-name", c.ServedModel, "--max-model-len", length, "--language-model-only", "--max-num-seqs", "1", "--max-num-batched-tokens", "2048", "--gpu-memory-utilization", "0.90"}
	kind, _ := models.HFModelType(c.Model)
	// The parser is a model-family capability, shared across AWQ/NVFP4 checkpoints.
	if strings.HasPrefix(kind, "qwen3_5") || strings.HasPrefix(kind, "qwen3_8") {
		if !hasOption(c.ExtraArgs, "--tool-call-parser") {
			args = append(args, "--enable-auto-tool-choice", "--tool-call-parser", "qwen3_coder")
		}
		if !hasOption(c.ExtraArgs, "--reasoning-parser") {
			args = append(args, "--reasoning-parser", "qwen3")
		}
		if !hasOption(c.ExtraArgs, "--default-chat-template-kwargs") {
			args = append(args, "--default-chat-template-kwargs", `{"enable_thinking":false}`)
		}
	}
	// Split across devices unless the operator chose a split in vllm_args.
	if !hasOption(c.ExtraArgs, "--tensor-parallel-size") && !hasOption(c.ExtraArgs, "-tp") {
		if n := models.TensorParallelSize(c.Model, c.GPUs); n > 1 {
			args = append(args, "--tensor-parallel-size", strconv.Itoa(n))
		}
	}
	return append(args, c.ExtraArgs...)
}
func hasOption(args []string, key string) bool {
	for _, a := range args {
		if a == key || strings.HasPrefix(a, key+"=") {
			return true
		}
	}
	return false
}

type VLLMBackend struct{ Runner CommandRunner }

func (b VLLMBackend) Aligned(ctx context.Context, python, vendor string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := b.Runner.Run(ctx, python, "-c", `import importlib.util,torch; assert importlib.util.find_spec('vllm') is not None; assert torch.cuda.is_available(); print('ARKEY_GPU_AMD' if torch.version.hip else 'ARKEY_GPU_NVIDIA')`)
	if err != nil {
		return false, fmt.Errorf("runtime: vLLM Python/GPU check failed: %w", err)
	}
	return strings.Contains(string(out), "ARKEY_GPU_"+strings.ToUpper(vendor)), nil
}
func (VLLMBackend) Accelerated(_ context.Context, path, vendor string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(b)
	return strings.Contains(text, "device_config=cuda") && strings.Contains(text, "Model loading took"), nil
}

type VLLMHealth struct {
	Model       string
	ContextSize int
}

func (h VLLMHealth) ContextWindow(ctx context.Context, port int) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/v1/models", port), nil)
	if err != nil {
		return 0, err
	}
	res, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, nil
	}
	var catalog struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&catalog); err != nil {
		return 0, err
	}
	for _, m := range catalog.Data {
		if m.ID == h.Model && m.MaxModelLen >= max(4096, h.ContextSize) {
			return m.MaxModelLen, nil
		}
	}
	return 0, nil
}

func (h VLLMHealth) Healthy(ctx context.Context, port int) (bool, error) {
	n, err := h.ContextWindow(ctx, port)
	return n > 0 && err == nil, err
}
