#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
style_file="$root_dir/web/src/style.css"

if grep -Eq '\.wireguard-section(\.active)?::before' "$style_file"; then
  echo "WireGuard settings must not draw a standalone left-edge pseudo-element." >&2
  exit 1
fi
