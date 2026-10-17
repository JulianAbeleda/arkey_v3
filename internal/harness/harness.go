// Package harness is Arkey's shared harness: one MCP server every Arkey TUI
// loads beside its own tools, so Codex, Kimi and Crush all reach the same
// DeepSeek Harness agent.
//
// `arkey harness-mcp` speaks MCP over stdio. Its one tool hands a task to
// `dsh headless` in the client's working directory, on the local model
// through Arkey's gateway, and returns the agent's final answer. The client's
// own loop decides when to call it; DeepSeek Harness runs its own tools to
// answer, and refuses anything that would need an approval, because nobody
// is there to give one.
package harness

import (
	"path/filepath"
	"time"
)

const (
	// DeepSeekMCP is the Harness menu's DeepSeek MCP entry, and the name each
	// client registers the server under.
	DeepSeekMCP = "deepseek-mcp"
	// Tool is the one tool the server publishes.
	Tool = "deepseek_harness"
	// Subcommand is the arkey argument that serves MCP on stdio.
	Subcommand = "harness-mcp"
	// ToolTimeout bounds one task, the way each client counts it. A dsh run
	// that reads and searches a workspace takes minutes, not seconds.
	ToolTimeout = 15 * time.Minute
	// StartupTimeout is how long a client waits for the server's handshake.
	StartupTimeout = 20 * time.Second
)

// Server is the stdio MCP server a client starts at session start, in the
// client's own vocabulary-free form; each client package writes it in its
// own config format.
type Server struct {
	Command string
	Args    []string
}

// PatchPath is the dsh patch layer Arkey owns for the harness: the
// arkey-local provider at the gateway. It is passed with --patch, so the
// operator's own headless profile is never written.
func PatchPath(stateDir string) string {
	return filepath.Join(stateDir, "dsh", "harness.patch.yml")
}

// Command is the server entry for an arkey binary: `arkey harness-mcp` with
// the DeepSeek Harness checkout and the patch it runs with.
func Command(arkey, dshDir, patch string) Server {
	return Server{Command: arkey, Args: []string{Subcommand, "--dsh", dshDir, "--patch", patch}}
}
