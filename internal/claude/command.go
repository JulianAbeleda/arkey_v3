package claude

import (
	"fmt"
	"strings"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
	"github.com/JulianAbeleda/arkey_v3/internal/client"
)

type BuildOptions struct {
	Parsed      cli.Options
	Model       string
	Binary      string
	StateHome   string
	BridgeURL   string
	BridgeToken string
	Environment []string
}

func Build(opts BuildOptions) (client.Plan, error) {
	if opts.Binary == "" {
		return client.Plan{}, fmt.Errorf("Claude snapshot is required")
	}
	if opts.Model == "" && !opts.Parsed.HasModel {
		return client.Plan{}, fmt.Errorf("selected model is required")
	}
	args := append([]string(nil), opts.Parsed.ClientArgs...)
	if !opts.Parsed.HasModel {
		args = append([]string{"--model", opts.Model}, args...)
	}
	env := append([]string(nil), opts.Environment...)
	env = client.SetEnv(env, "CLAUDE_CONFIG_DIR", opts.StateHome)
	env = client.SetEnv(env, "ANTHROPIC_BASE_URL", strings.TrimRight(opts.BridgeURL, "/"))
	env = client.SetEnv(env, "ANTHROPIC_AUTH_TOKEN", tokenOrDefault(opts.BridgeToken))
	env = client.SetEnv(env, "ANTHROPIC_API_KEY", "")
	env = client.SetEnv(env, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	return client.Plan{Binary: opts.Binary, Args: args, Env: env}, nil
}

func tokenOrDefault(token string) string {
	if token != "" {
		return token
	}
	return "arkey-moonbridge"
}
