#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lock_file="${ARKEY_BRAVE_SEARCH_LOCK:-${repo_root}/dependencies/brave-search.env}"
patch_file="${repo_root}/dependencies/brave-search-rate-limit.patch"
arkey_user_home="${ARKEY_USER_HOME:-${HOME:?HOME is required}}"
libexec_dir="${ARKEY_LIBEXEC_DIR:-${arkey_user_home}/.local/libexec/arkey}"
install_dir="${ARKEY_BRAVE_SEARCH_DIR:-${libexec_dir}/mcp/brave-search}"

[[ -r "$lock_file" ]] || { echo "Brave Search dependency lock is missing: $lock_file" >&2; exit 1; }
# shellcheck source=../dependencies/brave-search.env
source "$lock_file"
: "${BRAVE_SEARCH_PACKAGE:?Brave Search package is missing from dependency lock}"
: "${BRAVE_SEARCH_VERSION:?Brave Search version is missing from dependency lock}"
[[ "$BRAVE_SEARCH_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Brave Search version must be a plain semver (x.y.z).' >&2; exit 1; }
[[ -r "$patch_file" ]] || { echo "Brave Search rate-limit patch is missing: $patch_file" >&2; exit 1; }

for dependency in npm node patch; do
  command -v "$dependency" >/dev/null 2>&1 || { echo "$dependency is required to install Brave Search." >&2; exit 1; }
done

mkdir -p "$install_dir"
npm install --no-save --prefix "$install_dir" "${BRAVE_SEARCH_PACKAGE}@${BRAVE_SEARCH_VERSION}" >/dev/null

package_dir="${install_dir}/node_modules/${BRAVE_SEARCH_PACKAGE}"
target="${package_dir}/dist/index.js"
[[ -f "$target" ]] || { echo "Expected Brave Search build output missing: $target" >&2; exit 1; }

installed_version="$(node -p "require('${package_dir}/package.json').version")"
[[ "$installed_version" == "$BRAVE_SEARCH_VERSION" ]] || {
  echo "Installed Brave Search version ($installed_version) does not match pin ($BRAVE_SEARCH_VERSION)." >&2
  exit 1
}

marker='Patched for Arkey'
if grep -qF "$marker" "$target"; then
  : # already patched by a previous run; nothing to do
else
  patch -p0 --directory "$package_dir" < "$patch_file" || {
    echo "Brave Search rate-limit patch failed to apply; upstream $BRAVE_SEARCH_VERSION may have changed." >&2
    exit 1
  }
fi

node --check "$target" || { echo "Patched Brave Search server failed syntax check: $target" >&2; exit 1; }

cat <<EOF
Installed Brave Search MCP server $BRAVE_SEARCH_VERSION (rate-limit patch applied): $target

Add this to your Codex config (BRAVE_API_KEY is yours to set; Arkey never
stores or reads it):

[mcp_servers.brave]
command = "node"
args = ["$target"]
env = { "BRAVE_API_KEY" = "<your-brave-api-key>" }
EOF
