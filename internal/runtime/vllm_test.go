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
