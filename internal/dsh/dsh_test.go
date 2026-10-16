package dsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type patchEntry struct {
	ID     string         `yaml:"id"`
	Config map[string]any `yaml:"config"`
}

func readPatch(t *testing.T, path string) map[string]patchEntry {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var list []patchEntry
	if err := yaml.Unmarshal(b, &list); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	out := map[string]patchEntry{}
	for _, e := range list {
		out[e.ID] = e
	}
	return out
}

// The file DeepSeek Harness generates holds a comment header and `[]`.
func TestWriteConfigFillsTheGeneratedEmptyPatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles", "web", "cordis.patch.yml")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("# Your patch layer for this dsh profile.\n[]\n"), 0o600)
	if err := WriteConfig(path, "http://127.0.0.1:8090/v1", 262144); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# Your patch layer") {
		t.Fatalf("header comment lost:\n%s", b)
	}
	p := readPatch(t, path)
	r := p["llm-pi-ai"].Config["providers"].(map[string]any)[Provider].(map[string]any)
	if r["api"] != "openai-completions" || r["baseURL"] != "http://127.0.0.1:8090/v1" || r["apiKeyEnv"] != KeyEnv {
		t.Fatalf("route = %v", r)
	}
	c := r["compat"].(map[string]any)
	if c["supportsDeveloperRole"] != false || c["maxTokensField"] != "max_tokens" || c["thinkingFormat"] != "qwen-chat-template" {
		t.Fatalf("compat = %v", c)
	}
	m := r["models"].([]any)[0].(map[string]any)
	if m["id"] != Model || m["contextWindow"] != 262144 || m["maxTokens"] != 32768 {
		t.Fatalf("model = %v", m)
	}
	d := p["agent-default-model"].Config
	if d["provider"] != Provider || d["model"] != Model {
		t.Fatalf("default = %v", d)
	}
}

// Other providers, other entries and a default model chosen in the UI stay;
// only the arkey-local route is replaced, and a second write changes nothing.
func TestWriteConfigOwnsOnlyItsRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.patch.yml")
	os.WriteFile(path, []byte(`- id: llm-pi-ai
  config:
    providers:
      openrouter:
        apiKeyEnv: OPENROUTER_API_KEY
      arkey-local:
        baseURL: http://stale:1/v1
- id: agent-default-model
  config:
    provider: openrouter
    model: some-model
- id: something-else
  disabled: true
`), 0o600)
	for range 2 {
		if err := WriteConfig(path, "http://127.0.0.1:8090/v1", 65536); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := os.ReadFile(path)
	WriteConfig(path, "http://127.0.0.1:8090/v1", 65536)
	if second, _ := os.ReadFile(path); string(first) != string(second) {
		t.Fatalf("rewrite is not stable:\n%s\n---\n%s", first, second)
	}
	p := readPatch(t, path)
	providers := p["llm-pi-ai"].Config["providers"].(map[string]any)
	if providers["openrouter"] == nil {
		t.Fatal("another provider was dropped")
	}
	if got := providers[Provider].(map[string]any)["baseURL"]; got != "http://127.0.0.1:8090/v1" {
		t.Fatalf("stale route kept: %v", got)
	}
	if d := p["agent-default-model"].Config; d["provider"] != "openrouter" || d["model"] != "some-model" {
		t.Fatalf("the operator's default model was replaced: %v", d)
	}
	if _, ok := p["something-else"]; !ok {
		t.Fatal("an unrelated entry was dropped")
	}
}

func TestWriteConfigRefusesANonListPatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cordis.patch.yml")
	os.WriteFile(path, []byte("id: not-a-list\n"), 0o600)
	if WriteConfig(path, "http://127.0.0.1:8090/v1", 65536) == nil {
		t.Fatal("a mapping at the top level was rewritten")
	}
}

func TestBuildRunsTheWebCommandWithAPlaceholderKey(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apps", "cli", "src"), 0o755)
	os.WriteFile(entry(dir), []byte("// cli"), 0o644)
	if _, err := Build(dir, nil, false); err == nil {
		t.Fatal("a checkout without node_modules was launched")
	}
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	plan, err := Build(dir, []string{"PATH=" + os.Getenv("PATH")}, true)
	if err != nil {
		t.Skip("node is not installed:", err)
	}
	args := strings.Join(plan.Args, " ")
	if !strings.HasSuffix(args, entry(dir)+" web --port 0") || plan.Dir != dir {
		t.Fatalf("args = %s dir = %s", args, plan.Dir)
	}
	if !hasEnv(plan.Env, KeyEnv) {
		t.Fatalf("env = %v", plan.Env)
	}
	plan, _ = Build(dir, []string{KeyEnv + "=mine"}, false)
	if !slicesContain(plan.Env, KeyEnv+"=mine") || strings.Contains(strings.Join(plan.Args, " "), "--port") {
		t.Fatalf("env = %v args = %v", plan.Env, plan.Args)
	}
}

func slicesContain(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
