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

  fake_sing_box="$temp_dir/sing-box"
  password_file="$temp_dir/password"
  printf '%s\n' 'temporary-admin-password' > "$password_file"
  cat > "$fake_sing_box" <<'FAKE'
#!/usr/bin/env bash
set -Eeuo pipefail
case "${1:-} ${2:-}" in
  "generate uuid") printf '70d0c699-73a0-4d2a-a45d-4f46a661b4f2\n' ;;
  "generate reality-keypair") printf 'PrivateKey: private-key\nPublicKey: public-key\n' ;;
  "check -c") exit 0 ;;
  "version ") printf 'sing-box version smoke\n' ;;
  *) exit 2 ;;
esac
FAKE
  chmod 0755 "$fake_sing_box"
  "$temp_dir/sbm-panel" init \
    --config "$temp_dir/config.json" --state "$temp_dir/state.json" \
    --core-config "$temp_dir/core.json" --sing-box "$fake_sing_box" \
    --domain node.example.com --admin-password-file "$password_file"
  grep -Eq '"version"[[:space:]]*:[[:space:]]*3' "$temp_dir/config.json"
  grep -Eq '"amountGB"[[:space:]]*:[[:space:]]*0' "$temp_dir/config.json"
  if grep -Eiq 'wireguard|companion|"unit"[[:space:]]*:' "$temp_dir/config.json"; then
    echo "fresh business configuration contains removed fields" >&2
    exit 1
  fi
  "$temp_dir/sbm-panel" config apply --no-start \
    --config "$temp_dir/config.json" --state "$temp_dir/state.json" \
    --core-config "$temp_dir/core.json" --sing-box "$fake_sing_box"
  if grep -Eiq 'wireguard|exit-wireguard|auth_user|"endpoints"' "$temp_dir/core.json"; then
    echo "generated core configuration contains removed fields" >&2
    exit 1
  fi

  for old_version in 1 2; do
    printf '{"version":%s}\n' "$old_version" > "$temp_dir/old-config.json"
    if "$temp_dir/sbm-panel" config apply --no-start --config "$temp_dir/old-config.json" --core-config "$temp_dir/old-core.json" --sing-box "$fake_sing_box" >/dev/null 2>&1; then
      echo "old configuration version $old_version was accepted" >&2
      exit 1
    fi
  done
fi
