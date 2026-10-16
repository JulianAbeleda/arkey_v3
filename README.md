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
- `dependencies/moonbridge-tool-stream.patch`: consistent tool-call stream IDs and incremental arguments
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

On Apple Silicon, local models use llama.cpp's Metal backend. Selecting a model
automatically configures the GPU and runtime when they have not been configured.
Arkey checks `llama-server` on PATH before scanning source checkouts; a Homebrew
installation is supported. It verifies that llama reports an Apple Metal device
and that model layers were offloaded before saving the local selection.

## Model names and context

Before launching a local Codex route, Arkey refreshes its catalog display name
from the selected checkpoint: GGUF `general.name`, Hugging Face `_name_or_path`,
or the checkpoint filename/directory when metadata is absent. No model-family
name is hardcoded. The internal `arkey-local-*` route IDs remain stable.
The current copied Codex binary still prints the route ID in its footer; using
the catalog display name there requires a client display change.

vLLM uses `local.vllm_context_size = 0` by default: Arkey passes
`--max-model-len auto`, and vLLM profiles GPU memory and fits the KV cache within
its memory-utilization budget and the model's supported context. A positive value
of at least 4,096 pins the window. Existing positive settings remain pinned.
After startup, Arkey reads the served model's actual `max_model_len` from
`/v1/models` and uses it for MoonBridge, client launch budgets, and catalog metadata.
It preserves zero in the requested launch config so reopening Arkey reuses the
healthy auto-sized process. This requires a vLLM version supporting auto-fit.
llama.cpp uses `local.context_size`; zero derives a window from model metadata,
GPU memory, and the configured KV-cache precision. The client receives the same
resolved context as its engine. Catalog metadata reserves 10% of that context,
and the client output budget is one quarter of the window, capped at 32,768
tokens. Kimi's `loop_control.reserved_context_size` follows this budget too;
leaving its larger default reserve can make it request the entire context as
output. The same calculation applies to every engine and model. The reasoning
label such as `medium` is a separate setting.

### Multiple GPUs

`local.gpus = 0` (the default) splits a local model across every GPU the hardware
scan finds, for both engines; a positive value caps the count, and `1` keeps a model
on the first GPU. vLLM receives `--tensor-parallel-size`, reduced if needed to the
largest count that divides the checkpoint's attention heads; an explicit
`--tensor-parallel-size` in `vllm_args` wins. llama.cpp is pinned to the same
devices with `--device` (`ROCm0,ROCm1`, `CUDA0,CUDA1`, ...), and its derived context
is sized against those devices' pooled VRAM, counted at the smallest card's size
because a split fills every card equally. Changing the count relaunches the engine.
On two Radeon AI PRO R9700s, Qwen3.8-27B AWQ under vLLM went from a 130,928-token
auto-fit window at 29-31 tok/s on one card to the full 262,144 at 46-53 tok/s on
two.

## Shared local inference

Arkey owns the local llama-server used by both GameTerm and the coding
clients. `~/.config/arkey/config.toml` is the authority for its selected
model, binary, port and context; MoonBridge's local route is projected from
that configuration when the service is ensured.

Native clients use `arkey --local-runtime` for a credential-free JSON
descriptor, or `arkey --ensure-local-runtime` to start/reuse the service and
return the descriptor. The `arkey.local-runtime.v1` object contains `origin`,
`model`, `context_size` and `slots_path`. These commands do not launch a TUI
or change the selected client/route. Repeated ensures use the existing
cross-process lock and verified process identity; they do not replace an
unmanaged listener. GameTerm no longer owns or stops llama-server.

The server uses one slot, quantized KV, no separate RAM prompt cache, and
Arkey-managed disk slot storage scoped to the selected model and context.
The isolated Codex home defaults to MoonBridge; exec-level configuration
flags are kept together so an effort override cannot discard its route.
Clients share model memory and serialize
requests; a client switch may require a cold prompt evaluation.

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

The provider is written with `local: true`. That flag is not about where
the server runs; it tells MoonBridge the upstream is a llama.cpp-style
server, so it streams reasoning in the shape Codex reads. Without it Codex
logs `ReasoningSummaryDelta without active item` and drops the thinking.

### Thinking, per request

A llama.cpp server has one thinking switch and it is binary: the template's
thinking branch runs or it does not. It ignores `reasoning_effort`.
MoonBridge translates, so every client above it speaks one vocabulary:
`minimal`, `none` and `off` mean no thinking, any other level means
thinking, and no effort at all leaves the server's own `--reasoning`
default alone. In Arkey Codex it is a two-position switch in `/model`, named for what it
does rather than for a level that would mean nothing on a llama.cpp server:

```
Select Reasoning Level for arkey-server-llama
  1. Off   The model answers straight away.
› 2. On    The model works the problem through first, and takes longer.
```

Codex prints the effort string as the label and accepts names outside its
own low/medium/high vocabulary, so the menu can say Off and On. Arkey
writes both entries into the Codex model catalog; MoonBridge folds the case
and reads `off` as no thinking. Measured against Ubuntu on a question that needs
real reasoning: about 10 s at `minimal`, about 43 s at `high`.

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

For npm-installed Crush, the snapshot script resolves `run-crush.js` to the
package's native `bin/crush`; the private copy does not depend on the npm
launcher's relative imports. Select it through **TUI → Arkey Crush**.
npm-installed Codex is resolved the same way, from `bin/codex.js` to the
native binary in its per-platform `@openai/codex-*` package.

Crush cannot use the Codex frontier: MoonBridge converts the Chat Completions
ingress into Anthropic, Google GenAI and OpenAI Chat upstreams, but an OpenAI
Responses upstream is only reachable by verbatim passthrough from the Responses
ingress. Arkey refuses that combination before launch rather than failing on
every request.

Claude is snapshotted and isolated but intentionally unavailable in the menu
until the pinned MoonBridge fork provides Anthropic Messages ingress; Arkey does
not bypass that missing protocol boundary by modifying or redistributing Claude
Code.

### Client sandboxes

Codex and Claude Code run model commands inside a bubblewrap (`bwrap`) sandbox
built from user namespaces. Ubuntu 24.04 restricts those to programs with an
AppArmor profile that allows them
(`kernel.apparmor_restrict_unprivileged_userns = 1`); without one every
sandboxed command fails with "Operation not permitted" and the client asks to
rerun it unsandboxed. `scripts/install-sandbox-dependency.sh` (run by the
installer; `ARKEY_SKIP_SANDBOX_INSTALL=1` skips it) installs
`/etc/apparmor.d/arkey-bwrap`, which allows user namespaces for the root-owned
system `bwrap` and nothing else, then proves bwrap can create one. It needs
`sudo -n`, does nothing where namespaces are unrestricted, and defers to the
distribution's `bwrap-userns-restrict` profile where that exists. Claude Code's
sandbox also needs `socat`. Kimi and Crush ask for approval in-app and do not
sandbox through bwrap.

Arkey launches Codex with `--sandbox workspace-write` only as a default: any
sandbox or approval flag in the launch arguments, a profile, or `sandbox_mode` /
`default_permissions` in `~/.codex-moonbridge/config.toml` replaces it, and
Codex's `/permissions` choice applies for the session.

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
vllm_context_size = 0 # let vLLM fit the context to GPU memory
vllm_reasoning_effort = "medium"
vllm_memory_mib = 24576
vllm_args = []
vllm_environment = {} # optional CUDA/cache/build-guard environment
gpus = 0 # split across every detected GPU; a positive value caps it
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
