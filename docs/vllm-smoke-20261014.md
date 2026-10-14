# vLLM connection smoke — 2026-10-14

Historical connection-only smoke. The [managed lifecycle refactor](local-engine-lifecycle-20261014.md) supersedes its startup/unload limitations.

Arkey v3 now exposes Config → Local → vLLM. This connects to a separately managed
loopback server; it does not start or stop model processes. llama.cpp retains its
existing managed lifecycle and saved GGUF selection. Stop the external vLLM server
before loading llama.cpp on a GPU without room for both.

Tested on RTX 5090 with the local Qwen3.8-27B-W4A16-AWQ checkpoint, vLLM 0.31.0,
BF16 KV, 32,768 context, one sequence, batch budget 2,048, GPU utilization 0.90,
Qwen tool/reasoning parsers and thinking disabled. The temporary server ran under
BoltBeam's bounded system slice, prebuilt-helper guard, swap disabled and a 30-minute
runtime limit. Commands, server/memory logs and smoke outputs are local at
`/home/ubuntu/storage/arkey-vllm-smoke-20261014/`; client logs stay outside Git.

## Checks

- Actual terminal UI: select vLLM, verify connected state and persisted route.
- Config checks: reject non-loopback URLs, wrong served model, malformed model
  catalogs and a configured context exceeding the server's advertised limit.
- MoonBridge streaming Chat Completions: `ARKEY_VLLM_OK`.
- Tool round trip: model called `get_marker`; after a supplied tool result it
  returned `ARKEY_TOOL_ROUNDTRIP_OK`.
- MoonBridge streaming Responses: `ARKEY_RESPONSES_OK`, with response completion.
- Actual `arkey --no-boot exec` via the snapshotted Codex client: `ARKEY_CLIENT_OK`.
- Actual client file-read task: blocked by the host's Codex sandbox, which returned
  `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted`. This is not a passing
  shell-tool smoke. The model reported the failure instead of inventing file contents.
- `go test ./...` and `go vet ./...` passed. The patched pinned MoonBridge server
  package tests passed during its dependency build.

## Compatibility fixes discovered by the smoke

The first actual-client attempt failed because Codex omitted an output limit and
MoonBridge used its global 65,536-token default against a 32K server. Arkey carries
`dependencies/moonbridge-local-output-budget.patch` against pinned MoonBridge
`df314b48b5e8b0099ee1bc9d3a1dc3f7a6b06031`. For local routes with known output limits,
an omitted budget uses the smaller of that limit and the global default. Explicit
budgets and hosted-provider behavior remain unchanged. The installer applies and
tests this patch in an exported build tree, leaving the dependency checkout clean.

The second attempt exposed Qwen3.8's rejection of `reasoning_effort=high` from the
saved Codex configuration. The vLLM route has a configurable reasoning effort,
defaulting to `medium`; explicit client CLI overrides win. Different checkpoints
may need different settings. Local Codex model metadata includes the vLLM route.

This establishes basic connectivity, streaming and API tool round trips. It does
not establish coding quality, long-session stability, or successful local shell
execution on this host. See the README for the configuration fields and matching
MoonBridge route. The temporary smoke server is not an installed auto-start service.
