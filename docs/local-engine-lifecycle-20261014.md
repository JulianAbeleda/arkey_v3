# Shared local-engine lifecycle — 2026-10-14

This supersedes the connection-only vLLM setup in the [initial smoke](vllm-smoke-20261014.md).
Both llama.cpp and vLLM now use Config → Local → engine → installed model, Enter to
load, `r` to refresh and `d` to unload. Client launch reloads a stopped saved model.

## Shared implementation

`internal/runtime/controller.go` owns lifecycle sequencing, operation locking, process
ownership, health waits, cleanup and rollback. `Engine` adapters supply validation,
arguments, acceleration checks, health checks, service policy and optional safe process
recognition. llama-specific arguments live in `llama.go`; vLLM's are in `vllm.go`.
A single state file records the active engine and complete launch configuration, so
switches release the previous model and rollback restores its engine, port, template,
arguments and environment. Explicit CLI route overrides also use that route's client
context/output/reasoning settings.

The UI uses one model screen and discovery interface for both engines. GGUF and local
Hugging Face checkpoint discovery differ behind that boundary. vLLM discovers directories
with config metadata and safetensors weights; checkpoint quantization is left to vLLM.
Neither checkpoint names nor AWQ/NVFP4 names determine the launch path. GPU auto-scan
also works without a llama executable installed.

vLLM uses an Arkey-owned systemd system service, running as the current user, outside
the desktop's user-session cgroup. It has a hard memory cap, no swap, whole-group cleanup
and no automatic restart loop. There is no unbounded fallback. The cap is clamped to
75% of physical RAM and available RAM minus 2 GiB, rechecked when starting the service.
This is managed serving: there is no temporary smoke timeout on the installed service.
The tested installation retained the prebuilt-helper guard and its existing CUDA caches
through the configurable environment. It does not compile/install a new engine on selection.

## Actual terminal and GPU checks

RTX 5090, local Qwen3.8-27B checkpoints. vLLM 0.31.0 used 32,768 context, BF16 KV,
one sequence, 2,048 batched tokens and 0.90 GPU utilization. llama.cpp used the user's
existing executable and automatic sizing: 262,144 context with q8_0 KV. This is a
lifecycle smoke, not a matched performance comparison.

| Check | Result |
|---|---|
| Stop the prior externally started test server, select AWQ in the actual TUI | Arkey started the model and displayed loaded |
| Streaming Chat, streaming Responses and tool-call/result round trip on managed AWQ | Passed |
| Press `d` on the loaded AWQ model | Service stopped; GPU process released; selection retained |
| Select llama.cpp Q4_K_M 27B | Loaded and returned `ARKEY_LLAMA_MANAGED_OK` |
| Select NVFP4 while llama.cpp was loaded | llama service stopped; bounded vLLM service started and became healthy |
| Managed NVFP4 streaming Chat, Responses and tool round trip | Passed |
| Switch from vLLM back to llama.cpp | vLLM stopped; GGUF model loaded |
| Force a vLLM startup failure while Q4_K_M 27B was active | Error reported; previous llama model restored and healthy |
| Switch from restored llama.cpp to AWQ using the final managed path | Passed |

The live rollback test used an isolated config copy with an invalid vLLM argument.
The previous llama PID was 620755; the restored process was 621174, serving the same
Qwen3.8 Q4_K_M file. The actual user config was not changed by this injected failure.
The final AWQ service's observed hard cap was 25,177,777,152 bytes, with swap disabled;
its observed peak at readiness was 22,544,084,992 bytes. These are host-cgroup values,
not GPU allocation measurements.

The llama smoke caught an existing startup-log compatibility issue: this pinned build
emits ggml GPU initialization at verbosity 4, while default verbosity 3 omits the text
Arkey checks. The llama adapter now requests level 4, preserving acceleration checking.
The first attempt was correctly rejected and cleaned up; subsequent loads passed.

## Verification and limits

`go test -race ./...` and `go vet ./...` passed. Darwin/arm64 packages compiled using
`GOOS=darwin GOARCH=arm64 go test -exec /usr/bin/true ./...`; this is compile coverage,
not a macOS runtime test. Managed vLLM is Linux-only and refuses unavailable containment.
Tests cover engine switching, rollback to the previous full configuration, unmanaged-port
refusal, required-service refusal, model discovery, parser selection, model/context health,
persistence, unload, route-specific client budgets and memory-limit command construction.

Raw commands/state snapshots, API responses, failure logs and verification logs are local
at `/home/ubuntu/storage/arkey-managed-smoke-20261014/`. Client logs remain outside Git.
The earlier Codex shell-tool failure (`bwrap: loopback: Failed RTM_NEWADDR`) is a separate
host sandbox limitation and was not bypassed. API tool round trips pass; this record does
not claim that the sandbox issue or long-context CUDA graph issue is fixed.

## Integration with concurrent main changes

Before publishing, origin/main had advanced to `6c3c68d`. The merge retains the
network Server route, Apple Silicon detection and unified-memory sizing, shared
llama slot storage, and the updated Codex routing. Local engine adapters now carry
the llama slot path through launch matching and rollback, and route-specific client
budgets include Server as well as llama and vLLM. The Linux GPU fallback uses named
fields to accommodate the added unified-memory flag.

The merged tree passed `go test -race ./...`, `go vet ./...`, and the Darwin/arm64
compile check above. The local output-budget patch applies to the newer MoonBridge
pin `e11d9b8be1a3ddbb5515b1486f0abad6110a6308`; its server-package tests passed in an
exported build tree. A merged Arkey binary returned `ARKEY_MERGED_OK` through Codex
using the existing AWQ service (PID 622241), without reloading the model. This
additional smoke does not repeat the cold-load matrix or test macOS at runtime.

## Auto context and Kimi fixes (later on 2026-10-14)

`vllm_context_size = 0` now requests vLLM's native `--max-model-len auto`.
The engine profiles available GPU memory and sizes its actual KV cache; Arkey
reads the matching served model's `max_model_len` after health checks. Runtime
state records that actual window separately from the zero-valued launch request.
Client planning happens after startup, and MoonBridge route metadata and the
Codex catalog receive the resolved window. Positive context values remain pins.
This uses the installed vLLM 0.31.0 auto-fit support, documented in
[vLLM's model configuration](https://docs.vllm.ai/en/latest/api/vllm/config/).

On this RTX 5090, the selected Qwen3.8-27B NVFP4 model auto-fitted from its 262,144
model limit to **94,864 tokens** with 90% GPU utilization, BF16 KV, one sequence,
and 2,048 max batched tokens. This is a capacity result for those settings, not a
maximum-context benchmark. The bounded service retained its 25,177,777,152-byte
host-memory cap; measured startup peak was 4,065,591,296 bytes. The API and saved
state agreed on 94,864, while the launch request remained zero. Reopening Arkey
reused PID 637170. Kimi used 94,864 context and 23,716 reserved output tokens.

Kimi had two independent failures:

- Its default response reserve produced a request for 32,768 output tokens
  against the original 32,768 total context. The server rejected it with HTTP
  400. Arkey now sets `loop_control.reserved_context_size` and model output
  metadata from one shared budget: one quarter of context, capped at 32,768.
  Small windows receive a smaller reserve; no model or quantization names are
  involved. Kimi's actual outgoing 32K-window request was verified to contain
  `max_output_tokens: 8192`.
- MoonBridge announced tool items with a call ID but sent argument deltas using
  a different `fc_item_*` ID. Kimi rejected these as unknown stream indexes.
  Streaming calls also started with the completed JSON string `{}` instead of
  empty arguments. `dependencies/moonbridge-tool-stream.patch` makes item IDs
  consistent and leaves the initial arguments empty for incremental assembly.
  This is a protocol correction for all models, including interleaved calls.
  The installer applies it to the pinned MoonBridge source and runs the adapter
  regression tests before installing. No Kimi binary patch is needed.

Live verification: Kimi returned `ARKEY_KIMI_OK` at the original 32K server
window before auto-sizing. At the auto-sized window it returned
`ARKEY_KIMI_AUTO_OK`. After the stream fix, its `Read` tool opened a generated
fixture and it returned `maple-4827`; the tool round trip also passed with a
32K client window and 8,192-token reserve against the auto-sized server.
Codex also returned `ARKEY_CODEX_AUTO_OK` with the resolved context.
These are short response/tool smokes, not long-session or full-context tests.

Arkey race tests, vet, and Darwin/arm64 compile checks passed. MoonBridge's
protocol packages and server tests passed. Its optional `-tags=e2e` suite has
two baseline failures (`TestGoogleGenaiE2E_ToolUseRoundTrip` and
`TestPluginHooks_MultiplePlugins`), reproduced on the clean pinned source as
well as the patched tree. They are outside these changes.
Raw smoke/config/request-budget records remain private on this box under
`/home/ubuntu/storage/arkey-autoctx-kimi-20261014/`.
