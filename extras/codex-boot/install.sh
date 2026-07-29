#!/usr/bin/env bash
set -euo pipefail

mode_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${mode_root}/../.." && pwd)"
install_dir="${ARKEY_INSTALL_DIR:-${HOME}/.local/bin}"
config_dir="${ARKEY_CONFIG_DIR:-${XDG_CONFIG_HOME:-${HOME}/.config}/arkey}"
moonbridge_config="${MOONBRIDGE_CONFIG:-${config_dir}/moonbridge.yml}"
dependency_installer="${repo_root}/scripts/install-moonbridge-dependency.sh"

bash -n "$mode_root/arkey" "$mode_root/arkey-boot-menu" "$mode_root/arkey-boot-lib" \
  "$mode_root/arkey-local-runtime" "$mode_root/arkey-hardware-scan" \
  "$mode_root/codex-moonbridge" "$dependency_installer"
[[ -r "${repo_root}/dependencies/moonbridge.env" ]] || { echo 'MoonBridge dependency lock is missing.' >&2; exit 1; }

if [[ "${1:-}" == "--check" ]]; then
  echo "Arkey Codex boot mode check: ok"
  exit 0
fi

if [[ "$#" -gt 0 ]]; then
  echo "Usage: extras/codex-boot/install.sh [--check]" >&2
  exit 2
fi

mkdir -p "$install_dir"
if [[ "${ARKEY_SKIP_MOONBRIDGE_INSTALL:-0}" != 1 ]]; then
  "$dependency_installer"
fi
mkdir -p "$config_dir"
if [[ ! -e "$moonbridge_config" ]]; then
  install -m 0600 "$mode_root/moonbridge.example.yml" "$moonbridge_config"
  echo "Installed credential-free MoonBridge template: $moonbridge_config"
fi
install -m 0755 "$mode_root/arkey" "$install_dir/arkey"
install -m 0755 "$mode_root/arkey-boot-menu" "$install_dir/arkey-boot-menu"
install -m 0755 "$mode_root/arkey-local-runtime" "$install_dir/arkey-local-runtime"
install -m 0755 "$mode_root/arkey-hardware-scan" "$install_dir/arkey-hardware-scan"
install -m 0755 "$mode_root/codex-moonbridge" "$install_dir/codex-moonbridge"
install -m 0644 "$mode_root/arkey-boot-lib" "$install_dir/arkey-boot-lib"

echo "Installed Arkey boot launcher: $install_dir/arkey"
echo "Installed Arkey boot menu: $install_dir/arkey-boot-menu"
echo "Installed Arkey local runtime manager: $install_dir/arkey-local-runtime"
echo "Installed Arkey GPU scanner: $install_dir/arkey-hardware-scan"
echo "Installed Arkey backend config: $install_dir/arkey-boot-lib"
echo "Installed Arkey MoonBridge launcher: $install_dir/codex-moonbridge"
echo "Edit $moonbridge_config locally to configure frontier API credentials."
