# Nemotron NVFP4 load smoke — 2026-10-14

NVIDIA's `NVIDIA-Nemotron-3-Nano-30B-A3B-NVFP4`, revision
`6efb4a2a1c1fa277ce7b3df7a1416255011b1c99`, downloaded successfully (19.36 GB).
All 18 upstream file sizes and 24,147 indexed tensor entries were checked.
The installed vLLM 0.31.0 recognizes `NemotronHForCausalLM` without executing
checkpoint Python code.

On the RTX 5090 32 GB, Arkey's bounded service loaded the model with BF16
compute, FP8 KV, automatic context, 90% GPU memory utilization, one sequence,
and 2,048 maximum batched tokens. The full configured model limit of **262,144
tokens** fitted. The allocator's 2,632,516 cache slots do not permit requests
beyond that model limit. No full-context request was tested.

Extra launch arguments for this smoke were:

```text
--dtype bfloat16 --kv-cache-dtype fp8
--enable-auto-tool-choice --tool-call-parser qwen3_coder
--reasoning-parser nemotron_v3
--default-chat-template-kwargs '{"enable_thinking":false}'
```

These were explicit test-profile settings, not a change to Arkey's parser
inference rules. Parser choices must match the selected model. The model card's
older custom reasoning-parser plugin is unnecessary with this vLLM's built-in
`nemotron_v3` parser. Reasoning-on behavior was not tested.

The first cold launch reached model loading but spent its startup interval
compiling FlashInfer's `fused_moe_120`. That attempt did not pass readiness.
An explicit stop around failure/rollback left the endpoint offline until the
retry; this is not recorded as a successful rollback. We installed the official
`flashinfer-jit-cache==0.7.0.post1+cu130` shim and its exact-version architecture
provider dependencies, matching the existing FlashInfer version. The retry
passed initialization and saved **96 autotuning configs**. A second-process
cache-hit test has not been performed. The core vLLM/FlashInfer versions were
not upgraded, and no engine source was changed.

Model loading reported **18.66 GiB** GPU memory. The initial loader logged
transient allocation failures but continued; the successful retry's host-memory
peak was 5,427,388,416 bytes under a 25,177,777,152-byte cap, with swap disabled.
These host-memory figures are not GPU-memory measurements. The FP8 cache used
uncalibrated scales of 1.0; this is a functional smoke, not a quality comparison.

Passed:

- Direct OpenAI-compatible Chat returned `NEMOTRON_OK`.
- A tool request produced valid `lookup_code` arguments; the follow-up correctly
  returned the supplied `cedar-7149` result.
- Kimi via MoonBridge returned `NEMOTRON_KIMI_OK`.
- Kimi's file-reading tool round trip returned `maple-4827`.
- Codex via MoonBridge returned `NEMOTRON_CODEX_OK`.

Private commands, profile, model metadata, API results and logs are retained
under `~/storage/arkey-nemotron-20261014/`. This record establishes successful
loading and short chat/tool operation, not replay performance, sustained
stability or long-context quality.

References: [NVIDIA checkpoint](https://huggingface.co/nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B-NVFP4),
[FlashInfer precompiled-cache installation](https://docs.flashinfer.ai/installation.html).
