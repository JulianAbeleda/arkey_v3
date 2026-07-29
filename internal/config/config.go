// Package config owns durable, credential-free Arkey settings.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const CurrentVersion = 1

type Config struct {
	Version    int        `toml:"version"`
	Mode       string     `toml:"mode"`
	Frontier   Frontier   `toml:"frontier"`
	Local      Local      `toml:"local"`
	Hardware   Hardware   `toml:"hardware"`
	MoonBridge MoonBridge `toml:"moonbridge"`
	UI         UI         `toml:"ui"`
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
	return Config{Version: 1, Mode: "frontier", Frontier: Frontier{"deepseek"}, Local: Local{Runtime: "llama", Port: 8080, ContextSize: 32768}, Hardware: Hardware{Vendor: "unknown"}, MoonBridge: MoonBridge{Address: "127.0.0.1:38440", Config: filepath.Join(d, "moonbridge.yml")}}
}
func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Mode != "frontier" && c.Mode != "local" {
		return errors.New("invalid mode")
	}
	if c.Frontier.Backend != "deepseek" && c.Frontier.Backend != "codex" && c.Frontier.Backend != "claude" {
		return errors.New("invalid frontier backend")
	}
	if c.Local.Runtime != "llama" {
		return errors.New("invalid local runtime")
	}
	if c.Hardware.Vendor != "unknown" && c.Hardware.Vendor != "nvidia" && c.Hardware.Vendor != "amd" {
		return errors.New("invalid hardware vendor")
	}
	if c.Local.Port < 1 || c.Local.Port > 65535 {
		return errors.New("invalid local port")
	}
	if c.Local.ContextSize < 1 {
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
