#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lock_file="${ARKEY_MOONBRIDGE_LOCK:-${repo_root}/dependencies/moonbridge.env}"
arkey_user_home="${ARKEY_USER_HOME:-${HOME:?HOME is required}}"
data_root="${ARKEY_DATA_DIR:-${XDG_DATA_HOME:-${arkey_user_home}/.local/share}/arkey}"
source_dir="${ARKEY_MOONBRIDGE_SOURCE:-${data_root}/dependencies/moon-bridge-arkey}"
install_dir="${ARKEY_LIBEXEC_DIR:-${arkey_user_home}/.local/libexec/arkey}"

[[ -r "$lock_file" ]] || { echo "MoonBridge dependency lock is missing: $lock_file" >&2; exit 1; }
# shellcheck source=../dependencies/moonbridge.env
source "$lock_file"
: "${MOONBRIDGE_REPOSITORY:?MoonBridge repository is missing from dependency lock}"
: "${MOONBRIDGE_REVISION:?MoonBridge revision is missing from dependency lock}"
[[ "$MOONBRIDGE_REVISION" =~ ^[0-9a-f]{40}$ ]] || { echo 'MoonBridge revision must be a full Git commit.' >&2; exit 1; }

for dependency in git go; do
  command -v "$dependency" >/dev/null 2>&1 || { echo "$dependency is required to install MoonBridge." >&2; exit 1; }
done

mkdir -p "$(dirname "$source_dir")" "$install_dir"
if [[ -d "$source_dir/.git" ]]; then
  [[ -z "$(git -C "$source_dir" status --porcelain)" ]] || {
    echo "Refusing to replace a modified MoonBridge dependency checkout: $source_dir" >&2
    exit 1
  }
  git -C "$source_dir" remote set-url origin "$MOONBRIDGE_REPOSITORY"
else
  [[ ! -e "$source_dir" ]] || { echo "Dependency path exists but is not a Git checkout: $source_dir" >&2; exit 1; }
  git clone --quiet --filter=blob:none --no-checkout "$MOONBRIDGE_REPOSITORY" "$source_dir"
fi

git -C "$source_dir" fetch --quiet --depth 1 origin "$MOONBRIDGE_REVISION"
git -C "$source_dir" checkout --quiet --detach "$MOONBRIDGE_REVISION"
[[ "$(git -C "$source_dir" rev-parse HEAD)" == "$MOONBRIDGE_REVISION" ]] || {
  echo 'MoonBridge dependency revision verification failed.' >&2
  exit 1
}

build_dir="$(mktemp -d)"
trap 'find "$build_dir" -type f -delete 2>/dev/null || true; find "$build_dir" -depth -type d -empty -delete 2>/dev/null || true' EXIT
(
  cd "$source_dir"
  go build -trimpath -o "$build_dir/moonbridge" ./cmd/moonbridge
)
install -m 0755 "$build_dir/moonbridge" "$install_dir/moonbridge"
printf 'Installed MoonBridge %s: %s\n' "$MOONBRIDGE_REVISION" "$install_dir/moonbridge"
