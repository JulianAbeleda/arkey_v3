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
    vLLM
      installed Hugging Face checkpoints
      Enter loads · r refreshes · d unloads the active model
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

## Local engines

Config → Local → llama.cpp or vLLM opens that engine's installed-model list.
Enter loads the model, `r` refreshes discovery and `d` unloads the highlighted active
model. Launching a client starts the saved model if it is stopped. One shared controller
handles ownership, health, stop/start, cancellation and rollback. Switching engines
releases the old model before starting the new one; a failed start attempts to restore
the old engine and its exact saved launch configuration. Unmanaged servers are refused.

Engine adapters supply model validation, launch arguments, GPU checks, health probes and
service policy. llama.cpp discovers GGUF files and retains its existing GPU alignment and
context sizing. vLLM discovers local Hugging Face directories containing `config.json`
and safetensors weights. vLLM reads quantization from checkpoint metadata, so AWQ and
NVFP4 use the same model-selection flow.

### vLLM configuration

Configure these fields under `[local]` in `~/.config/arkey/config.toml`:

```toml
vllm_python = "/path/to/vllm-venv/bin/python"
vllm_model_dirs = ["/path/to/checkpoints"]
vllm_url = "http://127.0.0.1:8000"
vllm_model = "arkey-local-vllm" # served API name, not a checkpoint path
vllm_context_size = 32768
vllm_reasoning_effort = "medium"
vllm_memory_mib = 24576
vllm_args = []
vllm_environment = {} # optional CUDA/cache/build-guard environment
```

The UI saves the chosen directory as `vllm_checkpoint`; the previous GGUF selection
remains in `model`. The Python environment must already contain a compatible vLLM and
GPU stack. Arkey does not install packages or download models while loading.

Managed vLLM currently requires Linux systemd and noninteractive access to
`sudo -n systemctl` / `sudo -n systemd-run`. It runs as the current user in a separate
system slice, with a hard memory cap, swap disabled, whole-group cleanup and no automatic
restart loop. The cap is clamped to 75% of physical RAM and available RAM minus 2 GiB.
It never falls back to an unbounded launch. `MAX_JOBS` and `NINJA_NUM_JOBS` default to 1;
configure the normal prebuilt-helper guard and cache environment when using a guarded
vLLM installation. These settings are machine-local, not copied from a benchmark path.

Use a localhost/127.0.0.1 server root without `/v1`. The health check verifies the served
model and context limit. Configure the `arkey-local-vllm` route, `vllm-local` provider,
and `arkey-vllm` model from `dependencies/moonbridge.example.yml` in MoonBridge. Its
address, upstream name and limits must match these settings. Reload a running bridge
when changing its configuration.

Qwen3.5/3.8-family checkpoints receive the Qwen tool/reasoning parsers and thinking-off
chat-template default automatically, based on `config.json`, independent of quantization.
Other model families may need explicit `vllm_args`. Explicit parser arguments override
family defaults; model, address, served name and context flags remain owned by Arkey.

Codex uses `vllm_reasoning_effort` for this route; an explicit client CLI override wins.
Qwen3.8 accepts low, medium and xhigh, but rejects high. Adjust for another model or use
an empty string to retain client defaults. The pinned MoonBridge build includes
`dependencies/moonbridge-local-output-budget.patch`: for local routes, an omitted output
limit uses the smaller of the global default and the route's declared limit. Explicit
limits and hosted defaults are unchanged.

[Initial connection smoke and client limitation](docs/vllm-smoke-20261014.md).
[Managed loading, switching and refactor](docs/local-engine-lifecycle-20261014.md).
