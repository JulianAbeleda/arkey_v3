package codex

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
	"github.com/JulianAbeleda/arkey_v3/internal/client"
)

const MoonBridgeProvider = `model_provider="moonbridge"`

type Plan = client.Plan

type BuildOptions struct {
	Parsed          cli.Options
	Model           string
	Binary          string
	CodexHome       string
	Environment     []string
	ContextWindow   int
	MaxOutputTokens int
}

// Build assembles the codex launch plan. ContextWindow and MaxOutputTokens are
// injected as launch-time -c overrides rather than written into the user's
// config.toml: Arkey must not mutate user config, and the correct value is
// route-dependent, only known once the route for this launch has been selected.
func Build(opts BuildOptions) (Plan, error) {
	if opts.Binary == "" {
		return Plan{}, fmt.Errorf("Codex binary is required")
	}
	if opts.Model == "" && !opts.Parsed.HasModel && !opts.Parsed.PreserveSessionModel {
		return Plan{}, fmt.Errorf("selected model is required")
	}
	args := append([]string(nil), opts.Parsed.ClientArgs...)
	args = ensureExecSkip(args)
	if !opts.Parsed.HasModel && !opts.Parsed.PreserveSessionModel {
		args = append([]string{"-c", "model=" + opts.Model}, args...)
	}
	if !opts.Parsed.HasModelProvider {
		args = append([]string{"-c", MoonBridgeProvider}, args...)
	}
	if opts.ContextWindow > 0 && !hasClientArg(opts.Parsed.ClientArgs, "model_context_window") {
		args = append([]string{"-c", fmt.Sprintf("model_context_window=%d", opts.ContextWindow)}, args...)
	}
	if opts.MaxOutputTokens > 0 && !hasClientArg(opts.Parsed.ClientArgs, "model_max_output_tokens") {
		args = append([]string{"-c", fmt.Sprintf("model_max_output_tokens=%d", opts.MaxOutputTokens)}, args...)
	}
	args = append([]string{"--sandbox", "workspace-write"}, args...)

	env := append([]string(nil), opts.Environment...)
	env = client.SetEnv(env, "CODEX_HOME", opts.CodexHome)
	env = client.SetEnv(env, "CODEX_THREAD_ID", "")
	return Plan{Binary: opts.Binary, Args: args, Env: env}, nil
}

func DefaultBinary(home string) string {
	return filepath.Join(home, ".local", "libexec", "arkey", "clients", "codex", "codex")
}

func DefaultHome(home string) string {
	return filepath.Join(home, ".codex-moonbridge")
}

func hasClientArg(args []string, key string) bool {
	for _, arg := range args {
		if strings.Contains(arg, key) {
			return true
		}
	}
	return false
}

func ensureExecSkip(args []string) []string {
	for _, arg := range args {
		if arg == "--skip-git-repo-check" {
			return args
		}
	}
	for i, arg := range args {
		if arg == "exec" {
			out := make([]string, 0, len(args)+1)
			out = append(out, args[:i+1]...)
			out = append(out, "--skip-git-repo-check")
			out = append(out, args[i+1:]...)
			return out
		}
	}
	return args
}
