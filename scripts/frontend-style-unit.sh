#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if grep -Riq --exclude-dir=dist 'wireguard' "$root_dir/web/src"; then
  echo "Removed WireGuard UI is still present in frontend sources." >&2
  exit 1
fi

if ! grep -Fq "value: 'GB'" "$root_dir/web/src/views/SettingsView.vue" ||
   ! grep -Fq "value: 'GiB'" "$root_dir/web/src/views/SettingsView.vue"; then
  echo "Traffic-unit selector must offer GB and GiB." >&2
  exit 1
fi
