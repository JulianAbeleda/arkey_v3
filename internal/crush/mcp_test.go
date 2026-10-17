package crush

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/harness"
)

func TestSetMCPServerSurvivesWriteConfig(t *testing.T) {
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		t.Setenv("TMPDIR", resolved)
	}
	home := filepath.Join(t.TempDir(), "crush-home")
	if err := WriteConfig(home, "http://127.0.0.1:38440", "", "arkey-local-vllm", 32768, 8192); err != nil {
		t.Fatal(err)
	}
	server := harness.Command("/bin/arkey", "/dsh", "/patch.yml")
	if err := SetMCPServer(home, harness.DeepSeekMCP, &server); err != nil {
		t.Fatal(err)
	}
	// The next launch rewrites the provider first; the server must stay.
	if err := WriteConfig(home, "http://127.0.0.1:38440", "", "arkey-local-vllm", 32768, 8192); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCP map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Timeout int      `json:"timeout"`
		} `json:"mcp"`
	}
	b, _ := os.ReadFile(ConfigPath(home))
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.MCP[harness.DeepSeekMCP]
	if got.Type != "stdio" || got.Command != "/bin/arkey" || got.Args[0] != "harness-mcp" || got.Timeout != 900 {
		t.Fatalf("crush config:\n%s", b)
	}
	if err := SetMCPServer(home, harness.DeepSeekMCP, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(ConfigPath(home))
	cfg.MCP = nil
	_ = json.Unmarshal(b, &cfg)
	if _, ok := cfg.MCP[harness.DeepSeekMCP]; ok {
		t.Fatalf("off must remove the server:\n%s", b)
	}
}
