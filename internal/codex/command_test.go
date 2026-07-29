package codex

import (
	"reflect"
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
