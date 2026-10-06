#!/usr/bin/env bash
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d /tmp/sbm-vnstat-native.XXXXXX)"
cleanup() {
  sudo chown -R "$(id -u):$(id -g)" "$fixture"
  rm -rf "$fixture"
}
trap cleanup EXIT
mkdir -p "$fixture/extracted/usr/bin" "$fixture/extracted/usr/sbin"
cp "$(command -v vnstat)" "$fixture/extracted/usr/bin/vnstat"
cp "$(command -v vnstatd)" "$fixture/extracted/usr/sbin/vnstatd"
cd "$root_dir"
go test -c -o "$fixture/nettraffic.test" ./internal/nettraffic
go test -c -o "$fixture/traffic.test" ./internal/traffic
sudo unshare --mount --net bash "$root_dir/scripts/vnstat-native-fixture.sh" "$fixture"
