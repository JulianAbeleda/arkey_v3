# AGENTS.md

## What this project is

Arkey v3 is a single-binary Go terminal boot manager that routes four AI coding clients (Codex, Claude Code, Kimi, Crush) to a chosen upstream: local LLM inference (llama.cpp or vLLM), a remote llama.cpp server, or a frontier API (DeepSeek/Codex/Claude). It runs a Bubble Tea TUI, snapshots the user's installed client binaries into isolated homes, manages a pinned+patched MoonBridge fork (OpenAI-compatible protocol translation) that carries the client's traffic, owns the local inference engine lifecycle, and optionally serves an OpenAI-compatible gateway (`arkey gateway`, port 8090) and DeepSeek Harness's browser UI.

The repo intentionally contains no models, credentials, machine state, or client source. CI enforces this (see "Repo boundaries").

## Essential commands

| Purpose | Command |
|---|---|
| Build | `go build -o /tmp/arkey ./cmd/arkey` |
| Test | `go test ./...` |
| Race test | `go test -race ./...` |
| Vet | `go vet ./...` |
| Module verify | `go mod verify` |
| Full validation (what CI runs) | `scripts/install.sh --check` |
| Install | `scripts/install.sh` |

The README pairs `scripts/install.sh --check` + `go test ./...` as the standard validation. There is no Makefile.

`install.sh` supports env overrides: `ARKEY_USER_HOME`, `ARKEY_INSTALL_DIR`, `ARKEY_CONFIG_DIR`, `MOONBRIDGE_CONFIG`, plus skip flags `ARKEY_SKIP_MoonBRIDGE_INSTALL`, `ARKEY_SKIP_BRAVE_SEARCH_INSTALL`, `ARKEY_SKIP_SANDBOX_INSTALL`, `ARKEY_SKIP_CLIENT_SNAPSHOTS`.

`main.go` has `version`/`commit` vars set via `-ldflags "-X main.version=... -X main.commit=..."` — install.sh uses this.

## Repo boundaries (CI-enforced)

CI (`.github/workflows/ci.yml`) does three non-obvious checks beyond build/test:

1. **File boundary**: `git ls-files` must match only `.github|cmd|dependencies|docs|extras|internal|scripts` plus the top-level files `go.mod go.sum .gitignore LICENSE NOTICE README.md SECURITY.md`. Adding a new top-level directory requires updating this regex in the workflow.
2. **No private material**: rejects any `*.gguf`, `*.safetensors`, `*.pem`, `*.key`, `config.toml`, `runtime.json`, `models_catalog.json`, plus a credential regex scan (private keys, `sk-…`, `api_key/token:`), excluding `go.sum`.
3. **Shell syntax**: `bash -n` on install.sh, install-moonbridge-dependency.sh, install-brave-search-dependency.sh, snapshot-clients.sh.
4. **Pinned dependency check**: sources `dependencies/moonbridge.env`, asserts the 40-hex `MOONBRIDGE_REVISION`, shallow-fetches it from the moon-bridge repo, and verifies `FETCH_HEAD` matches.

## Architecture

```
cmd/arkey/main.go          Entry point: parse args, wire services, run TUI, unix.Exec the chosen client
internal/app/              UI state machine (Bubble Tea Model). Deliberately owns NO I/O.
internal/ui/               Pure rendering: Render(Screen) string via lipgloss. No I/O, no state.
internal/control/          The integration layer: implements app.Services. Wires config, platform, gpu, moonbridge, runtime.
internal/config/           Durable TOML config (Config struct, Validate, Store with load/save/migrate).
internal/platform/         OS-facing primitives: Paths (XDG-aware), RejectSymlinkComponents, EnsurePrivateDir, Runner.
internal/client/           Shared process boundary: client.Plan{Binary, Args, Env}, Validate, StateHome per client.
internal/codex/ claude/ kimi/ crush/   One package per client: Build(BuildOptions) → client.Plan, WriteConfig(...).
internal/cli/              Argument parsing: Parse(args) → Options, ErrUsage sentinel.
internal/moonbridge/       HTTP client to running bridge + serverroute.go (writes routes into MoonBridge YAML).
internal/runtime/          Local engine lifecycle: controller.go (shared), llama.go, vllm.go (adapters), process_*.go.
internal/gpu/              GPU detection (linux/darwin) + tensor-parallel head alignment.
internal/models/           Model discovery (GGUF, HF), metadata, context sizing, output budget.
internal/llamaserver/      Health/metadata probe for a running llama-server.
internal/gateway/          `arkey gateway` subcommand: OpenAI-compatible endpoint on 8090.
internal/dsh/              DeepSeek Harness: patches web profile, launches `dsh web`; `Headless` builds a `dsh headless --json` run.
internal/harness/          Shared harness: `arkey harness-mcp` stdio MCP server whose `deepseek_harness` tool runs dsh headless.
```

**Layering rule**: `app` defines the `Services` interface (all methods take `context.Context`, must honor cancellation). `control` is the sole real implementation; `app.NopServices` is a no-op implementation for tests/previews. `ui` never calls back into `app` or `control` — it receives a `Screen` value and renders it. The TUI passes per-operation timeouts (5s refresh, 6min launch, 60s unload) and uses a `generation` counter to discard stale async results.

**Control/data flow on launch**:
1. `main.go` → `control.New(home, workspace)` builds the `Services` implementation.
2. TUI runs; user picks client + route; `LaunchPlan` is stored on the model.
3. After `Program.Run()` returns, `main.go` calls `services.ValidateClient`, `services.ValidateRoute`, `services.PrepareLaunch` (6min timeout: starts MoonBridge if needed, writes routes into its config, ensures local engine if local mode).
4. `Build()` from the chosen client package produces a `client.Plan`.
5. Client-specific `WriteConfig` writes into the isolated state home.
6. `unix.Exec(binary, args, env)` replaces Arkey's process with the client.

## Configuration

- Single TOML file: `~/.config/arkey/config.toml` (0600, parent 0700). `config.Store` handles load/save; `MigrateLegacy` handles old-format files.
- `Config.Default(home)` provides all defaults; `Decode` merges over defaults (unknown fields are rejected via `DisallowUnknownFields`).
- Env overrides applied in `control.New`: `ARKEY_MODEL_DIR`, `MOONBRIDGE_CONFIG`, `MOONBRIDGE_ADDR`, `ARKEY_LLAMA_PORT`, `ARKEY_USER_HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`.
- `vllm_url` must be a local HTTP server root on localhost/127.0.0.1 with no path (validated in `Validate()`).
- `Local.ContextSize`: 0 means "derive from hardware at launch" (`models.DeriveContextSize`); positive is an operator pin; negative is always invalid.
- `Local.GPUs`: 0 = all detected GPUs; positive N = cap to N; negative invalid.

## Isolated client homes

Each client gets its own state home so the official install is never touched:

| Client | State home |
|---|---|
| codex | `~/.codex-moonbridge` |
| claude | `~/.claude-arkey` |
| kimi | `~/.kimi-arkey` |
| crush | `~/.crush-arkey` |

Client binaries are snapshotted (not symlinked) into `~/.local/libexec/arkey/clients/<name>/<name>` with a 0600 `snapshot.env` manifest (source path, sha256, version). `snapshot-clients.sh` resolves symlinks and, for npm-installed clients (crush's `run-crush.js`, codex's `codex.js`), resolves to the packaged native binary. Override source paths: `ARKEY_CODEX_SOURCE_BIN`, `ARKEY_CLAUDE_SOURCE_BIN`, `ARKEY_KIMI_SOURCE_BIN`, `ARKEY_CRUSH_SOURCE_BIN`.

## Pinned external dependencies

- **MoonBridge** (fork of JulianAbeleda/moon-bridge-arkey): pinned by full 40-char SHA in `dependencies/moonbridge.env`. `install-moonbridge-dependency.sh` clones at that SHA, exports a clean tree, applies `dependencies/moonbridge-local-output-budget.patch` + `dependencies/moonbridge-tool-stream.patch` (never patches the checkout directly), runs MoonBridge's own tests, builds to `~/.local/libexec/arkey/moonbridge`.
- **Brave Search MCP**: pinned by semver in `dependencies/brave-search.env`; `install-brave-search-dependency.sh` npm-installs, applies `dependencies/brave-search-rate-limit.patch` (idempotent, marked with "Patched for Arkey"), `node --check`s the result.
- **Sandbox (Linux only)**: `install-sandbox-dependency.sh` only runs if the kernel restricts unprivileged user namespaces (`kernel.apparmor_restrict_unprivileged_userns=1`); installs an AppArmor profile for bwrap via `sudo -n`.

## Cross-platform

Platform-specific behavior lives in build-tagged files: `tty_darwin.go`/`tty_linux.go`, `detect_darwin.go`/`detect_linux.go` (Metal vs ROCm/CUDA), `backends_darwin.go`/`backends_linux.go`, `process_darwin.go`/`process_linux.go`/`process_posix.go`, `bounded_darwin.go`/`bounded_linux.go` (memory-bounded vLLM launches; managed vLLM requires Linux systemd + `sudo -n`).

## Conventions

- **Error handling**: idiomatic `error` returns; `errors.Is(err, cli.ErrUsage)` for usage errors (exit 2 vs 1). User-facing errors are prefixed `"Arkey <area>:"` (e.g. `Arkey route:`, `Arkey client:`, `Arkey configuration:`) printed to stderr. No panic/recover.
- **Logging**: minimal — `fmt.Fprintln(os.Stderr, ...)` for errors, `fmt.Println` for status. No structured logging library. MoonBridge has its own log file under `~/.local/state/arkey/logs/`.
- **Path safety**: `platform.RejectSymlinkComponents` walks every existing path component and refuses symlinks (prevents private state escaping via a parent link). `EnsurePrivateDir` enforces 0700.
- **Context discipline**: every `Services` method takes `context.Context`. The UI uses `opCancel` so a new user action cancels an in-flight one; a `generation` counter discards stale results.
- **No I/O in `app` or `ui`**: all effects go through the `Services` interface. Tests use `NopServices` or mock implementations.
- **Process launching**: `unix.Exec` (not `exec.Command`) for the final client launch — Arkey's process is replaced, not forked.

## Gotchas

- `unix.Exec` means Arkey's process is gone after a successful launch; any cleanup logic after that line in `main.go` is dead code.
- The TUI only boots when both stdin and stdout are terminals (checked via `unix.IoctlGetTermios`). Pipe input and Arkey skips the TUI and goes straight to the launch path.
- `--no-boot`, any client args, or `ARKEY_BOOT=0` also skip the TUI.
- `vllm_url` in config must not include `/v1` — it's the server root, not the API endpoint (validated in `config.Validate()`).
- MoonBridge patches are applied to an exported clean tree, never the git checkout — re-running the installer is safe and idempotent.
- `harness-mcp` bypasses `cli.Parse` like `gateway`; clients start it with their cwd, which dsh runs in. Its dsh exec names the tsx loader and `TSX_TSCONFIG_PATH` from the checkout, since tsx resolves both from the cwd.
- The `gateway` subcommand (`arkey gateway`) bypasses `cli.Parse` and goes directly to `gateway.Main` — it doesn't need a terminal.
- `Local.Model` in config, if non-empty, must point to a regular `.gguf` file (validated on every load, not just at write time).
- CI's file-boundary regex includes `extras/` even though that directory doesn't exist yet — it's reserved.
