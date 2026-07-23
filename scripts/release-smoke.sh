#!/usr/bin/env bash
set -Eeuo pipefail

version="${1:-ci}"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_dir="$(mktemp -d /tmp/sbm-release-smoke.XXXXXX)"
trap 'rm -rf "$temp_dir"' EXIT

cd "$root_dir"
make release VERSION="$version"
if command -v sha256sum >/dev/null; then
  (cd dist && sha256sum --check checksums.txt)
else
  (cd dist && shasum -a 256 --check checksums.txt)
fi

for arch in amd64 arm64; do
  archive="dist/sbm-panel_${version}_linux_${arch}.tar.gz"
  [[ -f "$archive" ]]
  contents="$(tar -tzf "$archive")"
  grep -Fxq sbm-panel <<<"$contents"
  grep -Fxq sbm <<<"$contents"
done

tar -xzf "dist/sbm-panel_${version}_linux_amd64.tar.gz" -C "$temp_dir"
bash -n "$temp_dir/sbm"
if [[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]]; then
  [[ "$("$temp_dir/sbm-panel" version)" == "$version" ]]
fi
