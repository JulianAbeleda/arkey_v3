// Package config owns durable, credential-free Arkey settings.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const CurrentVersion = 1

type Config struct {
	Version    int           `toml:"version"`
	Client     string        `toml:"client"`
	Mode       string        `toml:"mode"`
	Frontier   Frontier      `toml:"frontier"`
	Local      Local         `toml:"local"`
	Server     Server        `toml:"server"`
	Servers    []ServerEntry `toml:"servers"`
	Hardware   Hardware      `toml:"hardware"`
	MoonBridge MoonBridge    `toml:"moonbridge"`
	UI         UI            `toml:"ui"`
}
type Frontier struct {
	Backend string `toml:"backend"`
}
type Local struct {
	Runtime     string `toml:"runtime"`
	Model       string `toml:"model"`
	LlamaServer string `toml:"llama_server"`
	Port        int    `toml:"port"`
	ContextSize int    `toml:"context_size"`
}

// Server is the selected llama.cpp server on the network (mode "server"): a
// llama-server somebody else started, reached by origin. Model and
// ContextSize are what the server reported when it was selected.
type Server struct {
	Label       string `toml:"label"`
	Origin      string `toml:"origin"`
	Model       string `toml:"model"`
	ContextSize int    `toml:"context_size"`
}

// ServerEntry is one server the Config → Server screen offers.
type ServerEntry struct {
	Label  string `toml:"label"`
	Origin string `toml:"origin"`
}

// ValidOrigin accepts scheme://host[:port] and nothing after it.
func ValidOrigin(s string) bool {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	return (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" && !strings.HasSuffix(s, "/")
}

type Hardware struct {
	Vendor string `toml:"vendor"`
	Name   string `toml:"name"`
}
type MoonBridge struct {
	Address string `toml:"address"`
	Config  string `toml:"config"`
}
type UI struct {
	ReducedMotion bool `toml:"reduced_motion"`
}

func Default(home string) Config {
	d := filepath.Join(home, ".config", "arkey")
	return Config{Version: 1, Client: "codex", Mode: "frontier", Frontier: Frontier{"deepseek"}, Local: Local{Runtime: "llama", Port: 8080, ContextSize: 32768}, Hardware: Hardware{Vendor: "unknown"}, MoonBridge: MoonBridge{Address: "127.0.0.1:38440", Config: filepath.Join(d, "moonbridge.yml")}}
}
func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Mode != "frontier" && c.Mode != "local" && c.Mode != "server" {
		return errors.New("invalid mode")
	}
	if c.Mode == "server" && !ValidOrigin(c.Server.Origin) {
		return errors.New("server mode needs a selected server origin (scheme://host:port)")
	}
	if c.Server.Origin != "" && !ValidOrigin(c.Server.Origin) {
		return errors.New("invalid server origin")
	}
	if c.Server.ContextSize < 0 {
		return errors.New("invalid server context size")
	}
	for i, entry := range c.Servers {
		if strings.TrimSpace(entry.Label) == "" {
			return fmt.Errorf("servers[%d] needs a label", i)
		}
		if !ValidOrigin(entry.Origin) {
			return fmt.Errorf("servers[%d]: origin must be scheme://host:port", i)
		}
	}
	if c.Client != "codex" && c.Client != "claude" && c.Client != "kimi" && c.Client != "crush" {
		return errors.New("invalid client")
	}
	if c.Frontier.Backend != "deepseek" && c.Frontier.Backend != "codex" && c.Frontier.Backend != "claude" {
		return errors.New("invalid frontier backend")
	}
	if c.Local.Runtime != "llama" {
		return errors.New("invalid local runtime")
	}
	if c.Hardware.Vendor != "unknown" && c.Hardware.Vendor != "nvidia" && c.Hardware.Vendor != "amd" && c.Hardware.Vendor != "metal" {
		return errors.New("invalid hardware vendor")
	}
	if c.Local.Port < 1 || c.Local.Port > 65535 {
		return errors.New("invalid local port")
	}
	// Zero means "derive from hardware at launch" (see models.DeriveContextSize).
	// A positive value is an explicit pin the operator has chosen; negative is
	// always a mistake.
	if c.Local.ContextSize < 0 {
		return errors.New("invalid context size")
	}
	if c.Local.Model != "" {
		i, e := os.Stat(c.Local.Model)
		if e != nil || !i.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(c.Local.Model), ".gguf") {
			return errors.New("local model must be a regular .gguf file")
		}
	}
	return nil
}
func Encode(c Config) ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	return toml.Marshal(c)
}
func Decode(b []byte, home string) (Config, error) {
	c := Default(home)
	if err := toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
