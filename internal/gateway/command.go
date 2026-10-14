package gateway

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

// DefaultListen is where the gateway answers: one fixed local port that the
// tailnet (or any client on the box) can be pointed at once, whatever engine runs.
const DefaultListen = "127.0.0.1:8090"

// Origins are the local engines Arkey's configuration names, the selected
// runtime first: llama.cpp at 127.0.0.1:<local.port>, vLLM at local.vllm_url.
func Origins(cfg config.Config) []string {
	llama := ""
	if cfg.Local.Port > 0 {
		llama = fmt.Sprintf("http://127.0.0.1:%d", cfg.Local.Port)
	}
	vllm := strings.TrimRight(cfg.Local.VLLMURL, "/")
	order := []string{llama, vllm}
	if cfg.Local.Runtime == "vllm" {
		order = []string{vllm, llama}
	}
	var out []string
	for _, o := range order {
		if o != "" && !contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Main runs `arkey gateway [--listen ADDR] [--backend ORIGIN ...]`. Without
// --backend the engines come from Arkey's configuration, re-read on every probe.
func Main(args []string, home string, stderr io.Writer) int {
	fs := flag.NewFlagSet("arkey gateway", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", DefaultListen, "address to answer on")
	var backends multi
	fs.Var(&backends, "backend", "engine origin, in preference order (repeatable); default: Arkey's configured engines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	g := &Gateway{Backends: backends}
	if len(backends) == 0 {
		store := config.Store{Path: platform.DefaultPaths(home).ConfigFile(), Home: home}
		if _, err := store.Load(); err != nil {
			fmt.Fprintln(stderr, "Arkey gateway: read the configuration:", err)
			return 1
		}
		var last []string
		g.Discover = func() []string {
			if cfg, err := store.Load(); err == nil {
				last = Origins(cfg)
			}
			return last // a configuration being rewritten keeps the previous answer
		}
	}
	server := &http.Server{Addr: *listen, Handler: g, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Fprintf(stderr, "Arkey gateway on http://%s\n", *listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, "Arkey gateway:", err)
		return 1
	}
	return 0
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, strings.TrimRight(v, "/")); return nil }
