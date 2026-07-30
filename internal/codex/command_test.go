package codex

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
)

func TestBuildInjectsRouteAndExecCompatibility(t *testing.T) {
	plan, err := Build(BuildOptions{
		Parsed: cli.Options{ClientArgs: []string{"exec", "test"}},
		Model:  "arkey-local-llama", Binary: "/bin/true", CodexHome: "/tmp/codex", Environment: []string{"PATH=/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--sandbox", "workspace-write", "-c", MoonBridgeProvider, "-c", "model=arkey-local-llama", "exec", "--skip-git-repo-check", "test"}
	if !reflect.DeepEqual(plan.Args, want) {
		t.Fatalf("args = %#v, want %#v", plan.Args, want)
	}
}

func TestBuildInjectsContextWindowAndMaxOutputTokens(t *testing.T) {
	plan, err := Build(BuildOptions{
		Parsed: cli.Options{ClientArgs: []string{"exec", "test"}},
		Model:  "arkey-local-llama", Binary: "/bin/true", ContextWindow: 32768, MaxOutputTokens: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"model_context_window=32768", "model_max_output_tokens=8192"}
	for _, w := range want {
		found := false
		for _, arg := range plan.Args {
			if arg == "-c" {
				continue
			}
			if arg == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected %q in args %#v", w, plan.Args)
		}
	}
}

func TestBuildOmitsZeroContextWindowAndMaxOutputTokens(t *testing.T) {
	plan, err := Build(BuildOptions{
		Parsed: cli.Options{ClientArgs: []string{"exec", "test"}},
		Model:  "arkey-local-llama", Binary: "/bin/true",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range plan.Args {
		if strings.Contains(arg, "model_context_window") || strings.Contains(arg, "model_max_output_tokens") {
			t.Fatalf("unexpected injected argument %q in %#v", arg, plan.Args)
		}
	}
}

func TestBuildPreservesUserSuppliedContextWindow(t *testing.T) {
	parsed, err := cli.Parse([]string{"-c", "model_context_window=99999", "exec", "--skip-git-repo-check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(BuildOptions{Parsed: parsed, Model: "arkey-local-llama", Binary: "/bin/true", ContextWindow: 32768})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, arg := range plan.Args {
		if arg == "model_context_window=99999" {
			found = true
		}
		if arg == "model_context_window=32768" {
			t.Fatalf("Arkey override should not win over user-supplied value, got %#v", plan.Args)
		}
	}
	if !found {
		t.Fatalf("user-supplied model_context_window did not survive: %#v", plan.Args)
	}
}

func TestBuildPreservesExplicitOverrides(t *testing.T) {
	parsed, err := cli.Parse([]string{"--no-boot", "-m", "custom", "-c", "model_provider=other", "exec", "--skip-git-repo-check", "test"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(BuildOptions{Parsed: parsed, Model: "ignored", Binary: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"model=ignored", MoonBridgeProvider} {
		for _, arg := range plan.Args {
			if arg == forbidden {
				t.Fatalf("unexpected injected argument %q in %#v", forbidden, plan.Args)
			}
		}
	}
}
