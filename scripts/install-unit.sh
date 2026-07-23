#!/usr/bin/env bash
set -Eeuo pipefail
trap 'status=$?; printf "install-unit failed at line %d: %s\n" "$LINENO" "$BASH_COMMAND" >&2; exit "$status"' ERR

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# The installer is linted separately; this path is resolved dynamically.
# shellcheck disable=SC1091
source "$root_dir/install.sh"

[[ "$(normalize_tag 1.2.3)" == v1.2.3 ]]
[[ "$(normalize_tag v1.2.3)" == v1.2.3 ]]
[[ "$(cleanup_domain ' HTTPS://Node.Example.COM:2096/path ')" == node.example.com ]]
validate_domain node.example.com
custom_panel_port=24443
validate_panel_port "$custom_panel_port"
[[ "$(country_flag US)" == "🇺🇸" ]]
validate_node_name "🇯🇵Japan-Tokyo"
[[ "$(urlencode_fragment '🇯🇵Japan Tokyo#1')" == "%F0%9F%87%AF%F0%9F%87%B5Japan%20Tokyo%231" ]]
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
apt-get() {
  if [[ "$1" == install ]]; then
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

ss() {
  [[ "$*" == *":2096"* ]] && printf '%s\n' 'LISTEN 0 4096 *:2096 *:*'
  return 0
}
check_ports "$custom_panel_port" >/dev/null
ss() { printf '%s\n' 'LISTEN 0 4096 *:443 *:*'; }
curl() { printf '401'; }
post_install_check node.example.com "$custom_panel_port" >/dev/null
