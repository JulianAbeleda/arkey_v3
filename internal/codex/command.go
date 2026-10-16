package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
	"github.com/JulianAbeleda/arkey_v3/internal/client"
	"github.com/pelletier/go-toml/v2"
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
	ReasoningEffort string
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
	if opts.ReasoningEffort != "" && !hasClientArg(opts.Parsed.ClientArgs, "model_reasoning_effort") {
		args = append([]string{"-c", fmt.Sprintf("model_reasoning_effort=%q", opts.ReasoningEffort)}, args...)
	}
	if !choosesPermissions(opts.Parsed.ClientArgs) && !configChoosesPermissions(opts.CodexHome) {
		args = append([]string{"--sandbox", "workspace-write"}, args...)
	}
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
	// Codex asks terminals that speak the kitty keyboard protocol (kitty,
	// WezTerm, Ghostty, foot) to report key releases. Its list popups act on
	// the release as well as the press, so the Enter that submits
	// /permissions or /model also accepts the popup's first row the instant
	// it opens. Keep the terminal in its legacy key mode unless the operator
	// chose otherwise. Shift+Enter then submits; Ctrl+J still inserts a
	// newline.
	if !hasEnv(env, KeyboardEnhancementEnv) {
		env = client.SetEnv(env, KeyboardEnhancementEnv, "1")
	}
	return Plan{Binary: opts.Binary, Args: args, Env: env}, nil
}

// KeyboardEnhancementEnv disables Codex's kitty keyboard protocol request.
const KeyboardEnhancementEnv = "CODEX_TUI_DISABLE_KEYBOARD_ENHANCEMENT"

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

// permissionFlags select Codex's sandbox or approval mode on the command line.
// A profile counts because its layer may set either.
var permissionFlags = []string{"-s", "--sandbox", "--dangerously-bypass-approvals-and-sandbox", "--approve-for-me", "-p", "--profile"}

// permissionKeys are the config keys that pick a sandbox: the legacy
// sandbox_mode and the permission-profile default_permissions.
var permissionKeys = []string{"sandbox_mode", "default_permissions"}

// choosesPermissions reports whether the operator picked permissions for this
// launch. Arkey's workspace-write sandbox is only a default: a forced
// --sandbox would override the operator's choice and, with
// --dangerously-bypass-approvals-and-sandbox, conflict with it.
func choosesPermissions(args []string) bool {
	for i, arg := range args {
		if arg == "--" {
			return false
		}
		for _, flag := range permissionFlags {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				return true
			}
		}
		if (arg == "-c" || arg == "--config") && i+1 < len(args) && isPermissionKey(args[i+1]) {
			return true
		}
		if value, ok := strings.CutPrefix(arg, "--config="); ok && isPermissionKey(value) {
			return true
		}
	}
	return false
}

func isPermissionKey(override string) bool {
	key, _, _ := strings.Cut(override, "=")
	return slices.Contains(permissionKeys, strings.TrimSpace(key))
}

// configChoosesPermissions reports whether the Codex home's config.toml sets
// a sandbox, so a Full Access choice saved there is not reset each launch.
func configChoosesPermissions(codexHome string) bool {
	if codexHome == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil || len(b) > 1<<20 {
		return false
	}
	cfg := map[string]any{}
	if toml.Unmarshal(b, &cfg) != nil {
		return false
	}
	for _, key := range permissionKeys {
		if _, ok := cfg[key]; ok {
			return true
		}
	}
	return false
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
