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
