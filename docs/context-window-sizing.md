# Arkey v3 context-window sizing scope

Status: not started; scope only

Scoped: 2026-07-30

Target: Codex first (dead code already present), then Kimi/local-window
honesty, then hardware-derived local sizing. Claude is documented but
unexercisable until MoonBridge Anthropic ingress ships.

## 1. Problem

Arkey tells its clients a context window that does not match the window the
local model actually has, and for two of three clients it tells them nothing
at all.

`internal/control/services.go:354` `ClientContextWindow()` returns
`cfg.Local.ContextSize` when `mode == "local"`, else a hardcoded `262144`. It
is wired into exactly one call site: `kimi.WriteConfig` at
`cmd/arkey/main.go:126`. Codex and Claude never receive it.

Observed failure on the user's workstation: `llama-server` runs
`--ctx-size 32768` while `~/.codex-moonbridge/config.toml` declares
`model_context_window=1000000` and `model_max_output_tokens=384000` (Codex's
own persisted config, which Arkey does not write and does not override at
launch today). Sessions peaked at 29,457 tokens against a real 32,768-token
KV cache; llama.cpp logged 280 "erased invalidated context checkpoint"
warnings and one `state_seq_set_data: error loading state: failed to restore
kv cache`. Multi-turn sessions degrade after the first few tool calls because
Codex plans compaction against a window ten times larger than the one that
exists.

A separate, already-fixed contributing factor: `llama-server` was running
with `n_parallel=4` (auto-selected), which caused slot thrashing. Arkey now
pins `--parallel 1` in `internal/runtime/controller.go` `llamaArgs` (see the
comment at controller.go:356-360 explaining the qwen35-arch segfault this
also avoids). That fix is already shipped and is not part of this scope.

## 2. Current wiring

`internal/control/services.go` `ClientContextWindow()` returns
`cfg.Local.ContextSize` for `mode == "local"` and a hardcoded `262144`
otherwise. Before Phase 1 it had exactly one call site,
`cmd/arkey/main.go` for `kimi.WriteConfig` — Codex never received it, which is
why a hand-written `model_context_window = 1000000` in
`~/.codex-moonbridge/config.toml` went unchallenged while llama-server ran at
32768.

`internal/kimi/config.go` `WriteConfig(stateHome, bridgeURL, bridgeToken,
model, contextWindow)` already consumes the value (`max_context_size` in the
generated `config.toml`), clamped to a 32768 fallback when `contextWindow < 1`.
Nothing to change there beyond making the value it receives honest (Phase 2).

Claude is out of scope for this document: `internal/control/services.go`
gates it behind MoonBridge Anthropic ingress that does not exist, so any
context-window work on that path cannot be exercised or tested. Revisit when
the ingress lands. Two facts recorded from the snapshotted binary so the work
does not start from zero: `CLAUDE_CODE_MAX_OUTPUT_TOKENS` is a real, verified
environment variable; `CLAUDE_CODE_MAX_CONTEXT_TOKENS` appears in the binary's
registered variable table but its effect is unverified. Claude Code has no
`-c key=value` launch-override mechanism — environment variables only.

## 3. Phase 1 — stop lying to Codex

### 3.1 Change

In `cmd/arkey/main.go`, pass `services.ClientContextWindow()` and
`services.ClientMaxOutputTokens()` (both already implemented on `Services`,
`internal/control/services.go:354-374`) into `codex.BuildOptions` at the
existing `codex.Build(...)` call site (main.go:106-109). No change to
`internal/codex/command.go` is required — the override logic is already
correct.

`ClientMaxOutputTokens()` already derives `window/4` clamped to
`[4096, 32768]`; this matches the "DOCUMENT" instruction's formula exactly
and needs no rework.

### 3.2 Why launch-time `-c` overrides, not a config.toml write

- The correct value is route-dependent (local vs. frontier, and which
  frontier backend) and is only known once `SelectClient`/`SelectFrontier`
  and model resolution have run for *this* launch — Arkey does not persist
  a value it would have to invalidate on every route change.
- Writing `~/.codex-moonbridge/config.toml` would mean Arkey mutates a file
  Codex treats as the user's own persisted configuration. A user who hand-sets
  `model_context_window` there is expressing intent Arkey must not overwrite
  silently on the next launch.
- `-c key=value` is Codex's documented one-shot override mechanism
  (`internal/codex/command.go` already uses it for `model=` and
  `MoonBridgeProvider`); it composes with `hasClientArg` to detect
  user-supplied values and defer to them, and it leaves no residue after the
  process exits.

### 3.3 User-supplied args must win

`hasClientArg(opts.Parsed.ClientArgs, "model_context_window")` (and the
`model_max_output_tokens` equivalent) must gate the injection exactly as
`model=` and `model_provider=` already do. If the user passed either flag on
the command line, Arkey must not add its own. This is already implemented in
`codex.Build`; Phase 1 must not regress it and must add an explicit test that
a user-supplied `-c model_context_window=N` survives Arkey's injection
unchanged.

### 3.4 Acceptance criteria

- `codex.Build` receives a nonzero `ContextWindow`/`MaxOutputTokens` from the
  real `main.go` call path for both local and frontier routes (an
  integration-level test, not just the existing unit test on `codex.Build`
  in isolation).
- A user-supplied `-c model_context_window=...` or
  `-c model_max_output_tokens=...` on the Arkey command line is never
  duplicated or overridden.
- Local-mode launches produce `model_context_window` equal to
  `cfg.Local.ContextSize` (32768 by default today), not the frontier
  262144 default.
- No change to `internal/claude/command.go` or `internal/kimi/config.go`
  behavior (Kimi already receives this value; see §2).

## 4. Phase 2 — honest frontier windows

### 4.1 Problem with the current constant

`262144` in `ClientContextWindow()` is applied to every non-local route
uniformly: DeepSeek, Codex-as-a-frontier-backend, and Claude all get the same
number regardless of the actual routed model's real window. `frontierModel()`
(`internal/control/services.go:429-440`) already resolves the concrete model
per backend (`deepseek-v4-pro`, `gpt-5.6-sol`, `claude-sonnet-4-6`, each
overridable by env var) — the context-window lookup should key off that same
resolved model, not off `mode` alone.

### 4.2 Preferred source of truth: MoonBridge's catalog

`internal/moonbridge/client.go` already fetches `/v1/models` and decodes it
into:

```go
type Model struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Slug  string `json:"slug"`
    Model string `json:"model"`
}
type Catalog struct{ Models []Model }
```

**This struct carries no context-window field today.** Before Phase 2 can
treat the catalog as a source of truth, the actual `/v1/models` response
body from a running MoonBridge instance must be inspected to confirm whether
it already includes a context-length field under a different key (e.g. an
OpenAI-style `context_length` on some routed models) that this typed decode
is silently dropping, or whether MoonBridge's catalog genuinely has no such
field and would need an upstream change. Do not assume either answer without
looking at a live response; `Catalog.HasRoute` matching (`ID`/`Name`/`Model`/
`Slug`, including suffix match on `Slug`) shows the shape is already looser
than a single canonical key, which is exactly where an undocumented extra
field could be hiding.

### 4.3 Fallback: a static per-backend table

If the catalog does not carry context-window data, fall back to a small
table keyed by the resolved model string from `frontierModel()`:

```text
deepseek-v4-pro   -> <verify against DeepSeek's published window>
deepseek-v4-flash -> <verify against DeepSeek's published window>
gpt-5.6-sol       -> <verify against the routed Codex-frontier window>
claude-sonnet-4-6 -> <verify against Anthropic's published window; see §6>
```

None of these numbers should be invented for the implementation PR; each
must be confirmed from the provider's current published model card at
implementation time, because frontier model windows change between dated
scopes like this one.

### 4.4 Acceptance criteria

- `ClientContextWindow()` returns a value that depends on the resolved model
  (`frontierModel(cfg.Frontier.Backend)`), not a single constant for all of
  `mode != "local"`.
- The catalog-response shape has been captured (a fixture) and either (a)
  used as the live source with a documented field name, or (b) explicitly
  ruled out with a comment stating what was checked.
- The static fallback table has a single definition site and unit test
  coverage per entry; it is not duplicated in `frontierModel` and the
  window lookup.
- Unknown/unrouted models fail closed (no context-window override injected)
  rather than emitting a guessed number.

## 5. Phase 3 — compute the local window from hardware

### 5.1 Problem with the current local number

`cfg.Local.ContextSize` (default 32768, `internal/config/config.go:51`) is a
static config value with no relationship to the GPU actually running the
model. It was hand-set for the box measured in this scope and does not
generalize: a smaller GPU can silently OOM at load, a larger one leaves
context on the table that Phase 1/2 will now honestly under-report.

### 5.2 Measured baseline (this box, RTX 5090, 32,607 MiB total)

Qwen3.6-27B-Q4_K_M: ≈17.1 GB resident model weight; total footprint at
32k context is 19,979 MiB, i.e. KV cache costs roughly **87 MB per 1k tokens
at f16**.

### 5.3 Formula

```text
kv_bytes_per_token = 2 * n_layers * n_kv_heads * head_dim * bytes(cache_type)
ctx = (total_vram - model_bytes - margin) / kv_bytes_per_token
```

The leading `2` is K and V. `bytes(cache_type)` is 2 for f16 (llama.cpp
default) and 1 for q8_0.

### 5.4 Metadata source: GGUF header, not a guess

`n_layers`, `n_kv_heads`, and `head_dim` must come from the GGUF file's own
key-value header, not from a hardcoded per-model-family table:

- `<arch>.block_count` → `n_layers`
- `<arch>.attention.head_count_kv` → `n_kv_heads`
- `<arch>.attention.key_length` → `head_dim`

GGUF is a documented binary key-value format (magic, version, tensor count,
KV count, then typed KV pairs, then tensor descriptors) — this is a header
read, not a full-tensor load, and does not require linking llama.cpp or any
GGML code into Arkey.

`internal/models` (`discover.go`, `metadata.go`) already owns model
discovery and is the natural home for a small GGUF header parser
(`internal/models/gguf.go` or similar): it already walks the model roots and
has the file paths in hand, so header parsing composes directly with
`Discover()` instead of introducing a second file-walking owner.

### 5.5 Critical constraint: size against total VRAM, not free VRAM

**Compute from total VRAM minus model minus a fixed margin. Do not size
against free VRAM measured at launch.**

Sizing against free VRAM makes the derived window vary run-to-run — a
browser or another GPU client holding a few hundred MB changes the number
the next time Arkey starts the same model on the same GPU. That variance
breaks session resumption: a client (Codex, Kimi) that already compacted a
session against last run's window has no way to know this run's window
shrank, and reopening the session either silently truncates context the
client still believes is available or wastes budget it now has and doesn't
know about. Determinism per `(GPU, model)` pair is worth more than reclaiming
the last GB of transient free memory. `total_vram` should be queried once
per GPU (already known from `internal/gpu` detection) and the margin should
be a fixed, documented constant (e.g. covering CUDA/ROCm context overhead,
other resident processes' typical footprint, and headroom for graph/compute
buffers), not measured at request time.

### 5.6 Cheap lever: q8_0 KV cache

`--cache-type-k q8_0 --cache-type-v q8_0` roughly halves
`kv_bytes_per_token` (1 byte vs. 2 per element), which is roughly a 2x
increase in derived context for the same VRAM budget. This is a llama-server
launch flag change in `internal/runtime/controller.go` `llamaArgs`
(controller.go:361-363), independent of the GGUF-parsing work, and should be
evaluated for perplexity/quality impact before being made the default —
this scope only records it as an available lever, not a decision to flip it.

### 5.7 Acceptance criteria

- A GGUF header parser reads `block_count`, `attention.head_count_kv`, and
  `attention.key_length` (or the correct per-architecture key names —
  verify against the architectures actually in `~/models`, since key names
  are namespaced by `<arch>.*` and Arkey supports more than one) without
  reading tensor data.
- `cfg.Local.ContextSize` becomes derived-with-override: hardware-derived by
  default, but an explicit user-set `context_size` in `config.toml` still
  wins (config already distinguishes "present" from "absent" only weakly —
  this needs a real decision, e.g. a sentinel or a separate
  `context_size_auto` bool, made explicit in the implementation PR).
- The derived value is computed from `total_vram`, a per-GPU constant fixed
  at detection time, never from free VRAM sampled at launch.
- The margin constant is named and documented in code, not a bare number.
- Switching between two GGUF files with different `n_layers`/`n_kv_heads`
  on the same GPU produces different, correctly-ordered derived windows in
  a unit test with fake GGUF fixtures (no real multi-GB model required in
  CI).
- q8_0 KV cache type is a distinct, separately-flagged change from the
  sizing formula and ships (if at all) in its own PR.

## 6. Claude Code context-window configuration — investigated, not guessed

`internal/claude/command.go` `Build()` configures Claude purely through
environment variables: `CLAUDE_CONFIG_DIR`, `ANTHROPIC_BASE_URL`,
`ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`,
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`. There is no `-c key=value`
launch-override mechanism analogous to Codex's.

Claude is gated off entirely today:
`internal/control/services.go:187` returns
`"Arkey Claude is snapshotted, but MoonBridge Anthropic ingress is not
implemented yet"` whenever `ValidateClient("claude")` is called. Anything
below is therefore unexercisable until that gate is removed.

### 6.1 What was verified

The Claude Code binary snapshotted at
`~/.local/libexec/arkey/clients/claude/claude` (275 MB, referenced by
`ClaudeBinary` in `internal/control/services.go:90`) was inspected directly
with `strings`. Confirmed present in the binary:

- `CLAUDE_CODE_MAX_OUTPUT_TOKENS` — a registered integer environment
  variable with a per-model default and upper limit (looked up via a
  `lst(modelName)` table keyed by model id, e.g. `"claude-opus-4-6"`). The
  binary's own error text confirms its effect: `"Claude's response exceeded
  the <N> output token maximum. To configure this behavior, set the
  CLAUDE_CODE_MAX_OUTPUT_TOKENS environment variable."` This is the real
  analog of Arkey's `ClientMaxOutputTokens()`/`model_max_output_tokens`, and
  it is settable via environment, which `internal/claude/command.go` already
  has a mechanism for (`client.SetEnv`).
- `CLAUDE_CODE_MAX_CONTEXT_TOKENS` — a registered integer environment
  variable name exists in the binary's environment-variable table. **Its
  runtime effect is unverified from static strings alone** — it was found
  registered alongside `CLAUDE_CODE_AUTO_COMPACT_WINDOW`,
  `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE`, `DISABLE_AUTO_COMPACT`, and
  `DISABLE_COMPACT`, which are compaction-trigger controls, not necessarily
  controls over the context window the API actually serves. Do not treat
  this as equivalent to Codex's `model_context_window` until its effect is
  confirmed (e.g. by running Claude Code with it set against a
  request-logging proxy and observing whether the API-facing context budget
  actually changes, versus only the local compaction threshold changing).
- `anthropic-beta: context-1m-2025-08-07` (internal feature name
  `long_context`) — a real Anthropic API beta header string is present in
  the binary. This is the documented mechanism by which some Claude models'
  effective context window is extended (to roughly 1M tokens) at the API
  level; it is a request header, not a CLI flag or config key, and would
  need to be threaded through MoonBridge's Anthropic ingress (not yet
  built) rather than through `internal/claude/command.go`.
- A `context_window_size` field appears in what is a statusline-script
  input schema (alongside `used_percentage`/`remaining_percentage`) — this
  is Claude Code reporting its current window to a user-configured
  statusline command, not an input a launcher can set.

### 6.2 What remains unverified

- Whether `CLAUDE_CODE_MAX_CONTEXT_TOKENS` changes the token budget sent to
  the API versus only a local compaction/warning threshold.
- Whether Claude Code exposes any `settings.json` key with the same effect
  (only environment variables were checked here; `~/.claude/settings.json`
  schema was not exhaustively enumerated).
- Whether the `context-1m-2025-08-07` beta is reachable through whatever
  Anthropic-compatible endpoint MoonBridge will eventually expose, or only
  through direct `api.anthropic.com` access.
- The real per-model context windows for whichever Claude model MoonBridge
  ends up routing as `claude-sonnet-4-6` (or its successor by the time
  ingress ships) — these must be pulled from Anthropic's current model
  documentation at implementation time, not from this scope.

### 6.3 Conclusion for this scope

Do not implement a Claude-side context-window override until (a) MoonBridge
Anthropic ingress exists so the route is exercisable at all, and (b) the two
unverified points above (`CLAUDE_CODE_MAX_CONTEXT_TOKENS` effect,
settings.json equivalents) are confirmed against a running Claude Code
instance with real network capture. Until then, Claude gets whatever
context window its model id defaults to at Anthropic, unmodified, and
Arkey's honesty problem does not apply to it because Arkey injects nothing
context-related for Claude today — Phase 1/2 are Codex/Kimi-only work.

## 7. Risks and controls

| Risk | Control |
| --- | --- |
| Phase 1 wiring regresses user-supplied `-c` overrides | explicit test: user-passed `model_context_window`/`model_max_output_tokens` survive unchanged |
| Phase 2 catalog assumption is wrong (no context field exists) | capture a live `/v1/models` fixture before writing the lookup; fail closed to the static table |
| Phase 2 static table goes stale as frontier providers change models | single definition site, dated comment, no duplication with `frontierModel` |
| Phase 3 GGUF key names differ per architecture | verify actual keys against every architecture present in `~/models` before hardcoding one key set |
| Phase 3 sizing formula OOMs at load despite passing sizing | keep `internal/runtime` health/acceleration checks and rollback (already implemented) as the final backstop, not just the formula |
| Phase 3 sizing drifts if computed against free VRAM | mandate total-VRAM-minus-model-minus-margin in code review; reject any PR reading free/available VRAM at launch time for this purpose |
| Session resumption breaks across a hardware-derived window change | derived window is a pure function of `(GPU, model)`; changing GPU or model is already a route-changing action the client must re-handshake for |
| Claude section is implemented on unverified assumptions | explicit unexercisable gate (services.go:187) remains until ingress ships; §6.2 items must be confirmed first |
| q8_0 KV cache silently becomes default and changes output quality | ship as a separate, explicitly reviewed PR, not bundled into the sizing formula |

## 8. Recommended PR sequence

1. `docs: define context-window sizing scope` (this file)
2. `feat: wire ClientContextWindow/MaxOutputTokens into codex.Build call site` (Phase 1; no `internal/codex` logic changes, main.go call site + tests only)
3. `feat: resolve frontier context window per routed model` (Phase 2 static table + tests)
4. `feat: source frontier context window from MoonBridge catalog` (Phase 2 catalog path, only after the response shape is captured; can be dropped if the catalog genuinely lacks the field)
5. `feat: add GGUF header parser to internal/models` (Phase 3 parser only, no wiring yet; unit tests on fixture headers)
6. `feat: derive local context_size from GGUF metadata and total VRAM` (Phase 3 wiring into `runtimeConfig`/`ActivateLocal`, config schema decision for auto-vs-explicit `context_size`)
7. `feat: add q8_0 KV cache type flag` (independent, explicitly reviewed for quality impact)
8. Claude work stays blocked behind MoonBridge Anthropic ingress; not sequenced here.

Each PR must be independently testable and must not touch `internal/claude`
until the ingress gate (`internal/control/services.go:187`) is removed in a
separate, unrelated change.
