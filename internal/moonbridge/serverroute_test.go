package moonbridge

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

const frontierOnly = `mode: "Transform"
server:
  addr: "127.0.0.1:38440"
models:
  deepseek-v4-pro:
    context_window: 1000000
providers:
  deepseek:
    protocol: "anthropic"
    base_url: "https://api.deepseek.com/anthropic"
    api_key: "keep-this-key"
    offers:
      - model: deepseek-v4-pro
routes:
  deepseek-v4-pro:
    model: deepseek-v4-pro
    provider: deepseek
defaults:
  model: deepseek-v4-pro
`

func TestSetServerRouteAddsTheRouteAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "moonbridge.yml")
	if err := os.WriteFile(path, []byte(frontierOnly), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := SetServerRoute(path, "http://100.106.46.126:8080", "arkey-local", 262144)
	if err != nil || !changed {
		t.Fatalf("first write changed=%v err=%v", changed, err)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	providers := doc["providers"].(map[string]any)
	server := providers[ServerProvider].(map[string]any)
	if server["base_url"] != "http://100.106.46.126:8080" || server["protocol"] != "openai-chat" {
		t.Fatalf("server provider = %#v", server)
	}
	offer := server["offers"].([]any)[0].(map[string]any)
	if offer["upstream_name"] != "arkey-local" || offer["model"] != ServerModel {
		t.Fatalf("offer = %#v", offer)
	}
	if providers["deepseek"].(map[string]any)["api_key"] != "keep-this-key" {
		t.Fatal("the frontier provider's key was lost")
	}
	if doc["models"].(map[string]any)[ServerModel].(map[string]any)["context_window"] != 262144 {
		t.Fatalf("model = %#v", doc["models"])
	}
	if doc["routes"].(map[string]any)[ServerRoute].(map[string]any)["provider"] != ServerProvider {
		t.Fatalf("route = %#v", doc["routes"])
	}
	if doc["defaults"].(map[string]any)["model"] != "deepseek-v4-pro" {
		t.Fatal("defaults were lost")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	again, err := SetServerRoute(path, "http://100.106.46.126:8080", "arkey-local", 262144)
	if err != nil || again {
		t.Fatalf("same route again: changed=%v err=%v", again, err)
	}
	moved, err := SetServerRoute(path, "http://10.0.0.9:8080", "arkey-local", 262144)
	if err != nil || !moved {
		t.Fatalf("a new origin is a change: changed=%v err=%v", moved, err)
	}
}

func TestSetServerRouteRefusesAnIncompleteRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "moonbridge.yml")
	if _, err := SetServerRoute(path, "", "m", 1); err == nil {
		t.Fatal("no origin must be refused")
	}
	if _, err := SetServerRoute(path, "http://x:1", "m", 0); err == nil {
		t.Fatal("no context window must be refused")
	}
}
