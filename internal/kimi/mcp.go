package kimi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JulianAbeleda/arkey_v3/internal/harness"
	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

// MCPPath is the user-level MCP file Kimi reads from KIMI_CODE_HOME.
func MCPPath(stateHome string) string { return filepath.Join(stateHome, "mcp.json") }

// SetMCPServer registers server under mcpServers.<name> in the isolated Kimi
// home, or removes it when server is nil. Other servers and keys are kept.
func SetMCPServer(stateHome, name string, server *harness.Server) error {
	if err := platform.EnsurePrivateDir(stateHome); err != nil {
		return err
	}
	path := MCPPath(stateHome)
	if err := platform.RejectSymlinkComponents(path); err != nil {
		return err
	}
	doc := map[string]any{}
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Kimi MCP config must be a regular file")
		}
		if info.Size() > 1<<20 {
			return fmt.Errorf("Kimi MCP config exceeds 1 MiB safety limit")
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if len(existing) > 0 {
			if err := json.Unmarshal(existing, &doc); err != nil {
				return fmt.Errorf("read existing Kimi MCP config: %w", err)
			}
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if server == nil {
		if _, ok := servers[name]; !ok {
			return nil
		}
		delete(servers, name)
	} else {
		if servers == nil {
			servers = map[string]any{}
		}
		servers[name] = map[string]any{
			"command":          server.Command,
			"args":             server.Args,
			"startupTimeoutMs": harness.StartupTimeout.Milliseconds(),
			"toolTimeoutMs":    harness.ToolTimeout.Milliseconds(),
		}
		doc["mcpServers"] = servers
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(stateHome, ".mcp.*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(append(data, '\n'))
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
