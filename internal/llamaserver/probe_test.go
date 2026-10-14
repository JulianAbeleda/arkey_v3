package llamaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeServer(healthy bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if !healthy {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"arkey-local"}]}`))
		case "/props":
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":262144},"total_slots":1}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestProbeReadsModelAndContextFromTheServer(t *testing.T) {
	server := fakeServer(true)
	defer server.Close()
	info, err := Probe(context.Background(), server.Client(), server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "arkey-local" || info.ContextSize != 262144 {
		t.Fatalf("info = %#v", info)
	}
}

// vLLM has no /props; its context is the served model's max_model_len.
func TestProbeReadsContextFromModelsWhenThereIsNoProps(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"any-served-name","max_model_len":244608}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vllm.Close()
	info, err := Probe(context.Background(), vllm.Client(), vllm.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "any-served-name" || info.ContextSize != 244608 {
		t.Fatalf("info = %#v", info)
	}
}

func TestProbeRefusesAServerWithNoContextAnywhere(t *testing.T) {
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer bare.Close()
	if _, err := Probe(context.Background(), bare.Client(), bare.URL); err == nil {
		t.Fatal("expected an error: no /props and no max_model_len")
	}
}

func TestProbeRefusesAServerThatIsNotReady(t *testing.T) {
	server := fakeServer(false)
	defer server.Close()
	if _, err := Probe(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("expected an error for health 503")
	}
	if _, err := Probe(context.Background(), server.Client(), "http://127.0.0.1:1"); err == nil {
		t.Fatal("expected an error for a closed port")
	}
}
