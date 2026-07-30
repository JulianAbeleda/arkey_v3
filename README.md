# Arkey v3

Arkey v3 is a Go boot manager for isolated, Arkey-modified client harnesses.
Its Bubble Tea interface selects an Arkey-owned snapshot of an installed coding
client, selects a local or frontier AI route, starts the required local runtime,
and connects supported clients through MoonBridge.

Arkey does not distribute Codex, Claude Code, or Kimi Code. The installer copies
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
Config
  Local
    tinygrad     (in development; unavailable)
    llama.cpp
      installed GGUF models
      Enter loads · r refreshes · d unloads the active model
  Frontier
    DeepSeek
    Codex
    Claude
  GPU Auto-scan
Exit
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
`ARKEY_CODEX_SOURCE_BIN`, `ARKEY_CLAUDE_SOURCE_BIN`, and
`ARKEY_KIMI_SOURCE_BIN`.

Generated configuration is stored outside the repository. Arkey writes
`~/.config/arkey/config.toml`; MoonBridge uses
`~/.config/arkey/moonbridge.yml` (or preserves an existing
`~/moon-bridge/config.yml`). Configuration and state files are mode `0600`, and
application state/logs live under `~/.local/state/arkey`. Models, API
credentials, Codex state, sessions, logs, and machine-specific GPU state are
never stored in this repository.

Client state is isolated under `~/.codex-moonbridge`, `~/.claude-arkey`, and
`~/.kimi-arkey`. Codex and Kimi use MoonBridge's OpenAI Responses ingress.
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
