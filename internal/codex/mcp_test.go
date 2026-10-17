package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JulianAbeleda/arkey_v3/internal/harness"
)

func TestSetMCPServerAddsAndRemovesOnlyItsTable(t *testing.T) {
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		t.Setenv("TMPDIR", resolved)
	}
	home := filepath.Join(t.TempDir(), "codex-home")
	if err := WriteConfig(home, "http://127.0.0.1:38440", "", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.toml")
	existing, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(existing, []byte("\n[mcp_servers.brave]\ncommand = 'node'\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	server := harness.Command("/bin/arkey", "/dsh", "/patch.yml")
	if err := SetMCPServer(home, harness.DeepSeekMCP, &server); err != nil {
		t.Fatal(err)
	}
	on, _ := os.ReadFile(path)
	for _, want := range []string{"[mcp_servers.deepseek-mcp]", "command = '/bin/arkey'", "'harness-mcp'", "tool_timeout_sec = 900", "[mcp_servers.brave]", "model_provider = 'moonbridge'"} {
		if !strings.Contains(string(on), want) {
			t.Fatalf("missing %q:\n%s", want, on)
		}
	}
	if err := SetMCPServer(home, harness.DeepSeekMCP, nil); err != nil {
		t.Fatal(err)
	}
	off, _ := os.ReadFile(path)
	if strings.Contains(string(off), "deepseek-mcp") || !strings.Contains(string(off), "[mcp_servers.brave]") {
		t.Fatalf("off must remove only Arkey's server:\n%s", off)
	}
}
