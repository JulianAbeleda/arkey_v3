// Package config owns durable, credential-free Arkey settings.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const CurrentVersion = 1

type Config struct {
	Version    int        `toml:"version"`
	Client     string     `toml:"client"`
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
	VLLMReasoningEffort string `toml:"vllm_reasoning_effort"`
	VLLMURL             string `toml:"vllm_url"`
	VLLMModel           string `toml:"vllm_model"`
	VLLMContextSize     int    `toml:"vllm_context_size"`
	Runtime             string `toml:"runtime"`
	Model               string `toml:"model"`
	LlamaServer         string `toml:"llama_server"`
	Port                int    `toml:"port"`
	ContextSize         int    `toml:"context_size"`
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
	return Config{Version: 1, Client: "codex", Mode: "frontier", Frontier: Frontier{"deepseek"}, Local: Local{Runtime: "llama", VLLMURL: "http://127.0.0.1:8000", VLLMModel: "arkey-local-vllm", VLLMContextSize: 32768, VLLMReasoningEffort: "medium", Port: 8080, ContextSize: 32768}, Hardware: Hardware{Vendor: "unknown"}, MoonBridge: MoonBridge{Address: "127.0.0.1:38440", Config: filepath.Join(d, "moonbridge.yml")}}
}
func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.Mode != "frontier" && c.Mode != "local" {
		return errors.New("invalid mode")
	}
	if c.Client != "codex" && c.Client != "claude" && c.Client != "kimi" && c.Client != "crush" {
		return errors.New("invalid client")
	}
	if c.Frontier.Backend != "deepseek" && c.Frontier.Backend != "codex" && c.Frontier.Backend != "claude" {
		return errors.New("invalid frontier backend")
	}
	if c.Local.Runtime != "llama" && c.Local.Runtime != "vllm" {
		return errors.New("invalid local runtime")
	}
	u, err := url.Parse(c.Local.VLLMURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("vllm_url must be a local HTTP server root, without /v1")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("vllm_url must use a loopback host")
	}
	if c.Local.VLLMModel == "" || c.Local.VLLMContextSize < 4096 {
		return errors.New("vLLM requires a served model name and context size of at least 4096")
	}
	switch c.Local.VLLMReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		return errors.New("invalid vllm_reasoning_effort")
	}
	if c.Hardware.Vendor != "unknown" && c.Hardware.Vendor != "nvidia" && c.Hardware.Vendor != "amd" {
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
