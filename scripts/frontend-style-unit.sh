#!/usr/bin/env bash
set -Eeuo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if grep -Riq --exclude-dir=dist 'wireguard' "$root_dir/web/src"; then
  echo "Removed WireGuard UI is still present in frontend sources." >&2
  exit 1
fi

if grep -REq --exclude-dir=dist 'quotaUnit|TrafficUnit|trafficQuota\.unit' "$root_dir/web/src"; then
  echo "Removed traffic-unit selector is still present in frontend sources." >&2
  exit 1
fi
