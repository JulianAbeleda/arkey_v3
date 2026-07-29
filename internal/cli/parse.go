package cli

import (
	"errors"
	"fmt"
	"strings"
)

var ErrUsage = errors.New("usage error")

type Options struct {
	ForceBoot            bool
	SuppressBoot         bool
	PreserveSessionModel bool
	ClientArgs           []string
	HasModel             bool
	HasModelProvider     bool
	ModelOverride        string
}

func Parse(args []string) (Options, error) {
	var out Options
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--boot":
			out.ForceBoot = true
		case "--no-boot":
			out.SuppressBoot = true
		case "--preserve-session-model":
			out.PreserveSessionModel = true
		default:
			out.ClientArgs = append([]string(nil), args[i:]...)
			i = len(args)
			continue
		}
		i++
	}

	if out.ForceBoot && out.SuppressBoot {
		return Options{}, fmt.Errorf("%w: --boot and --no-boot cannot be used together", ErrUsage)
	}
	if out.ForceBoot && len(out.ClientArgs) > 0 {
		return Options{}, fmt.Errorf("%w: --boot does not accept client arguments", ErrUsage)
	}
	out.HasModel, out.HasModelProvider, out.ModelOverride = inspectOverrides(out.ClientArgs)
	return out, nil
}

func inspectOverrides(args []string) (model, provider bool, modelValue string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-m" || arg == "--model":
			model = true
			if i+1 < len(args) {
				modelValue = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--model="):
			model = true
			modelValue = strings.TrimPrefix(arg, "--model=")
		case arg == "-c" || arg == "--config":
			if i+1 < len(args) {
				i++
				model, provider, modelValue = inspectConfig(args[i], model, provider, modelValue)
			}
		case strings.HasPrefix(arg, "--config="):
			model, provider, modelValue = inspectConfig(strings.TrimPrefix(arg, "--config="), model, provider, modelValue)
		}
	}
	return model, provider, modelValue
}

func inspectConfig(value string, model, provider bool, modelValue string) (bool, bool, string) {
	key, value, ok := strings.Cut(value, "=")
	if !ok {
		return model, provider, modelValue
	}
	switch key {
	case "model":
		model = true
		modelValue = value
	case "model_provider":
		provider = true
	}
	return model, provider, modelValue
}
