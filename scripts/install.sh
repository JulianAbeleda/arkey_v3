#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
arkey_user_home="${ARKEY_USER_HOME:-${HOME:?HOME is required}}"
install_dir="${ARKEY_INSTALL_DIR:-${arkey_user_home}/.local/bin}"
config_dir="${ARKEY_CONFIG_DIR:-${XDG_CONFIG_HOME:-${arkey_user_home}/.config}/arkey}"
moonbridge_config="${MOONBRIDGE_CONFIG:-${config_dir}/moonbridge.yml}"

for dependency in go git; do
  command -v "$dependency" >/dev/null 2>&1 || { echo "$dependency is required to install Arkey." >&2; exit 1; }
done

if [[ "${1:-}" == "--check" ]]; then
  [[ "$#" -eq 1 ]] || exit 2
  check_dir="$(mktemp -d)"
  trap 'find "$check_dir" -type f -delete 2>/dev/null || true; find "$check_dir" -depth -type d -empty -delete 2>/dev/null || true' EXIT
  (
    cd "$repo_root"
    GOTOOLCHAIN=auto go mod verify
    GOTOOLCHAIN=auto go test ./...
    GOTOOLCHAIN=auto go build -o "$check_dir/arkey" ./cmd/arkey
  )
  echo "Arkey Bubble Tea install check: ok"
  exit 0
fi
if [[ "$#" -ne 0 ]]; then
  echo "Usage: scripts/install.sh [--check]" >&2
  exit 2
fi

selected_toolchain="$(cd "$repo_root" && GOTOOLCHAIN=auto go env GOVERSION)"
case "$selected_toolchain" in
  go1.2[5-9]*|go1.[3-9][0-9]*) ;;
  *) echo "Arkey requires Go 1.25 or newer; selected: $selected_toolchain" >&2; exit 1 ;;
esac

if [[ "${ARKEY_SKIP_MOONBRIDGE_INSTALL:-0}" != 1 ]]; then
  "$repo_root/scripts/install-moonbridge-dependency.sh"
fi
if [[ "${ARKEY_SKIP_CLIENT_SNAPSHOTS:-0}" != 1 ]]; then
  "$repo_root/scripts/snapshot-clients.sh"
fi

build_dir="$(mktemp -d)"
cleanup() {
  find "$build_dir" -type f -delete 2>/dev/null || true
  find "$build_dir" -depth -type d -empty -delete 2>/dev/null || true
}
trap cleanup EXIT

commit="$(git -C "$repo_root" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)"
(
  cd "$repo_root"
  GOTOOLCHAIN=auto go build -trimpath \
    -ldflags "-s -w -X main.version=3.0.0-dev -X main.commit=${commit}" \
    -o "$build_dir/arkey" ./cmd/arkey
)

mkdir -p "$install_dir" "$config_dir"
chmod 0700 "$config_dir"
legacy_moonbridge_config="${arkey_user_home}/moon-bridge/config.yml"
if [[ ! -e "$moonbridge_config" && ! -f "$legacy_moonbridge_config" ]]; then
  install -m 0600 "$repo_root/dependencies/moonbridge.example.yml" "$moonbridge_config"
  echo "Installed credential-free MoonBridge template: $moonbridge_config"
elif [[ -f "$legacy_moonbridge_config" ]]; then
  chmod 0600 "$legacy_moonbridge_config"
  echo "Preserved existing MoonBridge configuration: $legacy_moonbridge_config"
fi

install -m 0755 "$build_dir/arkey" "$install_dir/arkey"

# The Bash implementation was removed after its compatibility release. Clear
# any helper copies an earlier install left behind so no stale rollback
# command stays on PATH.
#
# arkey-local-runtime and codex-moonbridge are deliberately NOT removed: they
# predate the boot manager and users may have their own wrappers exec'ing
# codex-moonbridge, which in turn calls arkey-local-runtime to start
# MoonBridge. Deleting them here would break tooling Arkey does not own.
for obsolete in arkey-legacy arkey-boot-menu arkey-hardware-scan arkey-boot-lib; do
  if [[ -e "$install_dir/$obsolete" ]]; then
    rm -f "$install_dir/$obsolete"
    echo "Removed obsolete Bash helper: $install_dir/$obsolete"
  fi
done

"$install_dir/arkey" --version
echo "Installed Arkey: $install_dir/arkey"
