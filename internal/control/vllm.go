package control

func (s *Services) ClientReasoningEffortFor(model string) string {
	cfg := s.configForRoute(model)
	if cfg.Mode == "local" && cfg.Local.Runtime == "vllm" {
		return cfg.Local.VLLMReasoningEffort
	}
	return ""
}
