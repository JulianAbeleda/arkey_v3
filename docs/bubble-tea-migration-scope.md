# Arkey v3 Bubble Tea migration scope

Status: compatibility release implemented; final Bash removal remains

Scoped: 2026-07-29

Implemented: 2026-07-29

The Go application is now the default `arkey` executable. The Bash baseline is
installed only as the explicitly named `arkey-legacy` rollback command for one
compatibility release. The implementation uses direct `arkey-legacy` invocation
instead of the proposed `ARKEY_LEGACY_UI` environment switch, which makes the
rollback visible and prevents normal startup from silently entering Bash.

Target: Linux first, with an architecture that does not prevent later platform support

## 1. Decision

Migrate the Arkey v3 application from Bash to one Go binary built with the
current Charm v2 stack:

| Dependency | Scoped version | Purpose |
| --- | ---: | --- |
| `charm.land/bubbletea/v2` | `v2.0.8` | event loop, terminal lifecycle, input, rendering |
| `charm.land/bubbles/v2` | `v2.1.1` | lists, help, spinner, viewport where useful |
| `charm.land/lipgloss/v2` | `v2.0.5` | layout, responsive styling, light/dark themes |
| `github.com/pelletier/go-toml/v2` | `v2.4.3` | versioned application configuration |
| `golang.org/x/sys` | `v0.47.0` | Linux exec, locking, and process primitives |
| `github.com/creack/pty` | `v1.1.24` | test-only pseudo-terminal integration coverage |
| Go | `1.25.x` | minimum toolchain declared by all three modules |

These were the latest stable versions resolved from the Go module proxy when
this scope was written. Direct runtime and test dependencies must be recorded
in `go.mod` and `go.sum`; updates should be deliberate dependency PRs, not
unbounded `@latest` installs. No YAML library is required: Arkey treats the
MoonBridge YAML as an opaque credential-bearing file, starts MoonBridge, and
uses its typed HTTP catalog instead of parsing or grepping secrets locally.

Bubble Tea v2 is the correct major version for this application. Its renderer,
declarative alternate-screen handling, synchronized output support, terminal
resize events, and improved key handling directly address the current Bash
menu's redraw and escape-sequence problems.

This is an application migration, not a cosmetic wrapper around the Bash menu.
The UI must never shell out to the old menu, state library, GPU scanner, or
runtime manager after final cutover.

## 2. Product boundary

Arkey v3 remains a boot and routing manager. It does not become an AI provider,
an inference engine, or a fork of the Codex source tree.

Retained external boundaries:

- MoonBridge remains a separately built, revision-pinned Go dependency.
- Arkey Codex remains the separately installed modified Codex-derived TUI.
- `llama-server` remains an external local inference executable.
- Local GGUF weights remain machine-local and outside Git.
- Provider credentials remain only in the machine-local MoonBridge YAML file.

Explicitly out of scope:

- vendoring MoonBridge into the Arkey repository;
- importing any Arkey v2 runtime snapshot;
- implementing tinygrad serving in this migration;
- adding session management to the menu;
- changing the local reasoning harness inside MoonBridge;
- changing DeepSeek-specific frontier reasoning translation;
- distributing model weights or API credentials;
- presenting Arkey Codex as the official Codex client;
- guaranteeing Windows or macOS runtime management in the first release.

## 3. Current behavior that must survive

The Bash baseline is approximately 1,478 lines across the launcher, state
library, menu, GPU scan, local runtime control, MoonBridge/Codex handoff,
installer, and tests.

| Current file | Responsibility to migrate |
| --- | --- |
| `arkey` | CLI flags, TTY boot decision, route injection, Codex handoff |
| `arkey-boot-lib` | validated state reads, atomic state writes, model mapping |
| `arkey-boot-menu` | navigation, status rendering, models, launch workflow |
| `arkey-hardware-scan` | NVIDIA/AMD detection, llama backend alignment, Codex metadata |
| `arkey-local-runtime` | MoonBridge lifecycle, llama lifecycle, health and acceleration checks |
| `codex-moonbridge` | MoonBridge readiness, Codex environment, sandbox and exec arguments |
| `install.sh` | install locations, safe config creation, component checks |
| dependency installer | exact MoonBridge checkout verification and build |

Required menu hierarchy:

```text
TUI
  Arkey Codex (modded; not official Codex)
Config
  Local
    tinygrad     (visible, disabled, in development)
    llama.cpp
      discovered GGUF models
  Frontier
    DeepSeek
    Codex
    Claude
  GPU Auto-scan
Exit
```

Required navigation:

- Up/Down and `j`/`k` move the current selection.
- Right/Enter selects or opens an item.
- Left/Escape/Backspace returns exactly one screen.
- `b` returns exactly one screen for compatibility.
- `q` and Ctrl+C quit unless a confirmation or critical operation owns the key.
- Number shortcuts remain available where unambiguous.
- Selection wraps at the top and bottom, matching the existing menu.
- A navigation stack, not nested blocking loops, owns all back behavior.

Required route behavior:

- TUI selection and AI route selection remain independent.
- TUI currently contains only `Arkey Codex (modded)`.
- Frontier selection persists DeepSeek, Codex, or Claude and switches mode to
  `frontier`.
- Successful local-model activation persists runtime, model, and `local` mode.
- Failed local activation does not commit a new selected route.
- The stable local model exposed to Codex remains `arkey-local-llama`.
- tinygrad remains disabled and cannot change the active route.

Required CLI compatibility:

```text
arkey
arkey --boot
arkey --no-boot
arkey --preserve-session-model [CODEX_ARGS...]
arkey exec "prompt"
```

- No arguments plus interactive stdin/stdout opens the TUI.
- Noninteractive invocation bypasses Bubble Tea.
- Arkey-owned flags are parsed only before the first Codex argument.
- Payload arguments such as `exec -- --boot --no-boot` pass through unchanged.
- `--boot` and `--no-boot` together return usage error status 2.
- `--boot` with Codex payload arguments returns usage error status 2.
- Arkey injects `model_provider="moonbridge"` only when the user did not.
- Arkey injects the selected model only when the user did not and
  `--preserve-session-model` is absent.
- `exec` retains the current `--skip-git-repo-check` compatibility behavior.
- Codex retains the `workspace-write` sandbox argument.
- `CODEX_HOME` defaults to `~/.codex-moonbridge` and remains outside Git.

## 4. Target repository layout

```text
cmd/arkey/main.go
internal/app/
  model.go
  messages.go
  update.go
  view.go
  keymap.go
internal/cli/
  parse.go
  launch.go
internal/config/
  config.go
  migrate.go
  store.go
internal/ui/
  theme.go
  layout.go
  menu.go
  status.go
internal/models/
  discover.go
  metadata.go
internal/gpu/
  detect.go
  align.go
internal/runtime/
  controller.go
  llama.go
  process_linux.go
internal/moonbridge/
  client.go
  process.go
  catalog.go
internal/codex/
  command.go
internal/platform/
  paths.go
  commands.go
internal/testkit/
  fakes.go
dependencies/moonbridge.env
scripts/install.sh
scripts/install-moonbridge-dependency.sh
go.mod
go.sum
```

The exact file split can change during implementation, but the boundaries must
remain. In particular, Bubble Tea models must not contain direct filesystem,
HTTP, `/proc`, `systemctl`, or process-launch logic.

## 5. Core architecture

### 5.1 One binary, two execution paths

The `arkey` binary owns both interactive and noninteractive use:

```text
parse args
  ├─ interactive boot requested -> run Bubble Tea -> receive exit or launch plan
  └─ direct invocation          -> build launch plan
                                     ↓
                              restore/no TUI active
                                     ↓
                              exec Arkey Codex
```

The current shell `exec` behavior should be preserved on Linux with an exact
argument-vector exec, not `sh -c`. For an interactive launch, the Bubble Tea
model records a `LaunchPlan`, returns `tea.Quit`, and the outer `main` performs
the exec only after `Program.Run` has returned and Bubble Tea has restored the
terminal.

Do not use `tea.ExecProcess` for the final Codex handoff. That API intentionally
suspends and later resumes the Bubble Tea program; Arkey's desired behavior is
to replace itself with Codex and never redraw underneath it.

### 5.2 Elm-style state and effects

The Bubble Tea model contains display state only:

- current screen and navigation stack;
- cursor/selection per screen;
- terminal width and height;
- loaded configuration snapshot;
- cached MoonBridge, route, GPU, and runtime status;
- discovered model summaries;
- active operation, spinner state, notice, and error state;
- optional pending launch plan;
- detected light/dark background and accessibility preferences.

`View` must be pure: no HTTP, filesystem reads, command execution, sleeps, or
configuration writes. `Update` performs deterministic transitions and returns
commands. All I/O is behind interfaces and returns typed messages.

Representative messages:

```text
configLoadedMsg
statusRefreshedMsg
modelsDiscoveredMsg
gpuScanStartedMsg / gpuScanFinishedMsg
localLoadStageMsg / localLoadFinishedMsg
moonbridgeReadyMsg
operationFailedMsg
launchReadyMsg
tea.WindowSizeMsg
tea.BackgroundColorMsg
tea.KeyPressMsg
spinner.TickMsg
```

Long operations run as `tea.Cmd` functions with contexts and deadlines. They
must not block `Update` or print directly to the terminal. A spinner and named
stage communicate indeterminate work; do not display a fake percentage for
model loading.

### 5.3 Dependency injection

The production adapters implement small interfaces for:

- filesystem and atomic writes;
- command execution;
- HTTP requests;
- clock/timers;
- process and service management;
- GPU discovery;
- model discovery;
- MoonBridge catalog/status;
- Codex launch planning.

Tests inject fakes. Environment overrides currently used by Bash tests remain
supported until equivalent explicit test options exist. Production behavior
must not depend on test-only environment variables.

## 6. UI and visual design

### 6.1 Charm components

- Bubble Tea v2 owns the alternate screen, cursor, signals, keyboard input,
  resize events, synchronized rendering, and color downsampling.
- Lip Gloss v2 owns borders, padding, width calculation, badges, panels, and
  responsive composition.
- Bubbles `list` is used for GGUF model selection and filtering.
- Bubbles `spinner` is used for GPU scan, MoonBridge startup, and model load.
- Bubbles `help` renders contextual key hints.
- Bubbles `viewport` is used only for scrollable error or log details.
- Bubbles `progress` is not used unless a real measurable percentage exists.

### 6.2 Screen composition

Wide layout:

```text
╭─ ARKEY ─────────────────────────────────────────────────────╮
│ workspace  ~/project            MoonBridge  ● online       │
│ route      Local · llama.cpp     GPU         ● NVIDIA       │
├─────────────────────────────────────────────────────────────┤
│  > TUI       Arkey Codex (modded)              ready        │
│    Config    Local · Qwen3-14B-Q4_K_M           loaded       │
│    Exit                                          quit         │
╰─────────────────────────────────────────────────────────────╯
  ↑/↓ move   →/enter open   ←/esc back   ? help   q quit
```

The final styling can evolve, but the information hierarchy cannot obscure the
fact that Arkey Codex is modified and not official Codex.

Responsive rules:

- `tea.WindowSizeMsg` is the only geometry source after startup.
- Rendered width must never exceed the terminal width.
- Use display-cell width from Lip Gloss, not byte length or rune count.
- At medium widths, collapse status cards into single-line badges.
- At narrow widths, hide nonessential detail before truncating labels.
- Below a usable minimum, show a stable resize message and key hints rather
  than forcing a fictitious 40x12 canvas.
- Model lists paginate or scroll; they never wrap a row into the next row.
- Long paths are middle-truncated so the filename remains visible.

Theme and accessibility:

- Request background color through Bubble Tea and select a light or dark
  Lip Gloss palette from `tea.BackgroundColorMsg`.
- Respect `NO_COLOR` and terminal color downsampling.
- Never encode status by color alone; every badge includes text or a symbol.
- Provide a reduced-motion option that replaces animated spinners with static
  stage text.
- Keep focus visibly distinct in ANSI-16, ANSI-256, true-color, and no-color
  terminals.
- Mouse support is optional and must never be required for navigation.

### 6.3 Navigation state

Use a screen enum and a stack:

```text
main -> tui
main -> config -> local runtime -> models -> loading/result
main -> config -> frontier
main -> config -> GPU scan/result
```

Left, Escape, Backspace, and `b` pop one stack entry. The main screen ignores
back. This single rule replaces the nested Bash loops that previously produced
inconsistent left-arrow behavior.

### 6.4 Renderer and latency invariants

Bubble Tea v2's renderer owns the terminal for the entire interactive session.
Arkey must not fight it:

- return one complete, pure `tea.View` with `AltScreen = true` on every render;
- never emit direct ANSI, `fmt.Print`, subprocess stdout/stderr, or log output
  while the renderer is active;
- never manually clear, home, or force-redraw the terminal;
- never recreate Bubbles components inside `View`;
- resize Bubbles components only from `tea.WindowSizeMsg` handling;
- do not perform discovery, HTTP, config writes, or process checks because of a
  resize or render;
- debounce resize-triggered recomputation for roughly 75–150 ms and attach a
  generation number so stale results cannot overwrite newer dimensions;
- refresh cached status at most every 2–5 seconds while foreground and idle,
  and suspend that polling during owned load/scan operations;
- start spinner ticks only while an operation is active;
- use `tea.Batch` only for independent effects and `tea.Sequence` when order is
  required;
- keep Bubble Tea's default renderer FPS initially; consider 30 FPS only after
  terminal-byte and latency profiling demonstrates a benefit;
- stop or cancel screen-specific commands when their screen generation is no
  longer current.

Synchronized terminal output is an automatic renderer capability, not a reason
to rely on terminal-specific behavior. Testing must also pass on terminals that
fall back to ordinary cursor-managed updates.

## 7. Configuration and state

### 7.1 Consolidated application config

Replace the collection of one-line state files with one versioned file:

```text
~/.config/arkey/config.toml
```

Recommended schema:

```toml
version = 1
mode = "frontier"

[frontier]
backend = "deepseek"

[local]
runtime = "llama"
model = "/absolute/path/to/model.gguf"
llama_server = "/absolute/path/to/llama-server"
port = 8080
context_size = 32768

[hardware]
vendor = "nvidia"
name = "GPU display name"

[moonbridge]
address = "127.0.0.1:38440"
config = "/absolute/path/to/moonbridge.yml"

[ui]
reduced_motion = false
```

Provider secrets do not belong in this file. They remain in
`~/.config/arkey/moonbridge.yml`, mode `0600`.

Config requirements:

- reject unsupported schema versions;
- validate enum values and port ranges;
- accept only regular `.gguf` files as selected models;
- preserve unknown future fields where practical, or fail without overwriting;
- write through a same-directory temporary file, mode `0600`, then rename;
- sync data before rename where supported;
- refuse to replace a symlinked config target;
- never log provider credentials or the contents of MoonBridge YAML;
- preserve last known-good in-memory config if a write fails.

### 7.2 Legacy migration

On first Go launch, if `config.toml` is absent:

1. Read and validate the legacy files `mode`, `backend`, `local-runtime`,
   `local-model`, `gpu-vendor`, `gpu-name`, and `llama-server`.
2. Apply current defaults for missing or invalid values.
3. Write `config.toml` atomically with mode `0600`.
4. Leave legacy files untouched for one rollback release.
5. Record migration in the config schema, not in an extra marker file.

During the compatibility release, the Go application reads TOML first and can
fall back to legacy state only when TOML does not exist. It must never dual-write
both formats. The final cleanup release removes the fallback reader.

### 7.3 Runtime state and logs

Use XDG paths:

```text
~/.local/state/arkey/local/runtime.json
~/.local/state/arkey/logs/moonbridge.log
~/.local/state/arkey/logs/llama.log
```

Runtime state records PID, executable path, argument fingerprint, model path,
port, and Linux process start time. The start time is required to defend
against PID reuse. Runtime state is not configuration and may be rebuilt.

## 8. Backend services

### 8.1 MoonBridge client and lifecycle

The Go client uses `net/http` with explicit short timeouts to query
`/v1/models`. Catalog parsing uses typed JSON; substring matching is removed.

Status vocabulary must distinguish:

- `online`: endpoint responds;
- `standby`: installed/configured but not currently responding;
- `unavailable`: binary or configuration missing;
- `route configured`: requested model appears in the catalog;
- `route missing`: requested model is absent;
- `credentials unverified`: route presence does not prove a provider key works.

MoonBridge lifecycle behavior:

- use the exact installed binary and machine-local YAML path;
- prefer user systemd transient units when a usable user manager exists;
- support a direct child-process fallback;
- refuse to stop an unrecognized process or overwrite an occupied address;
- reload only when the required local route is absent;
- wait for a bounded health check and surface the log path on failure;
- never expose YAML contents in the UI or logs.

The pinned MoonBridge revision remains in `dependencies/moonbridge.env`. The Go
application must report its expected revision at `arkey doctor`, but it does
not modify the dependency checkout during ordinary startup.

### 8.2 Model discovery and metadata

- Recursively discover regular `.gguf` files under the configured model roots.
- Default root remains `~/models`.
- Sort deterministically by display name then absolute path.
- Capture filename, parent path, size, and selected/running state.
- Discovery runs asynchronously and can be cancelled on screen exit.
- Ignore unreadable entries without crashing; report a nonfatal count.
- Do not follow directory symlink cycles.
- A refresh key rescans without restarting the application.
- Bubbles list filtering supports large model directories.

Codex local metadata is updated with Go's JSON encoder rather than `jq`.
Preserve unrelated catalog entries and file permissions, replace only the
`arkey-local-llama` entry, and write atomically. Preserve the current 32,768
context metadata and reasoning-summary capability until the modded Codex
catalog schema changes.

### 8.3 GPU detection and alignment

Initial platform behavior remains Linux:

- NVIDIA: device node plus successful `nvidia-smi` query;
- AMD: `/dev/kfd` plus successful ROCm query;
- unknown: no supported compute GPU detected.

Alignment requirements:

- enumerate configured candidate roots without invoking a shell;
- inspect dynamic dependencies of candidate `llama-server` executables;
- classify CUDA, ROCm/HIP, or CPU builds;
- select only a binary matching the detected vendor;
- never treat NVIDIA hardware as ROCm or AMD hardware as CUDA;
- save hardware and binary selection only after metadata update succeeds;
- clearly report detection, candidate rejection, and remediation;
- retain deterministic overrides in tests, not hidden production fallbacks.

`ldd` is Linux-specific. Place it behind a backend-inspection interface so a
future implementation can use platform-native binary inspection.

### 8.4 llama.cpp lifecycle

Required start sequence:

1. Validate model and aligned server paths.
2. Acquire an exclusive runtime-operation lock.
3. Ensure the MoonBridge local route exists and is healthy.
4. Detect an already healthy matching managed process and reuse it.
5. Refuse an unmanaged process on the configured port.
6. Stop the old managed llama process gracefully.
7. Start the selected model with explicit arguments, never through a shell.
8. Poll `/v1/models` until healthy or the operation deadline expires.
9. Verify the log proves the expected CUDA or ROCm/HIP backend initialized.
10. Commit runtime state and application selection only after all checks pass.

Default llama arguments remain:

```text
--alias arkey-local
--host 127.0.0.1
--port 8080
--ctx-size 32768
--gpu-layers all
```

The load timeout remains long enough for large models but must be cancellable.
Cancellation sends graceful termination to a process Arkey started. If a new
model fails after the old model was stopped, Arkey should attempt to restore
the previous model once; the UI must report both the primary failure and
rollback result.

An operation lock prevents two Arkey processes from racing to start or stop the
same service. PID validation includes executable identity and process start
time before any signal is sent.

### 8.5 tinygrad

tinygrad stays in the domain model as an unavailable runtime with an explicit
development reason. It has no server adapter and cannot become selected. A
future adapter can implement the same runtime interface without changing the
navigation model or config schema.

## 9. Errors, observability, and recovery

- User errors appear as styled notices with a short remediation step.
- Detailed command output goes to the XDG state log, not directly into the
  alternate screen.
- A details viewport can display sanitized recent log lines.
- Errors are typed by domain: config, dependency, route, GPU, process, health,
  acceleration, and launch.
- Every external command has a context, timeout where appropriate, captured
  stderr, and an explicit argument vector.
- Debug mode may record command names and arguments but must redact values
  associated with secrets and must never dump MoonBridge YAML.
- Bubble Tea panic and signal recovery remains enabled.
- Ctrl+C during a noncritical screen quits cleanly.
- Ctrl+C during an owned load cancels that operation, cleans up its process,
  and returns a result screen before a second quit.
- Terminal state must be restored on normal exit, error, panic, SIGINT, and
  SIGTERM.

## 10. Security requirements

- No `sh -c`, interpolated shell command, or unvalidated executable lookup.
- All configuration and state writes are atomic and scoped to exact paths.
- Config and credential templates are installed with mode `0600`.
- API keys never enter Git, application config, telemetry, tests, or snapshots.
- Model paths and executable paths are canonicalized before persistence.
- Never signal a PID based only on a stale PID file.
- Never stop a process that fails executable, arguments, and start-time checks.
- Refuse local servers bound beyond loopback unless a future explicit setting
  and warning are added.
- HTTP clients set connect/request deadlines and response-size limits.
- JSON/YAML parsing has bounded input and does not use text grep fallbacks.
- Dependency versions and checksums are locked in `go.sum`.
- MoonBridge remains locked to a full 40-character Git commit.
- Release archives include required third-party license notices.
- Public-repository scans continue to reject keys, model files, local configs,
  sessions, logs, and machine-specific paths.

## 11. Installation and packaging

### 11.1 Source install

Keep a small Bash installer because installation precedes the Go binary. It
must:

1. Validate Git and a Go 1.25-compatible toolchain.
2. Build Arkey with `go build -trimpath` from locked modules.
3. Install the binary atomically at `~/.local/bin/arkey`.
4. Build/install the exact pinned MoonBridge dependency as today.
5. Create the credential-free MoonBridge YAML only when absent, mode `0600`.
6. Never replace a user's existing config or credentials.
7. Run `arkey doctor --install-check` before reporting success.

Because the machine may have an older Go launcher with `GOTOOLCHAIN=auto`, the
installer must report the selected module toolchain, not assume `go version`
alone proves the compiler used. Offline installs need either Go 1.25 already
installed or a prebuilt Arkey release.

### 11.2 Binary releases

After source cutover is stable, publish checksummed Linux binaries for at least:

- `linux/amd64`;
- `linux/arm64` where the external runtime stack is supported.

The binary must not embed MoonBridge, Codex, credentials, models, or machine
configuration. A release installer verifies checksum and architecture before
replacement. Reproducible build metadata should expose version, commit, build
date, and expected MoonBridge revision through `arkey version`.

Expected runtime dependencies after migration:

- Arkey Codex binary;
- pinned MoonBridge binary;
- `llama-server` for local mode;
- NVIDIA or ROCm user-space tools for the corresponding GPU scan;
- user systemd when available, with direct-process fallback.

`curl`, `jq`, `find`, `numfmt`, `tput`, and the Bash runtime helpers cease to be
application runtime dependencies because their work moves into Go.

## 12. Test strategy

### 12.1 Unit tests

- every CLI parse and pass-through edge case;
- route/model argument injection and override detection;
- screen-stack transitions for every supported key;
- left-arrow regression from every child screen;
- cursor wrapping and disabled-item behavior;
- config defaults, validation, atomic writes, and legacy migration;
- TOML schema-version failures without overwrite;
- model discovery, ordering, symlink behavior, and filtering;
- MoonBridge catalog parsing and route classification;
- NVIDIA, AMD, CPU, and unknown backend parsing;
- PID identity/start-time validation;
- llama command construction and rollback decisions;
- Codex metadata replacement without damage to unrelated entries;
- cancellation and timeout behavior with fake clocks and runners.

### 12.2 UI model and golden tests

Drive `Update` directly with typed Bubble Tea messages and render `View` at
fixed sizes. Golden coverage includes:

- dark, light, ANSI-16/no-color themes;
- 120x36, 80x24, narrow, and below-minimum terminals;
- main, TUI, config, frontier, local runtime, models, loading, success, error,
  and help screens;
- long model names and deep paths;
- empty and very large model lists;
- selected, loaded, stopped, missing, unconfigured, and unavailable states.

Golden output is normalized for nondeterministic elapsed time. Tests assert
every rendered line fits the configured display-cell width.

### 12.3 Adapter tests

Use temporary XDG directories, `httptest.Server`, and fake executables to test:

- MoonBridge health/catalog timeouts and malformed responses;
- state permission and rename behavior;
- process-lock contention;
- managed/unmanaged port ownership;
- service-manager and direct-process branches;
- acceleration log verification;
- failed model startup and previous-model restoration.

No CI test requires a real GPU, provider key, GGUF, Codex session, or paid API.

### 12.4 Integration and terminal tests

- Run the compiled program through a pseudo-terminal.
- Send real Up/Down/Left/Right/Escape/Enter sequences.
- Resize repeatedly while navigating.
- Generate resize storms while model discovery, status refresh, and spinners
  are active; stale results must be discarded and no duplicate operation may
  start.
- Cycle all screens hundreds of times and assert no stale or wrapped lines.
- Verify panic, SIGINT, SIGTERM, and ordinary exit restore terminal modes.
- Verify launching a fake Codex process leaves no Bubble Tea frame underneath.
- Verify noninteractive stdout/stderr contain no terminal control sequences.
- Test WezTerm directly, but do not add any WezTerm dependency.
- Include tmux and a basic xterm-compatible terminal in the manual matrix.
- Profile idle, scrolling, and loading states for CPU, allocations, and bytes
  written to the pseudo-terminal; regressions require an explicit budget
  decision rather than an arbitrary FPS increase.

### 12.5 Hardware smoke test

On the current NVIDIA workstation:

1. Auto-scan identifies the NVIDIA GPU and CUDA llama-server.
2. Select the existing GGUF from the Bubble Tea model list.
3. Confirm llama.cpp becomes healthy on loopback port 8080.
4. Confirm logs show CUDA initialization and no CPU fallback.
5. Confirm MoonBridge exposes `arkey-local-llama` on port 38440.
6. Launch Arkey Codex and request a fixed smoke marker.
7. Confirm the response returns through MoonBridge from the local model.
8. Confirm no model-metadata warning and no reasoning-stream item errors.

### 12.6 CI gates

Required on every pull request:

```text
go test ./...
go test -race ./...
go vet ./...
go mod verify
go build ./cmd/arkey
legacy compatibility tests during the transition
pinned MoonBridge revision validation
repository-boundary and credential scans
```

Pin CI actions and cache only module/build data. Do not upload local configs,
test logs containing home paths, or runtime state as public artifacts.

## 13. Migration phases

### Phase 0 — lock the contract

- Convert the current Bash tests into a written parity matrix.
- Add fixtures for MoonBridge catalogs, GPU output, runtime logs, and legacy
  state files.
- Record current CLI exit statuses and exact argument construction.
- Keep the Bash implementation as the executable reference.

Exit: all behavior listed in sections 3 and 12 has an automated or explicit
manual test owner.

### Phase 1 — Go foundation, no UI cutover

- Add `go.mod`, locked Charm dependencies, version metadata, and CI.
- Implement platform paths, command runner interfaces, typed errors, and
  application config.
- Implement read-only legacy state import and config migration tests.
- Add `arkey version` and `arkey doctor` without replacing the Bash launcher.

Exit: Go foundation tests pass and no production routing changes.

### Phase 2 — backend parity

- Port model discovery and metadata registration.
- Port GPU detection and llama binary alignment.
- Port MoonBridge client/lifecycle.
- Port llama process lifecycle, health, acceleration checks, locks, and
  rollback.
- Exercise adapters with fake processes and HTTP servers.

Exit: Go subcommands can reproduce all backend operations without the Bash
helpers, while the Bash menu can remain the default frontend.

### Phase 3 — Bubble Tea UI

- Implement the screen stack, key map, theme, responsive layouts, lists,
  spinner, help, status refresh, notices, and error viewport.
- Connect UI effects only to tested backend interfaces.
- Add golden, resize, navigation, and cancellation tests.
- Keep the TUI behind `ARKEY_UI=bubbletea` or an explicit preview flag.

Exit: feature parity passes in preview mode and terminal restoration tests are
clean.

### Phase 4 — CLI and Codex handoff

- Port exact CLI parsing and route injection.
- Implement post-Bubble-Tea `exec` launch plans.
- Preserve noninteractive behavior and environment isolation.
- Validate local and frontier launch gates.

Exit: Bash and Go launchers produce identical argument vectors for the parity
matrix; local end-to-end smoke succeeds.

### Phase 5 — compatibility release

- Install the Go binary as `arkey` by default.
- Keep renamed Bash helpers for one rollback release only.
- Run a first-launch migration from legacy state to TOML.
- Provide `ARKEY_LEGACY_UI=1` as a temporary rollback escape hatch.
- Document known Linux/systemd/direct-process behavior.

Exit: normal use does not invoke Bash application helpers; telemetry-free issue
reports show no terminal corruption or state loss.

### Phase 6 — final cutover

- Remove the Bash menu, state library, GPU scanner, runtime manager, and Codex
  wrapper.
- Remove legacy state fallback and rollback flag after the compatibility window.
- Retain only installation/dependency shell scripts.
- Publish checksummed binaries and third-party notices.
- Update the lean repository boundary for the final Go source tree.

Exit: one Go application binary owns all runtime behavior; repository and
installed-file audits find no obsolete helper copies.

## 14. Recommended pull-request sequence

1. `docs: define Bubble Tea migration contract`
2. `build: add Go 1.25 module and CI gates`
3. `feat: add versioned config and legacy import`
4. `feat: port MoonBridge, model, and GPU services`
5. `feat: port llama runtime lifecycle`
6. `feat: add Bubble Tea preview UI`
7. `feat: port CLI and Codex launch handoff`
8. `release: make Go Arkey the default`
9. `cleanup: remove legacy Bash runtime helpers`

Each PR must be independently testable and keep the public repository free of
credentials, models, local state, and unrelated v2 material.

## 15. Acceptance criteria

The migration is complete only when all of the following are true.

Functional:

- Menu hierarchy and TUI/backend separation exactly match the product contract.
- All back keys work on every nested screen.
- DeepSeek, Codex, Claude, and local llama routes persist correctly.
- tinygrad is visible but cannot be selected.
- GPU scan cannot confuse NVIDIA and ROCm backends.
- Local selection commits only after MoonBridge, llama health, and GPU checks.
- Modded Codex launches through MoonBridge with the selected route.
- Existing noninteractive CLI calls remain compatible.

UI:

- No line corruption during navigation or resize stress.
- No frame exceeds terminal display width.
- Alternate screen and cursor are restored on every exit path.
- Light, dark, limited-color, and no-color terminals remain readable.
- Loading work remains responsive and cancellable.
- The UI always labels Arkey Codex as modified/not official.

Reliability and security:

- No blocking I/O occurs in `View` or `Update`.
- No external command uses a shell-interpolated string.
- No unrecognized PID is stopped.
- No provider key is read into application logs or committed files.
- Config/state files use correct permissions and atomic replacement.
- Race detector, unit, golden, integration, and hardware smoke tests pass.

Packaging:

- `arkey` is one Go binary at runtime.
- Bash remains only for source installation/dependency bootstrap.
- MoonBridge stays external and pinned to a verified full commit.
- A fresh install and an upgrade from the Bash version both pass doctor checks.
- Public release contents contain no v2 snapshot, model, session, key, or
  machine-specific path.

## 16. Risks and controls

| Risk | Control |
| --- | --- |
| Bubble Tea v2 dependency/API churn | lock exact stable versions; update deliberately |
| Go 1.25 unavailable offline | validate toolchain; provide prebuilt releases |
| UI rewrite accidentally changes routing | parity matrix and fake command-runner assertions |
| Long model loads freeze UI | async commands, spinner messages, context cancellation |
| Concurrent Arkey instances race services | exclusive operation lock and PID identity checks |
| Failed model switch leaves no server | previous-model rollback attempt and honest result state |
| Renderer corrupts terminal before Codex | quit Bubble Tea fully, then exec outside the program |
| Wide Unicode/model paths break layout | display-cell width tests and responsive truncation |
| Config migration damages legacy state | read-only import, atomic TOML write, one-release fallback |
| systemd unavailable | tested direct-process adapter |
| Route presence mistaken for valid credentials | separate configured and credentials-unverified states |
| Repository stops being lean | exact CI allowlist plus secret/model/state scans |

## 17. Source references

- Bubble Tea repository and documentation:
  <https://github.com/charmbracelet/bubbletea>
- Bubble Tea v2 release notes:
  <https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.0>
- Bubble Tea v2 upgrade guide:
  <https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md>
- Bubbles repository:
  <https://github.com/charmbracelet/bubbles>
- Lip Gloss repository:
  <https://github.com/charmbracelet/lipgloss>

The implementation should re-resolve and review stable versions immediately
before Phase 1, because dependency versions and minimum Go requirements can
change after this dated scope.
