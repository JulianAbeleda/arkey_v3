package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/config"
)

// fakeVLLM serves "served-27b" only, has no /props, and speaks vLLM's tokenize.
func fakeVLLM(t *testing.T, gotModel *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"served-27b","max_model_len":131072}]}`))
		case "/tokenize":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["model"] != "served-27b" || in["prompt"] != "hello world" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"count":2,"max_model_len":131072,"tokens":[11,22],"token_strs":["hello"," world"]}`))
		case "/detokenize":
			_, _ = w.Write([]byte(`{"prompt":"hello world"}`))
		case "/v1/chat/completions":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			*gotModel, _ = in["model"].(string)
			if *gotModel != "served-27b" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\ndata: [DONE]\n\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// fakeLlama serves anything and answers /props and llama's tokenize itself.
func fakeLlama() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"arkey-local"}]}`))
		case "/props":
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":262144},"total_slots":1}`))
		case "/tokenize":
			_, _ = w.Write([]byte(`{"tokens":[1,2,3]}`))
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func call(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	raw := rec.Body.String()
	var out map[string]any
	_ = json.Unmarshal([]byte(raw), &out)
	return rec.Code, out, raw
}

func TestALlamaClientWorksAgainstVLLMThroughTheGateway(t *testing.T) {
	var gotModel string
	vllm := fakeVLLM(t, &gotModel)
	defer vllm.Close()
	g := &Gateway{Backends: []string{"http://127.0.0.1:1", vllm.URL}} // llama.cpp is off: vLLM answers
	if code, out, _ := call(t, g, http.MethodGet, "/health", ""); code != 200 || out["status"] != "ok" {
		t.Fatalf("health %d %v", code, out)
	}
	code, out, _ := call(t, g, http.MethodGet, "/props", "")
	ctx := out["default_generation_settings"].(map[string]any)["n_ctx"]
	if code != 200 || ctx != float64(131072) {
		t.Fatalf("props %d %v", code, out)
	}
	if code, out, _ = call(t, g, http.MethodPost, "/tokenize", `{"content":"hello world","add_special":false}`); code != 200 || len(out["tokens"].([]any)) != 2 {
		t.Fatalf("tokenize %d %v", code, out)
	}
	_, out, _ = call(t, g, http.MethodPost, "/tokenize", `{"content":"hello world","with_pieces":true}`)
	if first := out["tokens"].([]any)[0].(map[string]any); first["id"] != float64(11) || first["piece"] != "hello" {
		t.Fatalf("pieces %v", out)
	}
	if _, out, _ = call(t, g, http.MethodPost, "/detokenize", `{"tokens":[11,22]}`); out["content"] != "hello world" {
		t.Fatalf("detokenize %v", out)
	}
	// the client names its own model; the backend serves another: the request still lands
	code, _, raw := call(t, g, http.MethodPost, "/v1/chat/completions", `{"model":"qwen3.5-4b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || gotModel != "served-27b" || !strings.Contains(raw, "pong") {
		t.Fatalf("chat %d model=%q body=%q", code, gotModel, raw)
	}
}

func TestLlamaIsPreferredAndForwardedUnchangedWhenItRuns(t *testing.T) {
	llama := fakeLlama()
	defer llama.Close()
	var unused string
	vllm := fakeVLLM(t, &unused)
	defer vllm.Close()
	g := &Gateway{Backends: []string{llama.URL, vllm.URL}}
	b, err := g.Active(context.Background())
	if err != nil || b.Origin != llama.URL || !b.Llama {
		t.Fatalf("active %#v %v", b, err)
	}
	if code, out, _ := call(t, g, http.MethodPost, "/tokenize", `{"content":"x"}`); code != 200 || len(out["tokens"].([]any)) != 3 {
		t.Fatalf("llama tokenize must be forwarded: %d %v", code, out)
	}
	if code, out, _ := call(t, g, http.MethodGet, "/props", ""); code != 200 || out["total_slots"] != float64(1) {
		t.Fatalf("llama props must be forwarded: %d %v", code, out)
	}
}

func TestNoBackendIsServiceUnavailable(t *testing.T) {
	g := &Gateway{Backends: []string{"http://127.0.0.1:1"}}
	if code, _, _ := call(t, g, http.MethodGet, "/health", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", code)
	}
}

func TestDiscoverFollowsTheConfigurationWithoutARestart(t *testing.T) {
	llama := fakeLlama()
	defer llama.Close()
	var unused string
	vllm := fakeVLLM(t, &unused)
	defer vllm.Close()
	current := []string{vllm.URL}
	g := &Gateway{Discover: func() []string { return current }}
	if b, _ := g.Active(context.Background()); b.Origin != vllm.URL {
		t.Fatalf("active %s", b.Origin)
	}
	current = []string{llama.URL}
	g.forget() // the probe cache expires on its own; skip the wait
	if b, _ := g.Active(context.Background()); b.Origin != llama.URL {
		t.Fatalf("after the switch: %s", b.Origin)
	}
}

func TestOriginsPutTheSelectedRuntimeFirst(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Local.Port, cfg.Local.VLLMURL = 8080, "http://127.0.0.1:8000/"
	cfg.Local.Runtime = "vllm"
	if got := Origins(cfg); strings.Join(got, " ") != "http://127.0.0.1:8000 http://127.0.0.1:8080" {
		t.Fatalf("vllm first: %v", got)
	}
	cfg.Local.Runtime = "llama"
	if got := Origins(cfg); got[0] != "http://127.0.0.1:8080" {
		t.Fatalf("llama first: %v", got)
	}
}

func TestWithServedModelKeepsAServedNameAndOtherBodies(t *testing.T) {
	served := []string{"a", "b"}
	if got := withServedModel([]byte(`{"model":"b"}`), served); !bytes.Equal(got, []byte(`{"model":"b"}`)) {
		t.Fatalf("%s", got)
	}
	if got := withServedModel([]byte(`not json`), served); string(got) != "not json" {
		t.Fatalf("%s", got)
	}
	var body map[string]any
	_ = json.Unmarshal(withServedModel([]byte(`{"model":"zzz","x":1}`), served), &body)
	if body["model"] != "a" || body["x"] != float64(1) {
		t.Fatalf("%v", body)
	}
}
