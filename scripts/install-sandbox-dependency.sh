#!/usr/bin/env bash
# Let bubblewrap create user namespaces where AppArmor restricts them, so the
# Codex and Claude Code sandboxes work. Kimi and Crush do not sandbox through
# bwrap and are unaffected either way.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
template="${repo_root}/dependencies/apparmor/arkey-bwrap"
profile="/etc/apparmor.d/arkey-bwrap"

[[ "$(uname -s)" == Linux ]] || exit 0
restricted="$(sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null || echo 0)"
if [[ "$restricted" != 1 ]]; then
  echo "Sandbox: user namespaces are not restricted by AppArmor; nothing to install."
  exit 0
fi
# Newer AppArmor releases ship their own profile for bwrap; two profiles
# attaching to one path conflict, so the distro's wins.
if [[ -e /etc/apparmor.d/bwrap-userns-restrict ]]; then
  echo "Sandbox: the distribution's bwrap-userns-restrict profile is installed; nothing to do."
  exit 0
fi

bwrap="$(command -v bwrap || true)"
if [[ -z "$bwrap" ]]; then
  echo "Sandbox: bubblewrap is not installed (apt install bubblewrap); Codex and Claude Code sandboxes stay unavailable." >&2
  exit 0
fi
bwrap="$(readlink -f -- "$bwrap")"
# A profile that grants userns must name a binary no user can replace.
if [[ "$(stat -c %u "$bwrap")" != 0 ]] || find "$bwrap" -perm /022 | grep -q .; then
  echo "Sandbox: $bwrap is not a root-owned, non-writable binary; refusing to grant it user namespaces." >&2
  exit 1
fi
[[ -r "$template" ]] || { echo "Sandbox AppArmor template is missing: $template" >&2; exit 1; }

# Claude Code's Linux sandbox also proxies network through socat.
note_socat() {
  command -v socat >/dev/null 2>&1 || echo "Sandbox: socat is not installed (apt install socat); Claude Code's sandbox needs it, Codex's does not."
}

verify() { "$bwrap" --ro-bind / / --unshare-user --unshare-net -- true >/dev/null 2>&1; }

rendered="$(sed "s|@BWRAP@|${bwrap}|" "$template")"
if [[ -f "$profile" ]] && [[ "$(cat "$profile")" == "$rendered" ]] && verify; then
  echo "Sandbox: AppArmor profile for $bwrap already loaded."
  note_socat
  exit 0
fi
if ! sudo -n true 2>/dev/null; then
  echo "Sandbox: installing $profile needs root. Run this script with sudo available, or" >&2
  echo "Sandbox: set ARKEY_SKIP_SANDBOX_INSTALL=1 to skip it. Codex and Claude Code sandboxes stay unavailable." >&2
  exit 0
fi
printf '%s\n' "$rendered" | sudo -n install -m 0644 -o root -g root /dev/stdin "$profile"
sudo -n apparmor_parser -r "$profile"

if ! verify; then
  echo "Sandbox: loaded $profile but bwrap still cannot create a user namespace." >&2
  exit 1
fi
echo "Sandbox: installed $profile; bwrap can create user namespaces."
note_socat
