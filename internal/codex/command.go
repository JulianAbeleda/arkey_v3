package codex

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
)

const MoonBridgeProvider = `model_provider="moonbridge"`

type Plan struct {
	Binary string
	Args   []string
	Env    []string
}

type BuildOptions struct {
	Parsed      cli.Options
	Model       string
	Binary      string
	CodexHome   string
	Environment []string
}

func Build(opts BuildOptions) (Plan, error) {
	if opts.Binary == "" {
		return Plan{}, fmt.Errorf("Codex binary is required")
	}
	if opts.Model == "" && !opts.Parsed.HasModel && !opts.Parsed.PreserveSessionModel {
		return Plan{}, fmt.Errorf("selected model is required")
	}
	args := append([]string(nil), opts.Parsed.CodexArgs...)
	args = ensureExecSkip(args)
	if !opts.Parsed.HasModel && !opts.Parsed.PreserveSessionModel {
		args = append([]string{"-c", "model=" + opts.Model}, args...)
	}
	if !opts.Parsed.HasModelProvider {
		args = append([]string{"-c", MoonBridgeProvider}, args...)
	}
	args = append([]string{"--sandbox", "workspace-write"}, args...)

	env := append([]string(nil), opts.Environment...)
	env = setEnv(env, "CODEX_HOME", opts.CodexHome)
	env = setEnv(env, "CODEX_THREAD_ID", "")
	return Plan{Binary: opts.Binary, Args: args, Env: env}, nil
}

func DefaultBinary(home string) string {
	return filepath.Join(home, ".local", "bin", "codex-openai")
}

func DefaultHome(home string) string {
	return filepath.Join(home, ".codex-moonbridge")
}

func (p Plan) Validate() error {
	info, err := os.Stat(p.Binary)
	if err != nil {
		return fmt.Errorf("Arkey Codex binary: %w", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("Arkey Codex binary is not executable: %s", p.Binary)
	}
	return nil
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

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i := range env {
		if len(env[i]) >= len(prefix) && env[i][:len(prefix)] == prefix {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
