# Arkey v3

Arkey v3 is a Go boot manager for **Arkey Codex**, our modified Codex-derived
TUI. Its Bubble Tea interface selects a local or frontier AI route, starts the
required local runtime, and connects the TUI through MoonBridge.

Arkey Codex is not the official Codex client. WezTerm is not part of this app;
its boot manager was only an interaction reference.

## What is included

- `cmd/arkey/`: the single Go application entry point
- `internal/`: Bubble Tea UI, configuration, GPU, MoonBridge, and llama runtime
- `dependencies/moonbridge.env`: the exact MoonBridge fork revision used by Arkey
- `scripts/install-moonbridge-dependency.sh`: verified dependency build/install
- `extras/codex-boot/`: temporary Bash rollback implementation for one compatibility release

MoonBridge remains an external, pinned dependency:
[JulianAbeleda/moon-bridge-arkey](https://github.com/JulianAbeleda/moon-bridge-arkey).
Arkey v3 does not contain or depend on the Arkey v2 runtime snapshot.

## Menu

```text
TUI
  Arkey Codex (modded)
Config
  Local
    tinygrad     (in development; unavailable)
    llama.cpp
      installed GGUF models
      Enter loads · d unloads the active model
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
`~/.local/libexec/arkey/moonbridge`, and installs Arkey into `~/.local/bin`.

Generated configuration is stored outside the repository. Arkey writes
`~/.config/arkey/config.toml`; MoonBridge uses
`~/.config/arkey/moonbridge.yml` (or preserves an existing
`~/moon-bridge/config.yml`). Configuration and state files are mode `0600`, and
application state/logs live under `~/.local/state/arkey`. Models, API
credentials, Codex state, sessions, logs, and machine-specific GPU state are
never stored in this repository.

See [`extras/codex-boot/README.md`](extras/codex-boot/README.md) for behavior,
commands, and local runtime details.

The implemented architecture and remaining compatibility-cutover work are
defined in
[`docs/bubble-tea-migration-scope.md`](docs/bubble-tea-migration-scope.md).

## Validate

```bash
scripts/install.sh --check
extras/codex-boot/test.sh
go test ./...
```
