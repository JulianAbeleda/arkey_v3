package control

import (
	"context"
	"fmt"

	"github.com/JulianAbeleda/arkey_v3/internal/config"
	"github.com/JulianAbeleda/arkey_v3/internal/moonbridge"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

func (s *Services) ClientReasoningEffortFor(model string) string {
	cfg := s.configForRoute(model)
	if cfg.Mode == "local" && cfg.Local.Runtime == "vllm" {
		return cfg.Local.VLLMReasoningEffort
	}
	return ""
}

// startLocal records the engine's measured context before configuring clients
// and projects the same window into the local MoonBridge route.
func (s *Services) startLocal(ctx context.Context, cfg config.Config) (arkeyruntime.State, error, error) {
	state, rollback, err := s.Runtime.Start(ctx, s.runtimeConfig(ctx, cfg))
	if err != nil || cfg.Local.Runtime != "vllm" {
		return state, rollback, err
	}
	window := state.ContextSize
	if window == 0 {
		window = cfg.Local.VLLMContextSize
	}
	if window < 4096 {
		return state, nil, fmt.Errorf("vLLM did not report its context window")
	}
	s.mu.Lock()
	s.resolvedVLLMContext = window
	s.mu.Unlock()
	if s.Bridge != nil {
		changed, err := moonbridge.SetVLLMRoute(s.Bridge.Config, cfg.Local.VLLMURL, cfg.Local.VLLMModel, window)
		if err != nil {
			return state, nil, err
		}
		if changed {
			if err = s.Bridge.Reload(ctx, "arkey-local-vllm"); err != nil {
				return state, nil, err
			}
		}
	}
	return state, nil, nil
}
