#!/usr/bin/env bash
set -euo pipefail

mode_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
launcher="$mode_root/arkey"
boot_menu="$mode_root/arkey-boot-menu"
boot_lib="$mode_root/arkey-boot-lib"
local_manager="$mode_root/arkey-local-runtime"
hardware_scan="$mode_root/arkey-hardware-scan"
codex_moonbridge="$mode_root/codex-moonbridge"
dependency_installer="$(cd "$mode_root/../.." && pwd)/scripts/install-moonbridge-dependency.sh"
test_root="$(mktemp -d)"
test_config="$test_root/config"
test_models="$test_root/models"
mkdir -p "$test_config" "$test_models"
install -m 0644 /dev/null "$test_models/A-test.gguf"
install -m 0644 /dev/null "$test_models/B-test.gguf"
install -m 0644 /dev/null "$test_models/ZZZ-this-model-name-is-deliberately-long-enough-to-wrap-a-terminal-row.gguf"
install -m 0755 /bin/true "$test_root/fake-cuda-llama-server"
printf 'models:\n  deepseek-v4-pro:\n  arkey-local-llama:\n' > "$test_root/moonbridge.yml"
printf '{"models":[{"slug":"deepseek-v4-pro"}]}' > "$test_root/models_catalog.json"
cleanup() {
  find "$test_root" -type f -delete 2>/dev/null || true
  find "$test_root" -depth -type d -empty -delete 2>/dev/null || true
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_eq() {
  local expected="$1" actual="$2" label="$3"
  [[ "$actual" == "$expected" ]] || fail "$label: expected '$expected', got '$actual'"
}

assert_contains() {
  local haystack="$1" needle="$2" label="$3"
  [[ "$haystack" == *"$needle"* ]] || fail "$label: missing '$needle'"
}

assert_ge() {
  local actual="$1" minimum="$2" label="$3"
  [[ "$actual" -ge "$minimum" ]] || fail "$label: expected at least $minimum, got $actual"
}

read_state() {
  local file="$1" value=""
  [[ -f "$test_config/$file" ]] && IFS= read -r value < "$test_config/$file" || true
  printf '%s' "$value"
}

bash -n "$launcher" "$boot_menu" "$boot_lib" "$local_manager" "$hardware_scan" "$codex_moonbridge" "$dependency_installer"

common_env=(
  ARKEY_BOOT_LIB="$boot_lib"
  ARKEY_CONFIG_DIR="$test_config"
  ARKEY_MODEL_DIR="$test_models"
  ARKEY_LOCAL_MANAGER=/bin/true
  ARKEY_HARDWARE_SCAN="$hardware_scan"
  MOONBRIDGE_ADDR=127.0.0.1:9
  MOONBRIDGE_CONFIG="$test_root/moonbridge.yml"
)

scan_env=(
  "${common_env[@]}"
  ARKEY_GPU_VENDOR_OVERRIDE=nvidia
  'ARKEY_GPU_NAME_OVERRIDE=Test NVIDIA GPU'
  ARKEY_LLAMA_CANDIDATES="$test_root/fake-cuda-llama-server"
  ARKEY_LLAMA_BACKEND_OVERRIDE=nvidia
  ARKEY_MODEL_CATALOG="$test_root/models_catalog.json"
)

actual="$(env "${scan_env[@]}" "$hardware_scan" scan)"
assert_contains "$actual" 'Detected: Test NVIDIA GPU (nvidia)' "GPU detection"
assert_eq 'nvidia' "$(read_state gpu-vendor)" "GPU vendor persistence"
assert_eq "$test_root/fake-cuda-llama-server" "$(read_state llama-server)" "aligned server persistence"
assert_eq '1' "$(jq '[.models[] | select(.slug == "arkey-local-llama")] | length' "$test_root/models_catalog.json")" "local metadata registration"
assert_eq '32768' "$(jq -r '.models[] | select(.slug == "arkey-local-llama") | .context_window' "$test_root/models_catalog.json")" "local metadata context window"
actual="$(env "${scan_env[@]}" "$hardware_scan" summary)"
assert_eq 'Test NVIDIA GPU · aligned' "$actual" "GPU alignment summary"

actual="$(env "${common_env[@]}" ARKEY_BOOT=0 CODEX_MOONBRIDGE_BIN=/bin/echo "$launcher" exec test)"
assert_eq '-c model_provider="moonbridge" -c model=deepseek-v4-pro exec test' "$actual" "default DeepSeek route"

printf '%s\n' local > "$test_config/mode"
printf '%s\n' tinygrad > "$test_config/local-runtime"
actual="$(env "${common_env[@]}" ARKEY_BOOT=0 CODEX_MOONBRIDGE_BIN=/bin/echo "$launcher" exec test)"
assert_eq '-c model_provider="moonbridge" -c model=arkey-local-llama exec test' "$actual" "local mode is llama-only"

printf '%s\n' frontier > "$test_config/mode"
printf '%s\n' codex > "$test_config/backend"
actual="$(env "${common_env[@]}" ARKEY_BOOT=0 CODEX_MOONBRIDGE_BIN=/bin/echo "$launcher" exec test)"
assert_eq '-c model_provider="moonbridge" -c model=gpt-5.6-sol exec test' "$actual" "persisted Codex route"

actual="$(env "${common_env[@]}" ARKEY_BOOT=0 CODEX_MOONBRIDGE_BIN=/bin/echo "$launcher" exec -- --boot --no-boot)"
assert_contains "$actual" 'exec -- --boot --no-boot' "payload flag preservation"

set +e
actual="$(env "${common_env[@]}" "$launcher" --boot --no-boot 2>&1)"
status=$?
set -e
assert_eq '2' "$status" "conflicting flag status"
assert_contains "$actual" 'cannot be used together' "conflicting flag message"

printf '%s\n' deepseek > "$test_config/backend"
actual="$(printf '11' | env "${common_env[@]}" ARKEY_BIN=/bin/echo "$boot_menu")"
assert_contains "$actual" 'Arkey Codex (modded)' "modded TUI label"
assert_contains "$actual" 'not official Codex' "modded TUI disclosure"
assert_contains "$actual" '--no-boot -m deepseek-v4-pro' "TUI handoff"

printf '223bb3' | env "${common_env[@]}" "$boot_menu" >/dev/null
assert_eq 'claude' "$(read_state backend)" "Frontier persistence"
assert_eq 'frontier' "$(read_state mode)" "Frontier mode persistence"

printf '%s\n' deepseek > "$test_config/backend"
printf '2\033[D3' | env "${common_env[@]}" "$boot_menu" >/dev/null
assert_eq 'deepseek' "$(read_state backend)" "Left arrow leaves Config without changing backend"

printf '%s\n' frontier > "$test_config/mode"
actual="$(printf '211\nbb3' | env "${common_env[@]}" "$boot_menu")"
assert_contains "$actual" 'TINYGRAD · IN DEVELOPMENT' "tinygrad disabled notice"
assert_eq 'frontier' "$(read_state mode)" "tinygrad cannot switch local mode"

printf '2121\nbbb3' | env "${common_env[@]}" "$boot_menu" >/dev/null
assert_eq 'local' "$(read_state mode)" "Local mode persistence"
assert_eq 'llama' "$(read_state local-runtime)" "llama runtime persistence"
assert_eq "$test_models/A-test.gguf" "$(read_state local-model)" "local model persistence"

printf '%s\n' frontier > "$test_config/mode"
printf '%s\n' codex > "$test_config/backend"
actual="$(printf '11\nb3' | env "${common_env[@]}" "$boot_menu")"
assert_contains "$actual" 'BACKEND NOT CONFIGURED' "unconfigured backend gate"

actual="$(printf '3' | env "${common_env[@]}" "$boot_menu")"
[[ "$actual" != *"Sessions"* ]] || fail "session entries must not appear"
assert_contains "$actual" 'TUI' "main TUI entry"
assert_contains "$actual" 'Config' "main Config entry"
assert_contains "$actual" 'Exit' "main Exit entry"

actual="$(printf '23\nb3' | env "${scan_env[@]}" "$boot_menu")"
assert_contains "$actual" 'CONFIG · GPU AUTO-SCAN' "Config GPU scan entry"
assert_contains "$actual" 'GPU and llama.cpp are aligned' "Config GPU scan success"

actual="$(printf $'2\033[B\033[A1\033[B\033[A2\033[B\033[Bbbb3' | env "${common_env[@]}" COLUMNS=80 LINES=24 "$boot_menu")"
clear_token=$'\033[H\033[J'; clear_count=0; remaining="$actual"
while [[ "$remaining" == *"$clear_token"* ]]; do
  remaining="${remaining#*"$clear_token"}"
  ((clear_count++)) || true
done
assert_ge "$clear_count" 10 "full-frame redraw count"
[[ "$actual" != *'ZZZ-this-model-name-is-deliberately-long-enough-to-wrap-a-terminal-row'* ]] || fail "long model labels must be clipped"
assert_contains "$actual" 'ZZZ-this-model-name' "clipped model label remains identifiable"

set +e
actual="$(env "${common_env[@]}" "$local_manager" start tinygrad "$test_models/A-test.gguf" 2>&1)"
status=$?
set -e
assert_eq '3' "$status" "tinygrad manager disabled status"
assert_contains "$actual" 'in development' "tinygrad manager disabled message"

env "${common_env[@]}" "$boot_menu" </dev/null >/dev/null

echo "Arkey Codex boot mode tests: ok"
