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
	args = configScope(args)

	env := append([]string(nil), opts.Environment...)
	env = client.SetEnv(env, "CODEX_HOME", opts.CodexHome)
	env = client.SetEnv(env, "CODEX_THREAD_ID", "")
	// The MoonBridge provider table names ARKEY_MOONBRIDGE_TOKEN as its
	// env_key, and Codex refuses to start when a named key is absent. Kimi
	// and Crush get the same default in their config; Codex gets it here.
	if !hasEnv(env, "ARKEY_MOONBRIDGE_TOKEN") {
		env = client.SetEnv(env, "ARKEY_MOONBRIDGE_TOKEN", DefaultBridgeToken)
	}
	return Plan{Binary: opts.Binary, Args: args, Env: env}, nil
}

// DefaultBridgeToken is what every Arkey client presents to MoonBridge
// when nobody set one.
const DefaultBridgeToken = "arkey-moonbridge"

func hasEnv(env []string, key string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") && len(entry) > len(key)+1 {
			return true
		}
	}
	return false
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

// Codex exec has its own -c collection. Supplying even one exec-level override
// can discard root-level overrides. Keep all configuration together at exec
// scope, with generated defaults before explicit user values.
func configScope(args []string) []string {
	var configs, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if (arg == "-c" || arg == "--config") && i+1 < len(args) {
			configs = append(configs, "-c", args[i+1])
			i++
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--config="); ok {
			configs = append(configs, "-c", value)
			continue
		}
		rest = append(rest, arg)
	}
	// The first positional is the command (or the interactive prompt). Skip
	// values of the root options that can precede it; never inspect prompt text.
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--":
			return args
		case "-m", "--model", "-s", "--sandbox", "-p", "--profile", "-C", "--cd", "-i", "--image", "--enable", "--disable", "-a", "--ask-for-approval", "--add-dir", "--local-provider":
			i++
			continue
		}
		if strings.HasPrefix(rest[i], "-") {
			continue
		}
		if rest[i] != "exec" {
			return args
		}
		at := i + 1
		out := append([]string(nil), rest[:at]...)
		out = append(out, configs...)
		return append(out, rest[at:]...)
	}
	return args
}
