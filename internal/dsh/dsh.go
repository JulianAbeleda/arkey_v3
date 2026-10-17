// Package dsh launches DeepSeek Harness's browser UI against Arkey's local
// OpenAI-compatible gateway.
//
// DeepSeek Harness reads YAML patch layers from $DSH_HOME (default ~/.dsh).
// Arkey owns one provider route in the web profile's patch, arkey-local,
// pointed at the gateway; every other entry, and any default model the
// operator picked in the UI, is left as it was.
package dsh

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/JulianAbeleda/arkey_v3/internal/client"
	"gopkg.in/yaml.v3"
)

const (
	// Provider is the route key Arkey owns in the pi-ai provider table.
	Provider = "arkey-local"
	// Model is the id DeepSeek Harness requests. The gateway answers with
	// whichever engine runs and rewrites the name to the one it serves, so
	// the id stays the same across llama.cpp and vLLM.
	Model = "arkey-local"
	// KeyEnv names the placeholder credential: pi-ai's OpenAI-compatible
	// client refuses to send a request without a key, and the local gateway
	// checks none.
	KeyEnv = "ARKEY_LOCAL_API_KEY"
	// Profile is the dsh profile `dsh web` boots.
	Profile = "web"
)

// DefaultDir is where Arkey expects the DeepSeek Harness checkout.
func DefaultDir(home string) string { return filepath.Join(home, "deepseek-harness") }

// Home is $DSH_HOME, or ~/.dsh.
func Home(home string) string {
	if v := os.Getenv("DSH_HOME"); v != "" {
		return v
	}
	return filepath.Join(home, ".dsh")
}

// PatchPath is the web profile's patch layer, the file DeepSeek Harness's own
// settings screens write to.
func PatchPath(dshHome string) string {
	return filepath.Join(dshHome, "profiles", Profile, "cordis.patch.yml")
}

func entry(dir string) string { return filepath.Join(dir, "apps", "cli", "src", "bin.ts") }

// Status reports whether a checkout at dir can serve the web UI.
func Status(dir string) string {
	if info, err := os.Stat(entry(dir)); err != nil || !info.Mode().IsRegular() {
		return "not installed"
	}
	if info, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil || !info.IsDir() {
		return "not built"
	}
	if _, err := exec.LookPath("node"); err != nil {
		return "node missing"
	}
	return "ready"
}

// Plan is the exec of `dsh web` from its checkout.
type Plan struct {
	client.Plan
	Dir string
}

// Build runs the CLI from source the way the checkout's own scripts do.
// freePort asks the server to pick an unused port, for when 3080 is taken;
// the browser it opens is told the real one either way.
func Build(dir string, env []string, freePort bool) (Plan, error) {
	if s := Status(dir); s != "ready" {
		return Plan{}, fmt.Errorf("DeepSeek Harness at %s is %s", dir, s)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return Plan{}, err
	}
	args := []string{"--import", "tsx/esm", entry(dir), "web"}
	if freePort {
		args = append(args, "--port", "0")
	}
	env = append([]string(nil), env...)
	if !hasEnv(env, KeyEnv) {
		env = client.SetEnv(env, KeyEnv, "arkey-local")
	}
	return Plan{Plan: client.Plan{Binary: node, Args: args, Env: env}, Dir: dir}, nil
}

// Headless is the exec of one `dsh headless --json` task from the checkout,
// with patch layered over the headless profile. The task is read from stdin
// (`-`), so a task that starts with a dash is never taken for a flag; a
// session id continues that Session.
func Headless(dir, patch, sessionID string, env []string) (client.Plan, error) {
	if s := Status(dir); s != "ready" {
		return client.Plan{}, fmt.Errorf("DeepSeek Harness at %s is %s", dir, s)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return client.Plan{}, err
	}
	// The run's working directory is the client's workspace, not the
	// checkout. tsx resolves both its loader and the tsconfig that maps the
	// workspace packages from the working directory, so both are named in the
	// checkout; otherwise the source imports resolve to stale builds or fail.
	loader := (&url.URL{Scheme: "file", Path: filepath.Join(dir, "node_modules", "tsx", "dist", "esm", "index.mjs")}).String()
	args := []string{"--import", loader, entry(dir), "headless", "--patch", patch, "--json"}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	args = append(args, "-")
	env = append([]string(nil), env...)
	env = client.SetEnv(env, "TSX_TSCONFIG_PATH", filepath.Join(dir, "tsconfig.json"))
	if !hasEnv(env, KeyEnv) {
		env = client.SetEnv(env, KeyEnv, "arkey-local")
	}
	return client.Plan{Binary: node, Args: args, Env: env}, nil
}

func hasEnv(env []string, key string) bool {
	for _, e := range env {
		if len(e) > len(key) && e[:len(key)+1] == key+"=" {
			return true
		}
	}
	return false
}

type compat struct {
	SupportsStore           bool   `yaml:"supportsStore"`
	SupportsDeveloperRole   bool   `yaml:"supportsDeveloperRole"`
	SupportsReasoningEffort bool   `yaml:"supportsReasoningEffort"`
	SupportsStrictMode      bool   `yaml:"supportsStrictMode"`
	MaxTokensField          string `yaml:"maxTokensField"`
	ThinkingFormat          string `yaml:"thinkingFormat"`
}

type model struct {
	ID               string             `yaml:"id"`
	Name             string             `yaml:"name"`
	ContextWindow    int                `yaml:"contextWindow"`
	MaxTokens        int                `yaml:"maxTokens"`
	Input            []string           `yaml:"input"`
	ReasoningEfforts map[string]*string `yaml:"reasoningEfforts"`
}

type route struct {
	DisplayName string  `yaml:"displayName"`
	API         string  `yaml:"api"`
	BaseURL     string  `yaml:"baseURL"`
	APIKeyEnv   string  `yaml:"apiKeyEnv"`
	Compat      compat  `yaml:"compat"`
	Models      []model `yaml:"models"`
}

// Route is the arkey-local provider: OpenAI Chat Completions at the gateway,
// shaped for a vLLM/llama.cpp server rather than OpenAI itself. Without these
// switches pi-ai treats an unknown address as OpenAI: it sends the system
// prompt as `developer` and caps output with max_completion_tokens, which
// local servers reject or ignore. Qwen-family thinking is toggled through the
// chat template's enable_thinking; reasoning_content in the stream is read
// without a switch.
func Route(baseURL string, contextWindow int) any {
	if contextWindow < 4096 {
		contextWindow = 32768
	}
	high := "high"
	return route{
		DisplayName: "Arkey local",
		API:         "openai-completions",
		BaseURL:     baseURL,
		APIKeyEnv:   KeyEnv,
		Compat: compat{
			MaxTokensField: "max_tokens",
			ThinkingFormat: "qwen-chat-template",
		},
		Models: []model{{
			ID:               Model,
			Name:             "Arkey local model",
			ContextWindow:    contextWindow,
			MaxTokens:        min(32768, contextWindow/4),
			Input:            []string{"text"},
			ReasoningEfforts: map[string]*string{"off": nil, "high": &high},
		}},
	}
}

// WriteConfig sets the arkey-local route in the web profile's patch and, when
// the patch picks no default model, makes the route's model the default. The
// rest of the file, comments included, is kept.
func WriteConfig(path, baseURL string, contextWindow int) error {
	var doc yaml.Node
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(bytes.TrimSpace(b)) > 0 {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("DeepSeek Harness patch %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}
	}
	list := doc.Content[0]
	if list.Kind != yaml.SequenceNode {
		return fmt.Errorf("DeepSeek Harness patch %s: top level must be a list of entries", path)
	}
	list.Style = 0 // an empty `[]` would otherwise stay inline once entries are added

	var want yaml.Node
	if err := want.Encode(Route(baseURL, contextWindow)); err != nil {
		return err
	}
	providers := mapping(mapping(byID(list, "llm-pi-ai"), "config"), "providers")
	set(providers, Provider, &want)

	if find(list, "agent-default-model") == nil {
		defaults := mapping(byID(list, "agent-default-model"), "config")
		set(defaults, "provider", scalar(Provider))
		set(defaults, "model", scalar(Model))
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".arkey.tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// find returns the patch entry whose id is id.
func find(list *yaml.Node, id string) *yaml.Node {
	for _, item := range list.Content {
		if item.Kind == yaml.MappingNode {
			if v := get(item, "id"); v != nil && v.Value == id {
				return item
			}
		}
	}
	return nil
}

// byID returns the entry for id, appending an empty one when absent.
func byID(list *yaml.Node, id string) *yaml.Node {
	if item := find(list, id); item != nil {
		return item
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	set(item, "id", scalar(id))
	list.Content = append(list.Content, item)
	return item
}

func get(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func set(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, scalar(key), v)
}

// mapping returns m[key] as a mapping, creating or replacing a non-mapping.
func mapping(m *yaml.Node, key string) *yaml.Node {
	if v := get(m, key); v != nil && v.Kind == yaml.MappingNode {
		v.Style = 0
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	set(m, key, v)
	return v
}
