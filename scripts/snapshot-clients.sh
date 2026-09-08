#!/usr/bin/env bash
set -euo pipefail

arkey_user_home="${ARKEY_USER_HOME:-${HOME:?HOME is required}}"
client_root="${ARKEY_CLIENT_ROOT:-${arkey_user_home}/.local/libexec/arkey/clients}"

for dependency in readlink sha256sum; do
  command -v "$dependency" >/dev/null 2>&1 || { echo "$dependency is required to snapshot Arkey clients." >&2; exit 1; }
done

if [[ -e "$client_root" && ( ! -d "$client_root" || -L "$client_root" ) ]]; then
  echo "Arkey client root must be a real directory: $client_root" >&2
  exit 1
fi
mkdir -p "$client_root"
chmod 0700 "$client_root"

first_installed_at() (
  local candidate match
  for candidate in "$@"; do
    if [[ "$candidate" == *"*"* ]]; then
      match="$(compgen -G "$candidate" 2>/dev/null | head -n 1)" || match=""
      if [[ -n "$match" ]]; then
        printf '%s\n' "$match"
        return 0
      fi
      continue
    fi
    if [[ -e "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  printf '%s\n' "$1"
)

snapshot_client() (
  local name="$1" source="$2" destination_dir destination resolved temporary metadata version digest
  destination_dir="${client_root}/${name}"
  destination="${destination_dir}/${name}"

  if [[ ! -e "$source" ]]; then
    printf 'Skipped %-6s (official client not installed at %s)\n' "$name" "$source"
    return 0
  fi
  resolved="$(readlink -f -- "$source")"
  if [[ -z "$resolved" || ! -f "$resolved" || ! -x "$resolved" ]]; then
    echo "Official $name client is not a regular executable: $source" >&2
    return 1
  fi
  if [[ -e "$destination_dir" && ( ! -d "$destination_dir" || -L "$destination_dir" ) ]]; then
    echo "Arkey $name snapshot directory must be a real directory: $destination_dir" >&2
    return 1
  fi
  mkdir -p "$destination_dir"
  chmod 0700 "$destination_dir"

  temporary="$(mktemp "${destination_dir}/.${name}.XXXXXX")"
  metadata="$(mktemp "${destination_dir}/.snapshot.XXXXXX")"
  cleanup_snapshot() {
    rm -f -- "$temporary" "$metadata"
  }
  trap cleanup_snapshot EXIT

  install -m 0755 -- "$resolved" "$temporary"
  digest="$(sha256sum -- "$temporary")"
  digest="${digest%% *}"
  version="$($temporary --version 2>/dev/null | head -n 1 || true)"
  {
    printf 'client=%s\n' "$name"
    printf 'source=%s\n' "$resolved"
    printf 'sha256=%s\n' "$digest"
    printf 'version=%s\n' "$version"
  } >"$metadata"
  chmod 0600 "$metadata"

  mv -f -- "$temporary" "$destination"
  mv -f -- "$metadata" "${destination_dir}/snapshot.env"
  printf 'Snapshotted %-6s %s\n' "$name" "${version:-$digest}"
)

# Each client is looked up in the places its own installer uses. The first path
# that exists wins. Setting the matching ARKEY_*_SOURCE_BIN variable overrides
# the search for that one client.
codex_source="$(first_installed_at \
  "${arkey_user_home}/.local/bin/codex" \
  "/opt/homebrew/lib/node_modules/@openai/codex/node_modules/@openai/codex-"*"/vendor/"*"/bin/codex" \
  "/usr/local/lib/node_modules/@openai/codex/node_modules/@openai/codex-"*"/vendor/"*"/bin/codex" \
  "${arkey_user_home}/.npm-global/lib/node_modules/@openai/codex/node_modules/@openai/codex-"*"/vendor/"*"/bin/codex")"
claude_source="$(first_installed_at \
  "${arkey_user_home}/.local/bin/claude" \
  "${arkey_user_home}/.claude/local/claude" \
  "/opt/homebrew/bin/claude" \
  "/usr/local/bin/claude")"
kimi_source="$(first_installed_at \
  "${arkey_user_home}/.kimi-code/bin/kimi" \
  "${arkey_user_home}/.local/bin/kimi")"
crush_source="$(first_installed_at \
  "${arkey_user_home}/.local/bin/crush" \
  "/opt/homebrew/bin/crush" \
  "/usr/local/bin/crush" \
  "${arkey_user_home}/go/bin/crush")"

snapshot_client codex "${ARKEY_CODEX_SOURCE_BIN:-$codex_source}"
snapshot_client claude "${ARKEY_CLAUDE_SOURCE_BIN:-$claude_source}"
snapshot_client kimi "${ARKEY_KIMI_SOURCE_BIN:-$kimi_source}"
snapshot_client crush "${ARKEY_CRUSH_SOURCE_BIN:-$crush_source}"

printf 'Arkey-owned client snapshots: %s\n' "$client_root"
