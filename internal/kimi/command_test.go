package kimi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAndWriteConfigUseIsolatedState(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("theme = 'light'\n[loop_control]\nmax_steps_per_turn = 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(home, "http://127.0.0.1:38440", "", "deepseek-v4-pro", 65536); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`default_model = 'arkey-moonbridge'`, `theme = 'light'`, `max_steps_per_turn = 42`, `type = 'openai_responses'`, `base_url = 'http://127.0.0.1:38440/v1'`, `model = 'deepseek-v4-pro'`, `max_context_size = 65536`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated config missing %q:\n%s", want, data)
		}
	}
	plan, err := Build(BuildOptions{Binary: "/bin/true", StateHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Args) != 2 || plan.Args[1] != ModelAlias {
		t.Fatalf("args = %#v", plan.Args)
	}
}
