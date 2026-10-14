// Package gateway is one stable, llama.cpp-shaped endpoint in front of
// whichever local engine is running. llama.cpp and vLLM take turns on one GPU,
// and clients built against llama.cpp (GameTerm) ask for /props, the llama
// /tokenize body and a model name of their own. The gateway sends every request
// to the first healthy backend and fills in what that backend lacks:
//
//   - /props: forwarded to llama.cpp; for a server without it (vLLM), answered
//     from the served model's max_model_len in /v1/models.
//   - /tokenize and /detokenize: forwarded to llama.cpp; for vLLM, the llama body
//     ({"content"} / {"tokens"}) is translated to vLLM's ({"model","prompt"}) and back.
//   - the "model" of a JSON request: replaced by the model the backend actually
//     serves when the client names one it does not, as llama.cpp would accept it.
//
// Everything else, streams included, is passed through unchanged.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	bodyLimit = 64 << 20
	// a backend's answer is reused this long, so a burst of requests probes once,
	// while an engine swap is noticed within seconds
	probeTTL = 3 * time.Second
)

// Backend is what one probe learned about an engine.
type Backend struct {
	Origin      string
	Models      []string // ids from /v1/models, first is the default
	ContextSize int      // max_model_len, when the server lists it
	Llama       bool     // answers /props: a llama.cpp server
}

// Gateway serves HTTP for the first healthy backend in Backends order. With
// Discover set, the backends are read from it on every probe instead, so a
// change in Arkey's configuration (another engine, another port) takes effect
// without restarting the gateway or touching a client.
type Gateway struct {
	Backends []string
	Discover func() []string
	Client   *http.Client // probes and translated calls; streams use their own transport

	mu      sync.Mutex
	cached  *Backend
	checked time.Time
}

// ErrNoBackend is returned when no backend answers.
var ErrNoBackend = errors.New("gateway: no backend answers")

func (g *Gateway) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Active returns the first backend that answers /v1/models, reusing a recent answer.
func (g *Gateway) Active(ctx context.Context) (Backend, error) {
	g.mu.Lock()
	if g.cached != nil && time.Since(g.checked) < probeTTL {
		b := *g.cached
		g.mu.Unlock()
		return b, nil
	}
	g.mu.Unlock()
	origins := g.Backends
	if g.Discover != nil {
		origins = g.Discover()
	}
	for _, origin := range origins {
		b, err := g.probe(ctx, strings.TrimRight(origin, "/"))
		if err != nil {
			continue
		}
		g.mu.Lock()
		g.cached, g.checked = &b, time.Now()
		g.mu.Unlock()
		return b, nil
	}
	g.forget()
	return Backend{}, ErrNoBackend
}

func (g *Gateway) forget() {
	g.mu.Lock()
	g.cached = nil
	g.mu.Unlock()
}

func (g *Gateway) probe(ctx context.Context, origin string) (Backend, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var listed struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if code, err := g.getJSON(ctx, origin+"/v1/models", &listed); err != nil || code != http.StatusOK {
		return Backend{}, fmt.Errorf("models: %d %v", code, err)
	}
	if len(listed.Data) == 0 {
		return Backend{}, errors.New("no model listed")
	}
	b := Backend{Origin: origin, ContextSize: listed.Data[0].MaxModelLen}
	for _, m := range listed.Data {
		b.Models = append(b.Models, m.ID)
	}
	var props map[string]any
	if code, err := g.getJSON(ctx, origin+"/props", &props); err == nil && code == http.StatusOK {
		b.Llama = true
	}
	return b, nil
}

func (g *Gateway) getJSON(ctx context.Context, url string, into any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	res, err := g.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, bodyLimit))
		return res.StatusCode, nil
	}
	return res.StatusCode, json.NewDecoder(io.LimitReader(res.Body, bodyLimit)).Decode(into)
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, err := g.Active(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"message": err.Error(), "type": "unavailable"}})
		return
	}
	switch {
	case r.URL.Path == "/health" && !b.Llama:
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	case r.URL.Path == "/props" && r.Method == http.MethodGet && !b.Llama:
		writeJSON(w, http.StatusOK, map[string]any{
			"default_generation_settings": map[string]any{"n_ctx": b.ContextSize, "model": b.Models[0]},
			"total_slots":                 1, "model_path": b.Models[0],
		})
	case r.URL.Path == "/tokenize" && r.Method == http.MethodPost && !b.Llama:
		g.tokenize(w, r, b)
	case r.URL.Path == "/detokenize" && r.Method == http.MethodPost && !b.Llama:
		g.detokenize(w, r, b)
	default:
		g.forward(w, r, b)
	}
}

// forward passes the request through, naming the served model when the client
// named another, and flushing every write so a stream arrives as it is made.
func (g *Gateway) forward(w http.ResponseWriter, r *http.Request, b Backend) {
	target, err := url.Parse(b.Origin)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	if r.Body != nil && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		raw, err := io.ReadAll(io.LimitReader(r.Body, bodyLimit))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": err.Error()}})
			return
		}
		raw = withServedModel(raw, b.Models)
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(raw)), int64(len(raw))
		r.Header.Del("Content-Length")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		g.forget() // the backend may have gone: probe again on the next request
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": err.Error(), "type": "backend"}})
	}
	proxy.ServeHTTP(w, r)
}

// withServedModel replaces a JSON body's "model" with the backend's first model
// when the backend does not serve the one named. Other bodies are unchanged.
func withServedModel(raw []byte, served []string) []byte {
	var body map[string]json.RawMessage
	if len(served) == 0 || json.Unmarshal(raw, &body) != nil {
		return raw
	}
	named, ok := body["model"]
	if !ok {
		return raw
	}
	var name string
	if json.Unmarshal(named, &name) == nil {
		for _, s := range served {
			if s == name {
				return raw
			}
		}
	}
	body["model"], _ = json.Marshal(served[0])
	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}

// tokenize answers llama.cpp's {"content", "add_special", "with_pieces"} with
// vLLM's /tokenize ({"model", "prompt", "add_special_tokens", "return_token_strs"}).
func (g *Gateway) tokenize(w http.ResponseWriter, r *http.Request, b Backend) {
	var in struct {
		Content    string `json:"content"`
		AddSpecial *bool  `json:"add_special"`
		WithPieces bool   `json:"with_pieces"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, bodyLimit)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "tokenize: " + err.Error()}})
		return
	}
	addSpecial := false // llama.cpp's default
	if in.AddSpecial != nil {
		addSpecial = *in.AddSpecial
	}
	var out struct {
		Tokens    []int    `json:"tokens"`
		TokenStrs []string `json:"token_strs"`
	}
	code, err := g.postJSON(r.Context(), b.Origin+"/tokenize", map[string]any{
		"model": b.Models[0], "prompt": in.Content, "add_special_tokens": addSpecial, "return_token_strs": in.WithPieces,
	}, &out)
	if err != nil || code != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": fmt.Sprintf("tokenize: backend %d %v", code, err)}})
		return
	}
	if !in.WithPieces {
		writeJSON(w, http.StatusOK, map[string]any{"tokens": orEmpty(out.Tokens)})
		return
	}
	pieces := make([]map[string]any, len(out.Tokens))
	for i, id := range out.Tokens {
		piece := ""
		if i < len(out.TokenStrs) {
			piece = out.TokenStrs[i]
		}
		pieces[i] = map[string]any{"id": id, "piece": piece}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": pieces})
}

// detokenize answers llama.cpp's {"tokens"} -> {"content"} with vLLM's
// {"model", "tokens"} -> {"prompt"}.
func (g *Gateway) detokenize(w http.ResponseWriter, r *http.Request, b Backend) {
	var in struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, bodyLimit)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "detokenize: " + err.Error()}})
		return
	}
	var out struct {
		Prompt string `json:"prompt"`
	}
	code, err := g.postJSON(r.Context(), b.Origin+"/detokenize", map[string]any{"model": b.Models[0], "tokens": orEmpty(in.Tokens)}, &out)
	if err != nil || code != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": fmt.Sprintf("detokenize: backend %d %v", code, err)}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": out.Prompt})
}

func (g *Gateway) postJSON(ctx context.Context, url string, body, into any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := g.client().Do(req)
	if err != nil {
		g.forget()
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, bodyLimit))
		return res.StatusCode, nil
	}
	return res.StatusCode, json.NewDecoder(io.LimitReader(res.Body, bodyLimit)).Decode(into)
}

func orEmpty(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
