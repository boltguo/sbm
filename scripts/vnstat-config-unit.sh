#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
source "$root/scripts/configure-vnstat.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
warn(){ printf '%s\n' "$*" >&2; }
systemctl(){ return 0; }
cat > "$work/vnstat.conf" <<'CONFIG'
DatabaseDir "/var/lib/vnstat"
UseUTC 0
DailyDays -1
HourlyDays 80
SaveInterval 5
# retain comments and other settings
MaxBandwidth 0
CONFIG
configure_vnstat "$work/vnstat.conf"
grep -Fxq 'DailyDays -1' "$work/vnstat.conf"
grep -Fxq 'HourlyDays 80' "$work/vnstat.conf"
grep -Fxq 'UseUTC 1' "$work/vnstat.conf"
grep -Fxq 'SaveInterval 1' "$work/vnstat.conf"
grep -Fxq 'MaxBandwidth 0' "$work/vnstat.conf"
grep -Fxq 'UseUTC 0' "$work/vnstat.conf.before-sbm"
cp "$work/vnstat.conf" "$work/first"
configure_vnstat "$work/vnstat.conf"
cmp -s "$work/vnstat.conf" "$work/first"
grep -Fxq 'UseUTC 0' "$work/vnstat.conf.before-sbm"
# Keep installer and tested helper in sync.
awk '/^configure_vnstat\(\) \{/{capture=1} capture{print} capture&&/^\}/{exit}' "$root/install.sh" > "$work/embedded"
awk '/^configure_vnstat\(\) \{/{capture=1} capture{print} capture&&/^\}/{exit}' "$root/scripts/configure-vnstat.sh" > "$work/helper"
cmp -s "$work/embedded" "$work/helper"
printf 'vnStat configuration preservation and idempotence: passed\n'
