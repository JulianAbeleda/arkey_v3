package kimi

import (
	"fmt"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
	"github.com/JulianAbeleda/arkey_v3/internal/client"
)

const ModelAlias = "arkey-moonbridge"

type BuildOptions struct {
	Parsed      cli.Options
	Binary      string
	StateHome   string
	Environment []string
}

func Build(opts BuildOptions) (client.Plan, error) {
	if opts.Binary == "" {
		return client.Plan{}, fmt.Errorf("Kimi snapshot is required")
	}
	args := append([]string(nil), opts.Parsed.ClientArgs...)
	if !opts.Parsed.HasModel {
		args = append([]string{"--model", ModelAlias}, args...)
	}
	env := append([]string(nil), opts.Environment...)
	env = client.SetEnv(env, "KIMI_CODE_HOME", opts.StateHome)
	env = client.SetEnv(env, "KIMI_CODE_NO_AUTO_UPDATE", "1")
	env = client.SetEnv(env, "KIMI_DISABLE_TELEMETRY", "1")
	return client.Plan{Binary: opts.Binary, Args: args, Env: env}, nil
}
