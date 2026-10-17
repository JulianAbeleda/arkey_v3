package kimi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/harness"
)

func TestSetMCPServerKeepsOtherServers(t *testing.T) {
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		t.Setenv("TMPDIR", resolved)
	}
	home := filepath.Join(t.TempDir(), "kimi-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MCPPath(home), []byte(`{"mcpServers":{"notes":{"command":"notes-mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := harness.Command("/bin/arkey", "/dsh", "/patch.yml")
	if err := SetMCPServer(home, harness.DeepSeekMCP, &server); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command       string   `json:"command"`
			Args          []string `json:"args"`
			ToolTimeoutMs int64    `json:"toolTimeoutMs"`
		} `json:"mcpServers"`
	}
	b, _ := os.ReadFile(MCPPath(home))
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	got := doc.MCPServers[harness.DeepSeekMCP]
	if got.Command != "/bin/arkey" || len(got.Args) == 0 || got.Args[0] != "harness-mcp" || got.ToolTimeoutMs != 900000 || doc.MCPServers["notes"].Command != "notes-mcp" {
		t.Fatalf("mcp.json:\n%s", b)
	}
	if err := SetMCPServer(home, harness.DeepSeekMCP, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(MCPPath(home))
	doc.MCPServers = nil
	_ = json.Unmarshal(b, &doc)
	if _, ok := doc.MCPServers[harness.DeepSeekMCP]; ok || doc.MCPServers["notes"].Command != "notes-mcp" {
		t.Fatalf("off must remove only Arkey's server:\n%s", b)
	}
}
