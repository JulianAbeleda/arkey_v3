package claude

import (
	"reflect"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/cli"
)

func TestBuildUsesIsolatedStateAndMoonBridge(t *testing.T) {
	plan, err := Build(BuildOptions{Model: "deepseek-v4-pro", Binary: "/bin/true", StateHome: "/tmp/claude-arkey", BridgeURL: "http://127.0.0.1:38440", Environment: []string{"PATH=/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--model", "deepseek-v4-pro"}; !reflect.DeepEqual(plan.Args, want) {
		t.Fatalf("args = %#v, want %#v", plan.Args, want)
	}
	for _, want := range []string{"CLAUDE_CONFIG_DIR=/tmp/claude-arkey", "ANTHROPIC_BASE_URL=http://127.0.0.1:38440", "ANTHROPIC_AUTH_TOKEN=arkey-moonbridge", "ANTHROPIC_API_KEY="} {
		if !contains(plan.Env, want) {
			t.Fatalf("environment missing %q: %#v", want, plan.Env)
		}
	}
}

func TestBuildPreservesNativeModelOverride(t *testing.T) {
	plan, err := Build(BuildOptions{Parsed: cli.Options{ClientArgs: []string{"--model", "custom"}, HasModel: true}, Model: "ignored", Binary: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Args, []string{"--model", "custom"}) {
		t.Fatalf("args = %#v", plan.Args)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
