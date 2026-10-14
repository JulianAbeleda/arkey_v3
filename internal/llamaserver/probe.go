// Package llamaserver asks an OpenAI-compatible server (llama.cpp, vLLM) what it
// is: healthy, which model it serves, and how much context that model has.
// Arkey's server route runs on these answers rather than on typed numbers.
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

// Probe checks /health and reads the first served model from /v1/models. The
// context comes from llama.cpp's /props (per slot) or, for a server without
// /props such as vLLM, from the model's max_model_len in /v1/models. A server
// that answers neither has no known context, which is an error.
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
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
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
	contextSize := 0
	propsErr := getJSON(ctx, client, origin+"/props", &props)
	if propsErr == nil {
		contextSize = props.Defaults.ContextSize
	}
	if contextSize <= 0 {
		contextSize = models.Data[0].MaxModelLen
	}
	if contextSize <= 0 {
		if propsErr != nil {
			return Info{}, fmt.Errorf("server reports no context size (no /props: %v, no max_model_len)", propsErr)
		}
		return Info{}, errors.New("server reports no context size")
	}
	return Info{Model: models.Data[0].ID, ContextSize: contextSize}, nil
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
