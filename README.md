# Arkey v3

Arkey v3 is a Go boot manager for isolated, Arkey-modified client harnesses.
Its Bubble Tea interface selects an Arkey-owned snapshot of an installed coding
client, selects a local or frontier AI route, starts the required local runtime,
and connects supported clients through MoonBridge.

Arkey does not distribute Codex, Claude Code, Kimi Code, or Crush. The installer copies
the user's existing official executables into a private Arkey libexec directory;
the official installations and their state remain untouched. “Modded” refers to
the Arkey harness, routing, and isolated configuration—not patched upstream code.
WezTerm is not part of this app; its boot manager was only an interaction
reference.

## What is included

- `cmd/arkey/`: the single Go application entry point
- `internal/`: Bubble Tea UI, configuration, GPU, MoonBridge, and llama runtime
- `dependencies/moonbridge.env`: the exact MoonBridge fork revision used by Arkey
- `dependencies/moonbridge.example.yml`: credential-free MoonBridge config template
- `scripts/install-moonbridge-dependency.sh`: verified dependency build/install
- `dependencies/brave-search.env`: the exact Brave Search MCP server version used by Arkey
- `dependencies/brave-search-rate-limit.patch`: patch that makes the client-side rate limiter queue instead of throwing
- `scripts/install-brave-search-dependency.sh`: pinned Brave Search MCP server install/patch
- `scripts/snapshot-clients.sh`: local-only, hashed client snapshot installer

MoonBridge remains an external, pinned dependency:
[JulianAbeleda/moon-bridge-arkey](https://github.com/JulianAbeleda/moon-bridge-arkey).
Arkey v3 does not contain or depend on the Arkey v2 runtime snapshot.

## Menu

```text
TUI
  Arkey Codex  (modded harness)
  Arkey Claude (modded harness; MoonBridge ingress pending)
  Arkey Kimi   (modded harness)
  Arkey Crush  (modded harness)
Config
  Local
    tinygrad     (in development; unavailable)
    llama.cpp
      installed GGUF models
      Enter loads · r refreshes · d unloads the active model
  Server
    one row per [[servers]] entry in config.toml
    Enter probes the server and makes it the route
  Frontier
    DeepSeek
    Codex
    Claude
  GPU Auto-scan
Exit
```

## Server route

A llama.cpp server somebody else runs, on this machine or another one, is a
route beside Local and Frontier. List the candidates in
`~/.config/arkey/config.toml`:

```toml
[[servers]]
label = "Ubuntu · Tailscale"
origin = "http://100.106.46.126:8080"
```

Config → Server → Enter probes the server (`/health`, `/v1/models`,
`/props`), writes a `llama-server` provider and the `arkey-server-llama`
route into the MoonBridge config with the server's own model name and
context window, registers the Codex model metadata, and persists
`mode = "server"`. Nothing is started for this route; the next launch
restarts the Arkey-owned MoonBridge so it reads the route. The same
selection without the screen, for a script or a first setup:

```bash
arkey --select-server=http://100.106.46.126:8080
arkey --no-boot exec "Reply with the single word ready."
```

`scripts/smoke-server-route.sh ORIGIN` proves the route on the installed
Arkey: selects the server, launches Codex through MoonBridge with a marker
prompt, and checks the reply, the launch warnings and MoonBridge's route
line.

The server must listen beyond loopback for another machine to reach it
(`--host 0.0.0.0`, or a tailnet address). Arkey never starts a server
that way itself.

Codex's isolated home (`~/.codex-moonbridge/config.toml`) gets its
`model_providers.moonbridge` table from Arkey at launch when it is missing
or the bridge address moved; nothing else in that file is touched.

On macOS the official Codex from npm is a Node wrapper; snapshot the native
binary it wraps:

```bash
ARKEY_CODEX_SOURCE_BIN=/opt/homebrew/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex scripts/snapshot-clients.sh
```

## Install

```bash
scripts/install.sh
```

The installer requires `git` and a Go 1.25-compatible toolchain. It builds the
Go Arkey binary, installs the pinned MoonBridge fork into
`~/.local/libexec/arkey/moonbridge`, snapshots any installed clients into
`~/.local/libexec/arkey/clients/`, and installs Arkey into `~/.local/bin`.
Missing clients are skipped and shown as unavailable in the TUI. Refresh the
snapshots after an official client update with:

```bash
scripts/snapshot-clients.sh
```

Snapshot executables and their `snapshot.env` manifests are machine-local and
never committed. Source locations can be overridden with
`ARKEY_CODEX_SOURCE_BIN`, `ARKEY_CLAUDE_SOURCE_BIN`,
`ARKEY_KIMI_SOURCE_BIN`, and `ARKEY_CRUSH_SOURCE_BIN`.

Generated configuration is stored outside the repository. Arkey writes
`~/.config/arkey/config.toml`; MoonBridge uses
`~/.config/arkey/moonbridge.yml` (or preserves an existing
`~/moon-bridge/config.yml`). Configuration and state files are mode `0600`, and
application state/logs live under `~/.local/state/arkey`. Models, API
credentials, Codex state, sessions, logs, and machine-specific GPU state are
never stored in this repository.

Client state is isolated under `~/.codex-moonbridge`, `~/.claude-arkey`,
`~/.kimi-arkey`, and `~/.crush-arkey`. Codex and Kimi use MoonBridge's OpenAI
Responses ingress. Crush speaks OpenAI Chat Completions and uses MoonBridge's
`/v1/chat/completions` ingress; its config and session state are pinned inside
the Arkey state home with `CRUSH_GLOBAL_CONFIG` and `CRUSH_GLOBAL_DATA`, so the
official `~/.config/crush` installation is untouched.

Crush cannot use the Codex frontier: MoonBridge converts the Chat Completions
ingress into Anthropic, Google GenAI and OpenAI Chat upstreams, but an OpenAI
Responses upstream is only reachable by verbatim passthrough from the Responses
ingress. Arkey refuses that combination before launch rather than failing on
every request.

Claude is snapshotted and isolated but intentionally unavailable in the menu
until the pinned MoonBridge fork provides Anthropic Messages ingress; Arkey does
not bypass that missing protocol boundary by modifying or redistributing Claude
Code.

The implemented architecture and remaining compatibility-cutover work are
defined in
[`docs/bubble-tea-migration-scope.md`](docs/bubble-tea-migration-scope.md).

## Validate

```bash
scripts/install.sh --check
go test ./...
```
