package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	runtime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

type reloadInspector struct{ runtime.Inspector }

func (reloadInspector) Process(context.Context, int) (runtime.Process, error) {
	return runtime.Process{PID: 42, Executable: "/bridge", StartTime: 1}, nil
}

type reloadService struct {
	runtime.Service
	stopped string
}

func (*reloadService) Available(context.Context) bool              { return true }
func (s *reloadService) Stop(_ context.Context, unit string) error { s.stopped = unit; return nil }

func TestBridgeReloadInitializesUnitBeforeStopping(t *testing.T) {
	for _, unit := range []string{"", "custom-bridge"} {
		t.Run(unit, func(t *testing.T) {
			ctx := context.Background()
			store := runtime.FileStore{Path: filepath.Join(t.TempDir(), "bridge.json")}
			if err := store.Save(ctx, runtime.State{PID: 42, Executable: "/bridge", StartTime: 1, Manager: "systemd"}); err != nil {
				t.Fatal(err)
			}
			service := &reloadService{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":[{"id":"local"}]}`))
			}))
			defer server.Close()
			manager := BridgeManager{Unit: unit, Store: store, Inspector: reloadInspector{}, Service: service, Client: moonbridge.Client{BaseURL: server.URL}}
			if err := manager.Reload(ctx, "local"); err != nil {
				t.Fatal(err)
			}
			want := unit
			if want == "" {
				want = "arkey-moonbridge"
			}
			if service.stopped != want {
				t.Fatalf("stopped unit %q, want %q", service.stopped, want)
			}
		})
	}
}
