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

  # Test candidate source preflight through the shipped CLI, with a private
  # executable fixture rather than the host's daemon or database.
  mkdir -p "$temp_dir/bin"
  cat > "$temp_dir/bin/vnstat" <<'VNSTAT'
#!/usr/bin/env bash
cat "$SBM_VNSTAT_SMOKE_JSON"
VNSTAT
  chmod 0755 "$temp_dir/bin/vnstat"
  export SBM_VNSTAT_SMOKE_JSON="$temp_dir/vnstat.json"
  sample_epoch="$(date +%s)"
  printf '{"jsonversion":"2","interfaces":[{"name":"ens5","created":{"timestamp":%s},"updated":{"timestamp":%s},"traffic":{"total":{"rx":0,"tx":0}}}]}\n' "$sample_epoch" "$sample_epoch" > "$SBM_VNSTAT_SMOKE_JSON"
  PATH="$temp_dir/bin:$PATH" "$temp_dir/sbm-panel" vnstat-check --config "$temp_dir/not-yet-created.json" --interface ens5 --vnstat-config "$temp_dir/vnstat.conf"
  # JSON v2 without epoch timestamps (vnStat 2.9) must fail clearly.
  printf '{"jsonversion":"2","interfaces":[{"name":"ens5","created":{},"updated":{},"traffic":{"total":{"rx":0,"tx":0}}}]}\n' > "$SBM_VNSTAT_SMOKE_JSON"
  if PATH="$temp_dir/bin:$PATH" "$temp_dir/sbm-panel" vnstat-check --interface ens5 > "$temp_dir/source-error" 2>&1; then
    echo 'candidate accepted vnStat without timestamp support' >&2; exit 1
  fi
  grep -Fq '2.10' "$temp_dir/source-error"

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
  grep -Eq '"version"[[:space:]]*:[[:space:]]*4' "$temp_dir/config.json"
  grep -Eq '"amount"[[:space:]]*:[[:space:]]*0' "$temp_dir/config.json"
  grep -Eq '"unit"[[:space:]]*:[[:space:]]*"GB"' "$temp_dir/config.json"
  if grep -Eiq 'wireguard|companion' "$temp_dir/config.json"; then
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

  for old_version in 1 2 3; do
    printf '{"version":%s}\n' "$old_version" > "$temp_dir/old-config.json"
    if "$temp_dir/sbm-panel" config apply --no-start --config "$temp_dir/old-config.json" --core-config "$temp_dir/old-core.json" --sing-box "$fake_sing_box" >/dev/null 2>&1; then
      echo "old configuration version $old_version was accepted" >&2
      exit 1
    fi
  done
fi
