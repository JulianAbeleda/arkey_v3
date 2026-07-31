#!/usr/bin/env bash
# Run an Arkey Codex task, nudging the model when it stops without finishing.
#
# Local models reliably end a turn by announcing an action instead of taking it
# ("Now let me read the file"). The agent loop treats a turn with no tool call as
# "done", so the task stalls and a human has to type "continue". Observed on
# Qwen3.6-27B via llama.cpp; see llama.cpp#20837 and #20164. Disabling reasoning
# does not fix it.
#
# Detection is by BEHAVIOUR, not by text. An earlier version classified the final
# message with a regex and was wrong in both directions: it missed "Let me get
# the parts I need" (the verb was not in its list) and fired on "Step 2 is
# complete... I'll note the suite stayed at 2 failures" (a real completion that
# happens to contain "I'll"). Prose does not reliably distinguish "finished" from
# "stopped early".
#
# What does: whether the model is still doing anything. After each run we count
# the tool calls in the session transcript. If a nudge produces new tool calls,
# the model had more work to do and was stalled. If a nudge produces none, it was
# genuinely finished. The cost of being wrong is one extra turn.
set -euo pipefail

max_nudges="${ARKEY_MAX_NUDGES:-8}"
arkey_bin="${ARKEY_BIN:-$HOME/.local/bin/arkey}"
sessions="${CODEX_HOME:-$HOME/.codex-moonbridge}/sessions"
last_reply="$(mktemp)"
trap 'rm -f "$last_reply"' EXIT

[[ "$#" -ge 1 ]] || { echo "usage: arkey-continue.sh PROMPT [arkey exec args...]" >&2; exit 2; }
prompt="$1"; shift

# Tool calls recorded in the most recently written session transcript.
tool_calls() {
  local newest
  newest="$(find "$sessions" -name '*.jsonl' -printf '%T@ %p\n' 2>/dev/null | sort -rn | head -1 | cut -d' ' -f2-)"
  [[ -n "$newest" ]] || { echo 0; return; }
  python3 - "$newest" <<'PY'
import json,sys
n=0
for line in open(sys.argv[1], errors="replace"):
    line=line.strip()
    if not line: continue
    try: r=json.loads(line)
    except Exception: continue
    if (r.get("payload") or r).get("type")=="function_call": n+=1
print(n)
PY
}

echo "arkey-continue: starting (ceiling $max_nudges nudges)" >&2
"$arkey_bin" exec "$@" -o "$last_reply" "$prompt" < /dev/null || true
calls="$(tool_calls)"
echo "arkey-continue: initial turn made $calls tool call(s)" >&2

nudges=0
while (( nudges < max_nudges )); do
  nudges=$(( nudges + 1 ))
  echo "arkey-continue: nudge $nudges/$max_nudges" >&2
  "$arkey_bin" exec "$@" resume --last -o "$last_reply" \
    "Continue the task from exactly where you stopped. Take the next action now. Do not summarize what you have done and do not ask whether to proceed. If the task is genuinely complete, reply with the single word DONE and nothing else." \
    < /dev/null || break
  after="$(tool_calls)"
  if (( after <= calls )); then
    # No new tool calls means one of two very different things: the model is
    # done, or it is stuck restating an intention it cannot act on (observed
    # when a file exceeds the tool-output limit and the model will not chunk
    # its own reads). The prompt asks for the literal word DONE on completion,
    # so use that to tell them apart rather than assuming the happy case.
    if grep -qiE '(^|[[:space:]])done([[:space:]]|[[:punct:]]|$)' "$last_reply" 2>/dev/null; then
      echo "arkey-continue: model reported DONE and took no further action -- finished" >&2
    else
      echo "arkey-continue: STUCK -- nudge produced no tool calls and no DONE." >&2
      echo "arkey-continue: the model is restating an intention it cannot act on." >&2
      exit 4
    fi
    nudges=$(( nudges - 1 ))
    break
  fi
  echo "arkey-continue: nudge produced $(( after - calls )) new tool call(s) -- was stalled" >&2
  calls="$after"
done

if (( nudges >= max_nudges )); then
  echo "arkey-continue: hit the nudge ceiling ($max_nudges); model is not converging" >&2
  exit 3
fi
echo "arkey-continue: finished after $nudges effective nudge(s), $calls tool call(s) total" >&2
