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

snapshot_client codex "${ARKEY_CODEX_SOURCE_BIN:-${arkey_user_home}/.local/bin/codex}"
snapshot_client claude "${ARKEY_CLAUDE_SOURCE_BIN:-${arkey_user_home}/.local/bin/claude}"
snapshot_client kimi "${ARKEY_KIMI_SOURCE_BIN:-${arkey_user_home}/.kimi-code/bin/kimi}"

printf 'Arkey-owned client snapshots: %s\n' "$client_root"
