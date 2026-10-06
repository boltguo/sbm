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

[[ "$(normalize_tag 2.0.2)" == v2.0.2 ]]
[[ "$(normalize_tag v2.0.2)" == v2.0.2 ]]

repo_version="$(tr -d '[:space:]' < "$root_dir/VERSION")"
declare -p SBM_RELEASE_VERSION >/dev/null
release_version="$(normalize_tag "$SBM_RELEASE_VERSION")"
[[ "$release_version" == "v${repo_version}" ]]
compatible_version="$(compatible_sing_box_version v2.0.2)"
[[ "$compatible_version" == v1.13.14 ]]

unset SBM_VERSION SING_BOX_VERSION
selected_version="$(requested_sbm_version)"
[[ "$selected_version" == "v${repo_version}" ]]
SBM_VERSION=2.1.0
selected_version="$(requested_sbm_version)"
[[ "$selected_version" == v2.1.0 ]]
selected_core_version="$(requested_sing_box_version "$selected_version")"
[[ "$selected_core_version" == v1.13.14 ]]
unset SBM_VERSION
SING_BOX_VERSION=1.13.12
selected_core_version="$(requested_sing_box_version v2.0.2)"
[[ "$selected_core_version" == v1.13.12 ]]
unset SING_BOX_VERSION

if (compatible_sing_box_version v9.9.9 >/dev/null 2>&1); then
  echo "unknown SBM release received an untested sing-box version" >&2
  exit 1
fi
if (SBM_VERSION=1.2.3 requested_sbm_version >/dev/null 2>&1); then
  echo "the v2 installer accepted an old SBM release" >&2
  exit 1
fi
if (SBM_VERSION=2.0.2 requested_sbm_version >/dev/null 2>&1); then
  echo 'the current installer accepted a panel without vnstat-check' >&2
  exit 1
fi

(
  github_latest_tag() { printf 'v2.1.0\n'; }
  unset SBM_VERSION
  update_target="$(panel_update_target_version)"
  [[ "$update_target" == v2.1.0 ]]
  SBM_VERSION=2.1.0
  update_target="$(panel_update_target_version)"
  [[ "$update_target" == v2.1.0 ]]
)

fresh_config="$(mktemp /tmp/sbm-v4-config.XXXXXX)"
old_config="$(mktemp /tmp/sbm-v2-config.XXXXXX)"
printf '{"version":4}\n' > "$fresh_config"
printf '{"version":2}\n' > "$old_config"
assert_panel_config_supported v2.0.2 "$fresh_config"
if (assert_panel_config_supported v2.0.2 "$old_config" >/dev/null 2>&1); then
  echo "SBM 2.x accepted an old configuration" >&2
  exit 1
fi
rm -f "$fresh_config" "$old_config"

(
  installed_panel_version() { printf '2.0.2\n'; }
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

firewall_config="$(mktemp /tmp/sbm-firewall-config.XXXXXX)"
printf '%s\n' \
  '{' \
  '  "inbounds": [' \
  '    {"id":"vless","type":"vless-reality","name":"VLESS","enabled":true,"port":8443},' \
  '    {"id":"hy2","type":"hysteria2","name":"HY2","enabled":false,"port":9443}' \
  '  ]' \
  '}' > "$firewall_config"
actual_rules="$(desired_firewall_rules 2096 "$firewall_config")"
expected_rules="$(printf '%s\n' 'tcp 80' 'tcp 2096' 'tcp 8443' | sort -u)"
[[ "$actual_rules" == "$expected_rules" ]]
fresh_rules="$(desired_firewall_rules 2096 /does/not/exist)"
[[ "$fresh_rules" == *"tcp 443"* && "$fresh_rules" == *"udp 443"* ]]
rm -f "$firewall_config"
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
vnstat() { printf "vnStat 2.12 by Teemu Toivola\n"; }
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
    '    "name": "sing-box-1.13.14-linux-amd64.tar.gz",' \
    '    "digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",' \
    '    "browser_download_url": "https://example.invalid/asset"'
}

digest="$(github_asset_sha256 SagerNet/sing-box v1.13.14 sing-box-1.13.14-linux-amd64.tar.gz)"
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
export SBM_FIREWALL_CONFIG_FILE="$helper_dir/config.json" SBM_FIREWALL_PRESERVED_FILE="$helper_dir/preserved"
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
# A source-restricted management port must never receive a global allow rule.
printf '{"panelPort":2096}\n' > "$SBM_FIREWALL_CONFIG_FILE"
cat > "$helper_dir/bin/ufw" <<'MOCK'
#!/usr/bin/env bash
if [[ "$*" == 'show added' ]]; then
  printf 'ufw allow from 192.0.2.10 to any port 2096 proto tcp\n'
else
  printf '%s\n' "$*" >> "$SBM_FIREWALL_TEST_LOG"
fi
MOCK
export SBM_FIREWALL_TEST_LOG="$ufw_log"
: > "$ufw_log"
"$helper" tcp 2096
grep -Fxq 'tcp 2096' "$SBM_FIREWALL_PRESERVED_FILE"
[[ ! -s "$ufw_log" ]]
"$helper" --restore
"$helper" --close tcp 2096
[[ ! -s "$ufw_log" ]]
# Protection persists even if host rules have not loaded yet after a reboot.
cat > "$helper_dir/bin/ufw" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SBM_FIREWALL_TEST_LOG"
MOCK
"$helper" tcp 2096
[[ ! -s "$ufw_log" ]]
printf 'iptables\n' > "$SBM_FIREWALL_MODE_FILE"
printf '{"domain":"node.example.com","panelPort":24443}\n' > "$SBM_FIREWALL_CONFIG_FILE"
cat > "$helper_dir/bin/iptables" <<'MOCK'
#!/usr/bin/env bash
if [[ "$*" == '-w -S INPUT' ]]; then
  printf '%s\n' '-A INPUT -s 192.0.2.10/32 -p tcp --dport 24443 -j ACCEPT'
else
  printf '%s\n' "$*" >> "$SBM_FIREWALL_TEST_LOG"
fi
MOCK
chmod 0755 "$helper_dir/bin/iptables"
"$helper" tcp 24443
grep -Fxq 'tcp 24443' "$SBM_FIREWALL_PRESERVED_FILE"
[[ ! -s "$ufw_log" ]]
printf 'firewalld\n' > "$SBM_FIREWALL_MODE_FILE"
printf '{"panelPort":34443}\n' > "$SBM_FIREWALL_CONFIG_FILE"
cat > "$helper_dir/bin/firewall-cmd" <<'MOCK'
#!/usr/bin/env bash
if [[ "$*" == '--list-all-zones' ]]; then
  printf '%s\n' 'rule family="ipv4" source address="192.0.2.10/32" port port="34443" protocol="tcp" accept'
else
  printf '%s\n' "$*" >> "$SBM_FIREWALL_TEST_LOG"
fi
MOCK
chmod 0755 "$helper_dir/bin/firewall-cmd"
"$helper" tcp 34443
grep -Fxq 'tcp 34443' "$SBM_FIREWALL_PRESERVED_FILE"
[[ ! -s "$ufw_log" ]]
unset SBM_FIREWALL_MODE_FILE SBM_FIREWALL_PORTS_FILE SBM_FIREWALL_CONFIG_FILE SBM_FIREWALL_PRESERVED_FILE SBM_FIREWALL_TEST_LOG
rm -rf "$helper_dir"

# Reconcile actual desired ports against the pre-restore ledger as well as the
# restored one. This covers a port change with an already-active oneshot.
(
  firewall_reconcile_dir="$(mktemp -d /tmp/sbm-firewall-reconcile.XXXXXX)"
  trap 'command rm -rf "$firewall_reconcile_dir"' EXIT
  reconcile_mode="$firewall_reconcile_dir/mode"
  reconcile_ports="$firewall_reconcile_dir/ports"
  reconcile_helper="$firewall_reconcile_dir/helper"
  reconcile_config="$firewall_reconcile_dir/config"
  reconcile_log="$firewall_reconcile_dir/log"
  printf '{"inbounds":[{"type":"vless-reality","enabled":true,"port":8443}]}\n' > "$reconcile_config"
  printf 'tcp 443\n' > "$reconcile_ports"
  cat > "$reconcile_helper" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SBM_RECONCILE_LOG"
MOCK
  chmod 0755 "$reconcile_helper"
  export SBM_RECONCILE_LOG="$reconcile_log"
  reconcile_function="$(declare -f open_firewall)"
  reconcile_function="${reconcile_function//FIREWALL_MODE/reconcile_mode}"
  reconcile_function="${reconcile_function//FIREWALL_PORTS/reconcile_ports}"
  reconcile_function="${reconcile_function//FIREWALL_HELPER/reconcile_helper}"
  eval "${reconcile_function//CONFIG_FILE/reconcile_config}"
  detect_host_firewall_mode() { printf 'none\n'; }
  open_firewall generic 443 'tcp 60443' >/dev/null
  [[ "$(cat "$reconcile_mode")" == none ]]
  grep -Fxq 'tcp 443' "$reconcile_log"
  grep -Fxq 'tcp 8443' "$reconcile_log"
  grep -Fxq -- '--close tcp 60443' "$reconcile_log"
  if grep -Fxq -- '--close tcp 443' "$reconcile_log"; then echo 'restored port closed' >&2; exit 1; fi
  printf '#!/usr/bin/env bash\nexit 1\n' > "$reconcile_helper"
  if open_firewall generic 443 'tcp 60443' >/dev/null; then echo 'failed firewall helper reported success' >&2; exit 1; fi
)

ss() {
  [[ "$*" == *":2096"* ]] && printf '%s\n' 'LISTEN 0 4096 *:2096 *:*'
  return 0
}
check_ports "$custom_panel_port" >/dev/null
ss() { printf '%s\n' 'LISTEN 0 4096 *:443 *:*'; }
curl() { printf '401'; }
post_install_check node.example.com "$custom_panel_port" >/dev/null

# SQLite backups must pause only the panel and restore it even if tar fails.
(
  backup_trace="$(mktemp /tmp/sbm-backup-test.XXXXXX)"
  trap 'command rm -f "$backup_trace"' EXIT
  backup_tar_status=0
  backup_stop_status=0
  systemctl() {
    printf 'systemctl %s\n' "$*" >> "$backup_trace"
    if [[ "$1" == stop ]]; then return "$backup_stop_status"; fi
    return 0
  }
  tar() { printf 'tar\n' >> "$backup_trace"; return "$backup_tar_status"; }
  chmod() { return 0; }
  rm() { return 0; }
  backup_config >/dev/null
  [[ "$(cat "$backup_trace")" == $'systemctl is-active --quiet sbm-panel.service\nsystemctl stop sbm-panel.service\ntar\nsystemctl start sbm-panel.service' ]]
  : > "$backup_trace"
  backup_tar_status=1
  if backup_config >/dev/null; then echo 'Failed archive reported success' >&2; exit 1; fi
  grep -Fxq 'systemctl start sbm-panel.service' "$backup_trace"
  : > "$backup_trace"
  backup_stop_status=1
  if backup_config >/dev/null; then echo 'Backup proceeded after failed stop' >&2; exit 1; fi
  if grep -Fxq tar "$backup_trace"; then echo 'SQLite archived while panel was running' >&2; exit 1; fi
)

# Restore exercises private fixture files and mocked services, including legacy
# archives that must retire the authoritative checkpoint before restarting.
(
  restore_test_state_dir="$(mktemp -d /tmp/sbm-restore-test.XXXXXX)"
  trap 'command rm -rf "$restore_test_state_dir"' EXIT
  restore_trace="$restore_test_state_dir/trace"
  archive="$restore_test_state_dir/backup.tar.gz"
  touch "$archive"
  restore_stop_status=0
  restore_list_status=0
  restore_extract_status=0
  restore_has_database=0
  restore_move_status=0
  # Redirect just this function's DB path, leaving installer constants intact.
  restore_function="$(declare -f restore_config)"
  eval "${restore_function//STATE_DIR/restore_test_state_dir}"
  systemctl() {
    printf 'systemctl %s\n' "$*" >> "$restore_trace"
    if [[ "$1" == stop ]]; then return "$restore_stop_status"; fi
    return 0
  }
  tar() {
    printf 'tar %s\n' "$1" >> "$restore_trace"
    case "$1" in
      -tzf)
        (( restore_list_status == 0 )) || return "$restore_list_status"
        printf 'etc/sbm/config.json\nvar/lib/sbm/state.json\n'
        if (( restore_has_database )); then printf 'var/lib/sbm/traffic.db\n'; fi
        ;;
      -xzf) return "$restore_extract_status" ;;
      *) return 1 ;;
    esac
  }
  chmod() { return 0; }
  mv() {
    printf 'mv\n' >> "$restore_trace"
    (( restore_move_status == 0 )) || return "$restore_move_status"
    command mv "$@"
  }
  quota_exceeded() { return 1; }
  json_number() { printf '443\n'; }
  write_firewall_helper() { printf 'refresh firewall helper\n' >> "$restore_trace"; }
  open_firewall() { printf 'reconcile firewall %s\n' "$2" >> "$restore_trace"; }
  run_restore() { restore_config <<< "$archive"$'\ny' >/dev/null; }

  printf 'current checkpoint' > "$restore_test_state_dir/traffic.db"
  run_restore
  [[ ! -f "$restore_test_state_dir/traffic.db" ]]
  [[ "$(cat "$restore_test_state_dir"/traffic.db.before-restore-*)" == 'current checkpoint' ]]
  grep -Fxq 'reconcile firewall 443' "$restore_trace"
  grep -Fxq 'systemctl restart sbm-firewall.service' "$restore_trace"
  grep -Fxq 'systemctl start sbm-panel.service' "$restore_trace"

  : > "$restore_trace"
  restore_has_database=1
  printf 'restored checkpoint' > "$restore_test_state_dir/traffic.db"
  run_restore
  [[ "$(cat "$restore_test_state_dir/traffic.db")" == 'restored checkpoint' ]]
  if grep -Fxq mv "$restore_trace"; then echo 'New backup lost its restored database' >&2; exit 1; fi

  : > "$restore_trace"
  restore_stop_status=1
  if run_restore; then echo 'Restore proceeded after failed service stop' >&2; exit 1; fi
  if grep -Fxq 'tar -xzf' "$restore_trace"; then echo 'Live database was overwritten' >&2; exit 1; fi
  restore_stop_status=0

  : > "$restore_trace"
  restore_list_status=1
  if run_restore 2>/dev/null; then echo 'Unreadable archive reported success' >&2; exit 1; fi
  if grep -q '^systemctl' "$restore_trace"; then echo 'Unreadable archive stopped services' >&2; exit 1; fi
  restore_list_status=0

  : > "$restore_trace"
  restore_extract_status=1
  if run_restore; then echo 'Failed extraction reported success' >&2; exit 1; fi
  if grep -q '^systemctl start' "$restore_trace"; then echo 'Partial restore restarted services' >&2; exit 1; fi
  restore_extract_status=0

  : > "$restore_trace"
  restore_has_database=0
  restore_move_status=1
  if run_restore; then echo 'Failed checkpoint retirement reported success' >&2; exit 1; fi
  if grep -q '^systemctl start' "$restore_trace"; then echo 'Stale checkpoint restarted services' >&2; exit 1; fi
)

# Installed CLI versions must satisfy the JSON timestamp contract.
for vnstat_version in 2.10 2.12 2.12.1 3.0; do
  vnstat() { printf 'vnStat %s by Teemu Toivola\n' "$vnstat_version"; }
  check_vnstat_version
done
for vnstat_version in 1.18 2.9 unknown; do
  if check_vnstat_version; then echo "unsupported vnStat accepted: $vnstat_version" >&2; exit 1; fi
done

# Retry source readiness before using a candidate binary. No host services run.
(
  preflight_calls=0
  # shellcheck disable=SC2317,SC2329 # Invoked through wait_vnstat_source's binary argument.
  candidate_check() {
    [[ "$*" == "vnstat-check --config $CONFIG_FILE" ]]
    preflight_calls=$((preflight_calls + 1))
    (( preflight_calls >= 3 ))
  }
  sleep() { :; }
  wait_vnstat_source candidate_check
  [[ "$preflight_calls" == 3 ]]
)

# shellcheck disable=SC2329 # Mocks are invoked by the sourced updater function.
# The downloaded manager executes in a new shell, rather than using old
# already-loaded repair functions from the interactive menu process.
(
  update_fixture="$(mktemp -d /tmp/sbm-manager-unit.XXXXXX)"
  trap 'rm -rf "$update_fixture"' EXIT
  update_manager="$update_fixture/sbm"
  update_function="$(declare -f perform_panel_update)"
  eval "${update_function//SBM_CMD/update_manager}"
  export SBM_UPDATE_TRACE="$update_fixture/trace"
  panel_update_target_version() { printf 'v2.1.0\n'; }
  assert_panel_config_supported() { :; }
  install_deps() { :; }
  configure_vnstat() { :; }
  write_update_status() { :; }
  # shellcheck disable=SC2317,SC2329 # Regression guard against calling the loaded manager.
  repair_runtime() { echo 'old manager executed' >&2; exit 1; }
  install_panel() {
    cat > "$update_manager" <<'MANAGER'
#!/usr/bin/env bash
[[ "$1" == --finish-panel-update ]] || exit 2
printf 'new manager executed\n' > "$SBM_UPDATE_TRACE"
MANAGER
  }
  perform_panel_update
  [[ "$(cat "$SBM_UPDATE_TRACE")" == 'new manager executed' ]]
)
