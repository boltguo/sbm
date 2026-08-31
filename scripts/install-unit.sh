#!/usr/bin/env bash
set -Eeuo pipefail
trap 'status=$?; printf "install-unit failed at line %d: %s\n" "$LINENO" "$BASH_COMMAND" >&2; exit "$status"' ERR

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The README tells people to run the installer as a single command, which leaves
# BASH_SOURCE empty. Under `set -u` that used to abort before main ever ran, so
# every supported invocation has to reach the root check rather than die early.
for invocation in single-command process-substitution; do
  case "$invocation" in
    single-command) output="$(bash -c "$(cat "$root_dir/install.sh")" 2>&1 || true)" ;;
    process-substitution) output="$(bash <(cat "$root_dir/install.sh") 2>&1 || true)" ;;
  esac
  if [[ "$output" != *"root"* ]]; then
    printf '%s invocation did not reach the root check: %s\n' "$invocation" "$output" >&2
    exit 1
  fi
  if [[ "$output" == *"unbound variable"* ]]; then
    printf '%s invocation tripped set -u: %s\n' "$invocation" "$output" >&2
    exit 1
  fi
done

# The installer is linted separately; this path is resolved dynamically.
# shellcheck disable=SC1091
source "$root_dir/install.sh"

[[ "$(normalize_tag 1.2.3)" == v1.2.3 ]]
[[ "$(normalize_tag v1.2.3)" == v1.2.3 ]]
[[ "$(normalize_tag 2.0.0)" == v2.0.0 ]]

repo_version="$(tr -d '[:space:]' < "$root_dir/VERSION")"
declare -p SBM_RELEASE_VERSION >/dev/null
release_version="$(normalize_tag "$SBM_RELEASE_VERSION")"
[[ "$release_version" == "v${repo_version}" ]]
compatible_version="$(compatible_sing_box_version v1.2.0)"
[[ "$compatible_version" == v1.13.14 ]]
compatible_version="$(compatible_sing_box_version v1.2.1)"
[[ "$compatible_version" == v1.13.14 ]]
compatible_version="$(compatible_sing_box_version v1.2.2)"
[[ "$compatible_version" == v1.13.14 ]]
compatible_version="$(compatible_sing_box_version v1.2.3)"
[[ "$compatible_version" == v1.13.14 ]]
compatible_version="$(compatible_sing_box_version v2.0.0)"
[[ "$compatible_version" == v1.13.14 ]]

unset SBM_VERSION SBM_PANEL_VERSION SING_BOX_VERSION
selected_version="$(requested_sbm_version)"
[[ "$selected_version" == "v${repo_version}" ]]
SBM_VERSION=1.2.0
selected_version="$(requested_sbm_version)"
[[ "$selected_version" == v1.2.0 ]]
selected_core_version="$(requested_sing_box_version "$selected_version")"
[[ "$selected_core_version" == v1.13.14 ]]
unset SBM_VERSION
SBM_PANEL_VERSION=v1.2.0
selected_version="$(requested_sbm_version)"
[[ "$selected_version" == v1.2.0 ]]
unset SBM_PANEL_VERSION
SING_BOX_VERSION=1.13.12
selected_core_version="$(requested_sing_box_version v1.2.0)"
[[ "$selected_core_version" == v1.13.12 ]]
unset SING_BOX_VERSION

if (compatible_sing_box_version v9.9.9 >/dev/null 2>&1); then
  echo "unknown SBM release received an untested sing-box version" >&2
  exit 1
fi

(
  github_latest_tag() { printf 'v2.0.0\n'; }
  unset SBM_VERSION SBM_PANEL_VERSION
  update_target="$(panel_update_target_version)"
  [[ "$update_target" == v2.0.0 ]]
  SBM_VERSION=1.2.0
  update_target="$(panel_update_target_version)"
  [[ "$update_target" == v1.2.0 ]]
)

fresh_config="$(mktemp /tmp/sbm-v3-config.XXXXXX)"
old_config="$(mktemp /tmp/sbm-v2-config.XXXXXX)"
printf '{"version":3}\n' > "$fresh_config"
printf '{"version":2}\n' > "$old_config"
assert_panel_config_supported v2.0.0 "$fresh_config"
if (assert_panel_config_supported v2.0.0 "$old_config" >/dev/null 2>&1); then
  echo "SBM 2.x accepted an old configuration" >&2
  exit 1
fi
rm -f "$fresh_config" "$old_config"

(
  installed_panel_version() { printf '1.2.0\n'; }
  unset SING_BOX_VERSION
  update_target="$(core_update_target_version)"
  [[ "$update_target" == v1.13.14 ]]
  SING_BOX_VERSION=1.13.12
  update_target="$(core_update_target_version)"
  [[ "$update_target" == v1.13.12 ]]
)

[[ "$(cleanup_domain ' HTTPS://Node.Example.COM:2096/path ')" == node.example.com ]]
validate_domain node.example.com
custom_panel_port=24443
validate_panel_port "$custom_panel_port"
[[ "$(country_flag US)" == "🇺🇸" ]]
# Node names use the uppercase country code, not the full country name.
[[ "$(location_node_name Japan JP Tokyo)" == "JP-Tokyo" ]]
[[ "$(location_node_name Singapore SG Singapore)" == "SG-Singapore" ]]
[[ "$(location_node_name 'United States' US Seattle)" == "US-Seattle" ]]
[[ "$(location_node_name '' jp Tokyo)" == "JP-Tokyo" ]]
[[ "$(location_node_name JP JP '')" == "JP" ]]
# Without a country code the full name is the only thing left to use.
[[ "$(location_node_name Japan '' Tokyo)" == "Japan-Tokyo" ]]
[[ "$(location_node_name Singapore '' Singapore)" == "Singapore" ]]
validate_node_name "JP-Tokyo"
[[ "$(urlencode_fragment 'Japan Tokyo#1')" == "Japan%20Tokyo%231" ]]
[[ "$(cloud_provider_name oracle)" == "Oracle Cloud (OCI)" ]]
[[ "$(cloud_provider_name alibaba)" == "Alibaba Cloud ECS / 阿里云" ]]
[[ "$(cloud_provider_name tencent)" == "Tencent Cloud CVM / 腾讯云" ]]
[[ "$(cloud_provider_name generic)" == "通用 KVM（含 DMIT 等）" ]]
[[ "$(cloud_provider_from_identity 'Amazon EC2')" == aws ]]
[[ "$(cloud_provider_from_identity 'Alibaba Cloud ECS')" == alibaba ]]
[[ "$(cloud_provider_from_identity 'aliyun')" == alibaba ]]
[[ "$(cloud_provider_from_identity 'Tencent Cloud')" == tencent ]]
[[ "$(cloud_provider_from_identity 'QCloud CVM')" == tencent ]]
[[ "$(cloud_provider_from_identity 'unknown kvm vendor')" == generic ]]

if (validate_domain invalid >/dev/null 2>&1); then
  echo "invalid domain was accepted" >&2
  exit 1
fi
if (validate_panel_port 70000 >/dev/null 2>&1); then
  echo "invalid panel port was accepted" >&2
  exit 1
fi
if (validate_node_name "" >/dev/null 2>&1); then
  echo "empty node name was accepted" >&2
  exit 1
fi
if (validate_node_name $'bad\nname' >/dev/null 2>&1); then
  echo "node name with a control character was accepted" >&2
  exit 1
fi

ufw() { [[ "${1:-}" == status ]] && printf 'Status: active\n'; }
[[ "$(detect_host_firewall_mode generic)" == ufw ]]
[[ "$(detect_host_firewall_mode oracle)" == iptables ]]
unset -f ufw
iptables() { [[ "$*" == "-S INPUT" ]] && printf '%s\n' '-P INPUT DROP'; }
[[ "$(detect_host_firewall_mode generic)" == iptables ]]
iptables() { [[ "$*" == "-S INPUT" ]] && printf '%s\n' '-P INPUT ACCEPT' '-A INPUT -j DROP'; }
[[ "$(detect_host_firewall_mode generic)" == iptables ]]
unset -f iptables

curl() {
  if [[ "$*" == *"ipwho.is"* ]]; then
    printf '%s\n' \
      '{' \
      '  "success": true,' \
      '  "country": "Japan",' \
      '  "country_code": "JP",' \
      '  "city": "Tokyo",' \
      '  "connection": {' \
      '    "isp": "Example ISP"' \
      '  }' \
      '}'
  fi
}
detect_geo
[[ "$GEO_CC" == JP && "$GEO_COUNTRY" == Japan && "$GEO_CITY" == Tokyo && "$GEO_ISP" == "Example ISP" ]]
unset -f curl

curl() {
  [[ "$*" == *"ipwho.is"* ]] && printf '%s' '{"success":true,"country":"United States","country_code":"US","city":"Seattle","connection":{"isp":"Example Network"}}'
}
detect_geo
[[ "$GEO_CC" == US && "$GEO_COUNTRY" == "United States" && "$GEO_CITY" == Seattle && "$GEO_ISP" == "Example Network" ]]
unset -f curl

installed_cron=false
installed_iptables=false
apt_update_count=0
apt_install_count=0
apt-get() {
  if [[ "$1" == update ]]; then
    ((apt_update_count += 1))
  fi
  if [[ "$1" == install ]]; then
    ((apt_install_count += 1))
    [[ " $* " == *" cron "* ]]
    [[ " $* " == *" iptables "* ]]
    installed_cron=true
    installed_iptables=true
  fi
}
# shellcheck disable=SC2317,SC2329 # Discovered through command -v inside install_deps.
crontab() { return 0; }
systemctl() { return 0; }
check_required_commands() { return 0; }
install_deps >/dev/null
[[ "$installed_cron" == true ]]
[[ "$installed_iptables" == true ]]
[[ "$apt_update_count" -eq 1 ]]
[[ "$apt_install_count" -eq 1 ]]

dpkg-query() { printf 'install ok installed'; }
apt_update_count=0
apt_install_count=0
install_deps >/dev/null
[[ "$apt_update_count" -eq 0 ]]
[[ "$apt_install_count" -eq 0 ]]
unset -f dpkg-query

cron_contents=""
crontab() {
  if [[ "$1" == -l ]]; then
    [[ -n "$cron_contents" ]] || return 1
    printf '%s\n' "$cron_contents"
    return
  fi
  cron_contents="$(<"$1")"
}
ensure_acme_cron >/dev/null
[[ "$cron_contents" == *"acme.sh\" --cron"* ]]

curl() {
  printf '%s\n' \
    '    "name": "sing-box-1.2.3-linux-amd64.tar.gz",' \
    '    "digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",' \
    '    "browser_download_url": "https://example.invalid/asset"'
}

digest="$(github_asset_sha256 SagerNet/sing-box v1.2.3 sing-box-1.2.3-linux-amd64.tar.gz)"
[[ "$digest" == 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef ]]

# Exercise the firewall helper that write_firewall_helper emits. It is a
# heredoc, so shellcheck and bash -n never see it as part of install.sh.
helper_dir="$(mktemp -d)"
helper="$helper_dir/open-port.sh"
awk 'index($0, "FIREWALL_HELPER\" <<") { capture=1; next } capture && /^HELPER$/ { exit } capture' "$root_dir/install.sh" > "$helper"
[[ -s "$helper" ]] || { echo "could not extract the firewall helper" >&2; exit 1; }
chmod 0755 "$helper"
bash -n "$helper"
if command -v shellcheck >/dev/null 2>&1; then shellcheck "$helper"; fi

export SBM_FIREWALL_MODE_FILE="$helper_dir/mode" SBM_FIREWALL_PORTS_FILE="$helper_dir/ports"
printf 'ufw\n' > "$SBM_FIREWALL_MODE_FILE"
: > "$SBM_FIREWALL_PORTS_FILE"
ufw_log="$helper_dir/ufw.log"
mkdir -p "$helper_dir/bin"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >> "%s"\n' "$ufw_log" > "$helper_dir/bin/ufw"
chmod 0755 "$helper_dir/bin/ufw"
PATH="$helper_dir/bin:$PATH"

"$helper" tcp 8443
"$helper" udp 443
grep -Fxq 'tcp 8443' "$SBM_FIREWALL_PORTS_FILE"
grep -Fxq 'allow 8443/tcp' "$ufw_log"

# Closing releases the rule and forgets it, so a reboot does not restore it.
"$helper" --close tcp 8443
grep -Fxq 'delete allow 8443/tcp' "$ufw_log"
if grep -Fxq 'tcp 8443' "$SBM_FIREWALL_PORTS_FILE"; then
  echo "closed port was still recorded" >&2
  exit 1
fi
grep -Fxq 'udp 443' "$SBM_FIREWALL_PORTS_FILE"

# TCP/80 must survive a close so certificate renewal keeps working.
"$helper" tcp 80
: > "$ufw_log"
"$helper" --close tcp 80
[[ ! -s "$ufw_log" ]]
grep -Fxq 'tcp 80' "$SBM_FIREWALL_PORTS_FILE"

# Uninstall revokes everything that was recorded and empties the ledger.
: > "$ufw_log"
"$helper" --revoke-all
grep -Fxq 'delete allow 443/udp' "$ufw_log"
grep -Fxq 'delete allow 80/tcp' "$ufw_log"
[[ ! -s "$SBM_FIREWALL_PORTS_FILE" ]]

if ("$helper" --close tcp 70000 >/dev/null 2>&1); then
  echo "helper accepted an invalid port" >&2
  exit 1
fi
unset SBM_FIREWALL_MODE_FILE SBM_FIREWALL_PORTS_FILE
rm -rf "$helper_dir"

ss() {
  [[ "$*" == *":2096"* ]] && printf '%s\n' 'LISTEN 0 4096 *:2096 *:*'
  return 0
}
check_ports "$custom_panel_port" >/dev/null
ss() { printf '%s\n' 'LISTEN 0 4096 *:443 *:*'; }
curl() { printf '401'; }
post_install_check node.example.com "$custom_panel_port" >/dev/null
