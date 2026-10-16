package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestVLLMCheckpointArgumentsAndOwnedFlags(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"qwen3_5"}`), 0600)
	os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("fixture"), 0600)
	c := Config{Engine: "vllm", Server: "/venv/bin/python", Model: dir, Port: 8000, ContextSize: 32768, Vendor: "nvidia", LogPath: "/logs/vllm", ServedModel: "arkey-local-vllm"}
	if err := ValidateVLLM(c); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(VLLMArgs(c), " ")
	for _, want := range []string{"--model " + dir, "--host 127.0.0.1", "--max-model-len 32768", "--tool-call-parser qwen3_coder"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q: %s", want, args)
		}
	}
	if strings.Contains(args, "--quantization") {
		t.Fatal("quantization must come from checkpoint metadata")
	}
	c.ExtraArgs = []string{"--port=9999"}
	if ValidateVLLM(c) == nil {
		t.Fatal("ownership flag override accepted")
	}
	c.ExtraArgs = []string{"--tool-call-parser", "custom"}
	args = strings.Join(VLLMArgs(c), " ")
	if strings.Count(args, "--tool-call-parser") != 1 {
		t.Fatal("duplicate parser option")
	}
}
func TestVLLMHealthValidatesServedModelAndContext(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"other","max_model_len":32768}]}`, `{"data":[{"id":"arkey-local-vllm","max_model_len":8192}]}`, `{"data":[{"id":"arkey-local-vllm"}]}`, `not json`, `{"data":[{"id":"arkey-local-vllm","max_model_len":32768}]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		u, _ := url.Parse(server.URL)
		port, _ := strconv.Atoi(u.Port())
		ok, _ := (VLLMHealth{Model: "arkey-local-vllm", ContextSize: 32768}).Healthy(context.Background(), port)
		want := strings.Contains(body, `"id":"arkey-local-vllm","max_model_len":32768`)
		server.Close()
		if ok != want {
			t.Fatalf("body %s: got %v", body, ok)
		}
	}
}

func TestVLLMAutoContextDelegatesToEngine(t *testing.T) {
	c := Config{ContextSize: 0}
	if args := strings.Join(VLLMArgs(c), " "); !strings.Contains(args, "--max-model-len auto") {
		t.Fatal(args)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"local","max_model_len":98304}]}`)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	n, err := (VLLMHealth{Model: "local"}).ContextWindow(context.Background(), port)
	if err != nil || n != 98304 {
		t.Fatalf("%d %v", n, err)
	}
}

// Qwen3.8-27B's layout: 24 attention heads, 4 key/value heads, 16 and 48
// linear-attention heads.
const qwen27BConfig = `{"model_type":"qwen3_5","text_config":{"num_attention_heads":24,"num_key_value_heads":4,"linear_num_key_heads":16,"linear_num_value_heads":48}}`

func TestVLLMSplitsAcrossDetectedGPUs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(qwen27BConfig), 0600)
	c := Config{Model: dir, GPUs: 2}
	if args := strings.Join(VLLMArgs(c), " "); !strings.Contains(args, "--tensor-parallel-size 2") {
		t.Fatal(args)
	}
	// Three GPUs divide the 24 attention heads but not the 16 linear key
	// heads, so the split falls back to the largest size that fits: two.
	c.GPUs = 3
	if args := strings.Join(VLLMArgs(c), " "); !strings.Contains(args, "--tensor-parallel-size 2") {
		t.Fatal(args)
	}
	for _, gpus := range []int{0, 1} {
		c.GPUs = gpus
		if args := strings.Join(VLLMArgs(c), " "); strings.Contains(args, "--tensor-parallel-size") {
			t.Fatalf("%d GPUs: %s", gpus, args)
		}
	}
	c.GPUs, c.ExtraArgs = 2, []string{"--tensor-parallel-size=1"}
	if args := strings.Join(VLLMArgs(c), " "); strings.Count(args, "--tensor-parallel-size") != 1 {
		t.Fatal("operator split must replace the automatic one:", args)
	}
}
