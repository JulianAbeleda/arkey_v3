package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/app"
	"github.com/JulianAbeleda/arkey_v3/internal/config"
)

// vLLM is an externally managed local server. Arkey never adopts or kills it.
func (s *Services) checkVLLM(ctx context.Context, cfg config.Config) error {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(cfg.Local.VLLMURL, "/")+"/v1/models", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("vLLM unavailable at %s: %w", cfg.Local.VLLMURL, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("vLLM model lookup: %s", res.Status)
	}
	var catalog struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&catalog); err != nil {
		return fmt.Errorf("vLLM model lookup: %w", err)
	}
	for _, model := range catalog.Data {
		if model.ID == cfg.Local.VLLMModel {
			if model.MaxModelLen < cfg.Local.VLLMContextSize {
				return fmt.Errorf("vLLM reports context %d; configured vllm_context_size is %d", model.MaxModelLen, cfg.Local.VLLMContextSize)
			}
			return nil
		}
	}
	return fmt.Errorf("vLLM does not serve configured model %q", cfg.Local.VLLMModel)
}

func (s *Services) activateVLLM(ctx context.Context) (app.Status, error) {
	cfg := s.snapshot()
	if err := s.checkVLLM(ctx, cfg); err != nil {
		return app.Status{}, err
	}
	if err := s.Bridge.EnsureRoute(ctx, "arkey-local-vllm"); err != nil {
		return app.Status{}, err
	}
	if regularFile(s.ModelCatalog) {
		if err := s.updateLocalCatalog(ctx); err != nil {
			return app.Status{}, err
		}
	}
	if err := s.updateConfig(func(c *config.Config) { c.Mode = "local"; c.Local.Runtime = "vllm" }); err != nil {
		return app.Status{}, err
	}
	return s.Refresh(ctx)
}

func (s *Services) ClientReasoningEffort() string {
	cfg := s.snapshot()
	if cfg.Mode == "local" && cfg.Local.Runtime == "vllm" {
		return cfg.Local.VLLMReasoningEffort
	}
	return ""
}
