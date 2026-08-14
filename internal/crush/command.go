// Package crush builds the launch plan for the Arkey-owned Crush snapshot.
//
// Crush (github.com/charmbracelet/crush) speaks the OpenAI Chat Completions
// API, so it reaches MoonBridge through the /v1/chat/completions ingress rather
// than the Responses ingress that Codex and Kimi use.
package crush

import (
	"fmt"
	"path/filepath"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
	"github.com/JulianAbeleda/arkey_v3/internal/client"
)

// ModelAlias is the provider-scoped fallback name used when no route model has
// been selected yet.
const ModelAlias = "arkey-moonbridge"

// ProviderID is the provider key written into the generated Crush config.
const ProviderID = "moonbridge"

type BuildOptions struct {
	Parsed      cli.Options
	Binary      string
	StateHome   string
	Environment []string
}

// Build returns the launch plan for the Crush snapshot.
//
// Crush has no --model flag: the active model comes from its config file, so
// isolation is a matter of pointing its config and data directories inside the
// Arkey state home. CRUSH_GLOBAL_CONFIG/CRUSH_GLOBAL_DATA are Crush's own
// overrides — narrower than XDG_CONFIG_HOME, which every child process Crush
// spawns (LSP servers, MCP servers, git) would also inherit.
func Build(opts BuildOptions) (client.Plan, error) {
	if opts.Binary == "" {
		return client.Plan{}, fmt.Errorf("Crush snapshot is required")
	}
	if opts.StateHome == "" {
		return client.Plan{}, fmt.Errorf("Crush state home is required")
	}
	args := append([]string(nil), opts.Parsed.ClientArgs...)
	env := append([]string(nil), opts.Environment...)
	env = client.SetEnv(env, "CRUSH_GLOBAL_CONFIG", ConfigDir(opts.StateHome))
	env = client.SetEnv(env, "CRUSH_GLOBAL_DATA", DataDir(opts.StateHome))
	env = client.SetEnv(env, "CRUSH_DISABLE_METRICS", "1")
	// The Arkey harness pins its own provider; Crush must not reach out to the
	// Catwalk provider catalog on start.
	env = client.SetEnv(env, "CRUSH_DISABLE_PROVIDER_AUTO_UPDATE", "1")
	return client.Plan{Binary: opts.Binary, Args: args, Env: env}, nil
}

// ConfigDir is the directory Crush reads crush.json from.
func ConfigDir(stateHome string) string { return filepath.Join(stateHome, "config") }

// DataDir is where Crush keeps sessions, logs and project state.
func DataDir(stateHome string) string { return filepath.Join(stateHome, "data") }

// ConfigPath is the generated Crush config file.
func ConfigPath(stateHome string) string {
	return filepath.Join(ConfigDir(stateHome), "crush.json")
}
