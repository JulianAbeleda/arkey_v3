# Arkey v3

Arkey v3 is a lean boot manager for **Arkey Codex**, our modified Codex-derived
TUI. It selects a local or frontier AI route, starts the required local runtime,
and connects the TUI through MoonBridge.

Arkey Codex is not the official Codex client. WezTerm is not part of this app;
its boot manager was only an interaction reference.

## What is included

- `extras/codex-boot/`: boot menu, routing, GPU scan, and local llama.cpp runtime
- `dependencies/moonbridge.env`: the exact MoonBridge fork revision used by Arkey
- `scripts/install-moonbridge-dependency.sh`: verified dependency build/install

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
  Frontier
    DeepSeek
    Codex
    Claude
  GPU Auto-scan
Exit
```

## Install

```bash
extras/codex-boot/install.sh
```

The installer requires `git` and `go` while installing MoonBridge. It builds
the pinned fork into `~/.local/libexec/arkey/moonbridge` and installs the Arkey
launchers into `~/.local/bin`.

The generated configuration is stored outside the repository at
`~/.config/arkey/moonbridge.yml` with mode `0600`. Models, API credentials,
Codex state, sessions, logs, and machine-specific GPU state are never stored in
this repository.

See [`extras/codex-boot/README.md`](extras/codex-boot/README.md) for behavior,
commands, and local runtime details.

The planned migration from the Bash application to a Go UI built with Bubble
Tea, Bubbles, and Lip Gloss is defined in
[`docs/bubble-tea-migration-scope.md`](docs/bubble-tea-migration-scope.md).

## Validate

```bash
extras/codex-boot/install.sh --check
extras/codex-boot/test.sh
```
