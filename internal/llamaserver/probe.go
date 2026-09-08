// Package llamaserver asks a llama.cpp server what it is: healthy, which
// model it serves, and how much context that model has. Arkey's server route
// runs on these answers rather than on typed numbers.
package llamaserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Info is what one probe learned.
type Info struct {
	Model       string
	ContextSize int
}

const bodyLimit = 1 << 20

// Probe checks /health, reads the first served model from /v1/models, and
// the per-slot context from /props. Any missing answer is an error.
func Probe(ctx context.Context, client *http.Client, origin string) (Info, error) {
	origin = strings.TrimRight(origin, "/")
	if client == nil {
		client = http.DefaultClient
	}
	if code, _, err := get(ctx, client, origin+"/health"); err != nil {
		return Info{}, fmt.Errorf("server is not reachable: %w", err)
	} else if code != http.StatusOK {
		return Info{}, fmt.Errorf("server is not ready (health %d)", code)
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, client, origin+"/v1/models", &models); err != nil {
		return Info{}, fmt.Errorf("server model list: %w", err)
	}
	if len(models.Data) == 0 || strings.TrimSpace(models.Data[0].ID) == "" {
		return Info{}, errors.New("server lists no model")
	}
	var props struct {
		Defaults struct {
			ContextSize int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := getJSON(ctx, client, origin+"/props", &props); err != nil {
		return Info{}, fmt.Errorf("server props: %w", err)
	}
	if props.Defaults.ContextSize <= 0 {
		return Info{}, errors.New("server reports no context size")
	}
	return Info{Model: models.Data[0].ID, ContextSize: props.Defaults.ContextSize}, nil
}

func get(ctx context.Context, client *http.Client, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	return resp.StatusCode, body, err
}

func getJSON(ctx context.Context, client *http.Client, url string, into any) error {
	code, body, err := get(ctx, client, url)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("status %d", code)
	}
	return json.Unmarshal(body, into)
}
