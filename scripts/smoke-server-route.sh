#!/usr/bin/env bash
# The server route, end to end, on the installed Arkey: select the server
# without the screen, launch Codex through MoonBridge against it, and check
# the reply, the absence of the two launch warnings, and MoonBridge's route
# line. Needs a reachable llama.cpp server and a snapshotted Codex.
#
#   scripts/smoke-server-route.sh http://100.106.46.126:8080
set -euo pipefail
origin="${1:?usage: smoke-server-route.sh ORIGIN}"
arkey="${ARKEY_BIN:-$HOME/.local/bin/arkey}"
log="${XDG_STATE_HOME:-$HOME/.local/state}/arkey/logs/moonbridge.log"
workspace="$(mktemp -d)"
trap 'rm -rf "$workspace"' EXIT
fail=0
say() { printf '%s\n' "$*" >&2; }

selected="$("$arkey" --select-server="$origin")"
say "$selected"
case "$selected" in *"server reachable"*) ;; *) say "FAIL: the server was not selected as reachable"; fail=1 ;; esac

marker="ready-$(date +%s)"
cd "$workspace"
output="$(env -u ARKEY_MOONBRIDGE_TOKEN perl -e 'alarm 180; exec @ARGV' "$arkey" --no-boot exec "Reply with exactly this word and nothing else: $marker" </dev/null 2>&1 || true)"
if ! grep -q "$marker" <<<"$output" || ! grep -q '^ready-' <<<"$(grep -o "$marker" <<<"$output" | tail -1)"; then
  say "FAIL: the reply did not carry the marker"; say "$output" | tail -20; fail=1
fi
for bad in "Missing environment variable" "Model metadata for" "Arkey route:" "Arkey client:" "ReasoningSummary"; do
  if grep -q "$bad" <<<"$output"; then say "FAIL: launch said: $bad"; fail=1; fi
done
if ! tail -50 "$log" | grep -q 'model=arkey-server-llama'; then
  say "FAIL: MoonBridge did not log the server route"; fail=1
fi
if [[ $fail -eq 0 ]]; then say "PASS: Codex answered through MoonBridge from $origin"; fi
exit "$fail"
