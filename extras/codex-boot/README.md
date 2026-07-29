# Arkey Codex Boot Mode

Arkey v3 now uses an Arkey-owned Go/Bubble Tea boot menu that launches **Arkey
Codex**, our modified Codex-derived TUI, through MoonBridge. It is not the
official Codex client.
WezTerm is not a dependency and does not own or install this mode; its terminal
boot menu was used only as an interaction reference.

The menu is structured as:

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

The TUI and AI route are separate. Frontier and local choices persist in
`~/.config/arkey/config.toml`. Local selection is committed only after the
llama.cpp server starts and passes its health check. MoonBridge
then exposes the stable `arkey-local-llama` route to the modded TUI. Models are
discovered recursively under `~/models`.

tinygrad remains visible so the intended runtime layout is clear, but it cannot
be selected while its serving stack is in development. llama.cpp is the only
working local runtime for now and listens on `127.0.0.1:8080`.

The Bubble Tea model list marks the healthy in-memory model as `● loaded`, a
remembered but stopped choice as `selected`, and a recognized process that has
not become healthy as `◐ starting`. Move to the active row and press `d` to
unload it. Unloading releases the runtime without forgetting the saved model.

GPU Auto-scan detects NVIDIA or AMD compute devices, verifies the linked backend
of installed llama.cpp servers, and saves only a matching executable. It also
registers `arkey-local-llama` in the modded Codex model catalog. Local loading
refuses an unscanned or mismatched binary instead of silently falling back to
CPU.

MoonBridge is not vendored into Arkey. The installer checks out the exact public
fork revision in `dependencies/moonbridge.env`, builds it once, and installs the
runtime binary at `~/.local/libexec/arkey/moonbridge`. The generated local
configuration is `~/.config/arkey/moonbridge.yml` with mode `0600`; its API keys
and all Codex sessions remain outside Git.

Use Up/Down to move, Right or Enter to select, Left to return to the previous
menu, and `d` on the active model row to unload it.

```bash
arkey                    # Arkey boot menu
arkey --no-boot          # Arkey Codex directly through MoonBridge
arkey exec "fix tests"   # noninteractive Arkey Codex through MoonBridge
```

Install, validate, and test from the Arkey repository. The Bash files in this
directory remain temporarily as an explicit `arkey-legacy` rollback path; they
are not the default application:

```bash
scripts/install.sh
scripts/install.sh --check
extras/codex-boot/test.sh
```

Set `ARKEY_SKIP_MOONBRIDGE_INSTALL=1` only when packaging or validating Arkey
without downloading/building its dependency.
