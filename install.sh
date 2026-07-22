#!/usr/bin/env bash
# SBM — 极简单人 sing-box 面板安装与管理脚本
set -Eeuo pipefail

readonly REPO="boltguo/sbm"
readonly SBM_BIN="/usr/local/bin/sbm-panel"
readonly SING_BOX_BIN="/usr/local/bin/sing-box"
readonly SBM_CMD="/usr/local/bin/sbm"
readonly CONFIG_DIR="/etc/sbm"
readonly CONFIG_FILE="/etc/sbm/config.json"
readonly STATE_DIR="/var/lib/sbm"
readonly STATE_FILE="/var/lib/sbm/state.json"
readonly CERT_DIR="/etc/sbm/cert"
readonly CORE_DIR="/etc/sing-box"
readonly CORE_CONFIG="/etc/sing-box/config.json"
readonly LEGACY_ENV="/etc/sing-box/sbm.env"
readonly ACME_BIN="/root/.acme.sh/acme.sh"
readonly CERT_RELOAD="/usr/local/lib/sbm/cert-reload.sh"
readonly SELF_URL="https://raw.githubusercontent.com/${REPO}/main/install.sh"

RED=$'\e[31m'; GREEN=$'\e[32m'; YELLOW=$'\e[33m'; CYAN=$'\e[36m'; RESET=$'\e[0m'
info() { printf '%s[*]%s %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '%s[!]%s %s\n' "$YELLOW" "$RESET" "$*"; }
die() { printf '%s[x]%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

need_root() { [[ $(id -u) -eq 0 ]] || die "请先执行 sudo -i，再运行安装命令。"; }
check_os() {
  [[ -r /etc/os-release ]] || die "无法识别操作系统。"
  # shellcheck source=/dev/null
  . /etc/os-release
  case "${ID:-}" in debian|ubuntu) ;; *) die "仅支持 Debian 和 Ubuntu。" ;; esac
  command -v apt-get >/dev/null || die "未找到 apt-get。"
}
arch_tag() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64\n' ;;
    aarch64|arm64) printf 'arm64\n' ;;
    *) die "不支持的架构：$(uname -m)" ;;
  esac
}
cleanup_domain() {
  local value="$1"
  value="${value//[[:space:]]/}"
  value="${value#http://}"; value="${value#https://}"; value="${value%%/*}"; value="${value%%:*}"
  printf '%s\n' "${value,,}"
}
validate_domain() {
  local domain="$1"
  [[ ${#domain} -le 253 && "$domain" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || die "域名格式无效。"
}
public_ipv4() { curl -4fsS --max-time 8 https://api.ipify.org 2>/dev/null || true; }
check_dns() {
  local domain="$1" public resolved
  public="$(public_ipv4)"
  resolved="$(getent ahostsv4 "$domain" 2>/dev/null | awk '{print $1}' | sort -u || true)"
  [[ -n "$resolved" ]] || die "域名 ${domain} 没有可用的 A 记录。"
  if [[ -n "$public" ]] && ! grep -Fxq "$public" <<<"$resolved"; then
    die "域名解析未指向本机（本机公网 IPv4：${public}）。Cloudflare 必须使用 DNS only / 灰云。"
  fi
  info "域名解析检查通过。"
}
port_free() {
  local port="$1" network="$2"
  if [[ "$network" == tcp ]]; then
    if ss -H -ltn "sport = :${port}" 2>/dev/null | grep -q .; then return 1; fi
  else
    if ss -H -lun "sport = :${port}" 2>/dev/null | grep -q .; then return 1; fi
  fi
  return 0
}
validate_panel_port() {
  local port="$1"
  [[ "$port" =~ ^[0-9]+$ ]] && (( port >= 1 && port <= 65535 )) || die "面板端口必须是 1 到 65535 的整数。"
  case "$port" in
    80|443|9090) die "TCP/${port} 已由证书、默认入站或本机 Clash API 使用，请换一个面板端口。" ;;
  esac
}
check_ports() {
  local panel_port="$1"
  port_free 80 tcp || die "TCP/80 已被占用，HTTP-01 申请证书需要此端口。"
  port_free 443 tcp || die "TCP/443 已被占用。"
  port_free 443 udp || die "UDP/443 已被占用。"
  port_free "$panel_port" tcp || die "TCP/${panel_port} 已被占用。"
}
install_deps() {
  info "安装最少运行依赖…"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y >/dev/null
  apt-get install -y ca-certificates curl openssl socat tar gzip iproute2 >/dev/null
}
enable_bbr() {
  install -d -m 0755 /etc/sysctl.d
  printf '%s\n' 'net.core.default_qdisc=fq' 'net.ipv4.tcp_congestion_control=bbr' > /etc/sysctl.d/99-sbm-bbr.conf
  sysctl --system >/dev/null 2>&1 || true
}
github_latest_tag() {
  local repository="$1"
  curl -fsSL "https://api.github.com/repos/${repository}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1
}
install_sing_box() {
  local arch tag release_version url temp_dir
  arch="$(arch_tag)"; tag="$(github_latest_tag SagerNet/sing-box)"; [[ -n "$tag" ]] || die "无法查询 sing-box 最新版本。"
  release_version="${tag#v}"
  url="https://github.com/SagerNet/sing-box/releases/download/${tag}/sing-box-${release_version}-linux-${arch}.tar.gz"
  temp_dir="$(mktemp -d /tmp/sbm-sing-box.XXXXXX)"
  curl -fsSL "$url" -o "${temp_dir}/sing-box.tar.gz" || { rm -rf "$temp_dir"; die "下载 sing-box 失败。"; }
  tar -xzf "${temp_dir}/sing-box.tar.gz" -C "$temp_dir"
  install -m 0755 "${temp_dir}/sing-box-${release_version}-linux-${arch}/sing-box" "$SING_BOX_BIN"
  rm -rf "$temp_dir"
  info "已安装 $($SING_BOX_BIN version | head -n1)。"
}
install_panel() {
  local arch tag release_version asset base_url temp_dir expected actual
  arch="$(arch_tag)"; tag="${SBM_PANEL_VERSION:-$(github_latest_tag "$REPO")}"; [[ -n "$tag" ]] || die "无法查询 sbm-panel 最新版本。"
  release_version="${tag#v}"; asset="sbm-panel_${release_version}_linux_${arch}.tar.gz"
  base_url="https://github.com/${REPO}/releases/download/${tag}"
  temp_dir="$(mktemp -d /tmp/sbm-panel.XXXXXX)"
  curl -fsSL "${base_url}/${asset}" -o "${temp_dir}/${asset}" || { rm -rf "$temp_dir"; die "下载 sbm-panel 失败；请确认仓库已有对应架构的 Release。"; }
  curl -fsSL "${base_url}/checksums.txt" -o "${temp_dir}/checksums.txt" || { rm -rf "$temp_dir"; die "下载校验文件失败。"; }
  expected="$(awk -v name="$asset" '$2 == name {print $1}' "${temp_dir}/checksums.txt")"
  actual="$(sha256sum "${temp_dir}/${asset}" | awk '{print $1}')"
  [[ -n "$expected" && "$expected" == "$actual" ]] || { rm -rf "$temp_dir"; die "sbm-panel 下载校验失败。"; }
  tar -xzf "${temp_dir}/${asset}" -C "$temp_dir"
  install -m 0755 "${temp_dir}/sbm-panel" "$SBM_BIN"
  if [[ -f "${temp_dir}/sbm" ]]; then
    bash -n "${temp_dir}/sbm" || { rm -rf "$temp_dir"; die "Release 中的 sbm 管理脚本校验失败。"; }
    install -m 0755 "${temp_dir}/sbm" "$SBM_CMD"
  fi
  rm -rf "$temp_dir"
  info "已安装 sbm-panel $($SBM_BIN version)。"
}
issue_certificate() {
  local domain="$1"
  if [[ ! -x "$ACME_BIN" ]]; then
    info "安装 acme.sh…"
    curl -fsSL https://get.acme.sh | sh >/dev/null
  fi
  "$ACME_BIN" --set-default-ca --server letsencrypt >/dev/null
  "$ACME_BIN" --register-account >/dev/null 2>&1 || true
  info "通过 Let's Encrypt HTTP-01 申请证书…"
  "$ACME_BIN" --issue --standalone -d "$domain" --keylength 2048
  install -d -m 0700 "$CERT_DIR" /usr/local/lib/sbm
  write_cert_reload_hook
  "$ACME_BIN" --install-cert -d "$domain" \
    --key-file "${CERT_DIR}/key.pem" \
    --fullchain-file "${CERT_DIR}/fullchain.pem" \
    --reloadcmd "$CERT_RELOAD"
  chmod 0600 "${CERT_DIR}/key.pem" "${CERT_DIR}/fullchain.pem"
}
write_cert_reload_hook() {
  install -d -m 0755 /usr/local/lib/sbm
  cat > "$CERT_RELOAD" <<'HOOK'
#!/usr/bin/env bash
set -euo pipefail
if systemctl is-enabled --quiet sbm-panel.service 2>/dev/null; then
  systemctl restart sbm-panel.service
fi
if systemctl is-enabled --quiet sing-box.service 2>/dev/null && ! grep -Eq '"quotaExceeded"[[:space:]]*:[[:space:]]*true' /var/lib/sbm/state.json; then
  systemctl restart sing-box.service
fi
HOOK
  chmod 0755 "$CERT_RELOAD"
}
write_services() {
  install -d -m 0700 "$CONFIG_DIR" "$STATE_DIR" "$CORE_DIR"
  cat > /etc/systemd/system/sing-box.service <<EOF
[Unit]
Description=sing-box proxy core
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStartPre=${SING_BOX_BIN} check -c ${CORE_CONFIG}
ExecStart=${SING_BOX_BIN} run -c ${CORE_CONFIG} -D /var/lib/sing-box
Restart=on-failure
RestartSec=3s
LimitNOFILE=1048576
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
  cat > /etc/systemd/system/sbm-panel.service <<EOF
[Unit]
Description=SBM sing-box management panel
After=network-online.target sing-box.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=${SBM_BIN} serve
Restart=on-failure
RestartSec=3s
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictRealtime=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}
open_firewall() {
  local panel_port="$1"
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
    info "放行 UFW 默认端口…"
    ufw allow 80/tcp >/dev/null
    ufw allow 443/tcp >/dev/null
    ufw allow 443/udp >/dev/null
    ufw allow "${panel_port}/tcp" >/dev/null
  fi
}
install_sbm_command() {
  if [[ -f "${BASH_SOURCE[0]}" && "${BASH_SOURCE[0]}" != /dev/fd/* ]]; then
    install -m 0755 "${BASH_SOURCE[0]}" "$SBM_CMD"
  else
    curl -fsSL "$SELF_URL" -o "$SBM_CMD"
    chmod 0755 "$SBM_CMD"
  fi
}
protect_legacy_install() {
  [[ ! -f "$LEGACY_ENV" ]] && return
  local backup
  backup="/root/sbm-legacy-$(date +%Y%m%d-%H%M%S).tar.gz"
  tar -czf "$backup" -C / etc/sing-box 2>/dev/null || true
  chmod 0600 "$backup" 2>/dev/null || true
  die "检测到旧版 ${LEGACY_ENV}，已备份到 ${backup}。请先用旧脚本卸载或手动迁移。"
}
do_install() {
  need_root; check_os; protect_legacy_install
  [[ ! -f "$CONFIG_FILE" ]] || { menu; return; }
  printf '%s===== SBM 极简 sing-box 面板安装 =====%s\n' "$CYAN" "$RESET"
  local domain panel_port admin_password password_file
  read -r -p "Domain: " domain
  domain="$(cleanup_domain "$domain")"; validate_domain "$domain"
  read -r -p "面板端口 [2096]: " panel_port
  panel_port="${panel_port:-2096}"; validate_panel_port "$panel_port"
  install_deps; check_dns "$domain"; check_ports "$panel_port"
  enable_bbr; install_sing_box; install_panel; issue_certificate "$domain"; write_services
  admin_password="$(openssl rand -base64 32 | tr -d '/+=' | cut -c1-24)"
  password_file="$(mktemp /tmp/sbm-password.XXXXXX)"; chmod 0600 "$password_file"; printf '%s' "$admin_password" > "$password_file"
  if ! "$SBM_BIN" init --domain "$domain" --panel-port "$panel_port" --admin-password-file "$password_file"; then rm -f "$password_file"; die "初始化面板配置失败。"; fi
  rm -f "$password_file"
  "$SBM_BIN" config apply --no-start
  open_firewall "$panel_port"; install_sbm_command
  systemctl enable --now sing-box.service >/dev/null
  systemctl enable --now sbm-panel.service >/dev/null
  systemctl is-active --quiet sing-box.service || die "sing-box 启动失败，请执行 journalctl -u sing-box -e。"
  systemctl is-active --quiet sbm-panel.service || die "面板启动失败，请执行 journalctl -u sbm-panel -e。"
  local token; token="$(json_string subscriptionToken "$CONFIG_FILE")"
  printf '\n%s安装完成%s\n' "$GREEN" "$RESET"
  printf '面板地址：%shttps://%s:%s/%s\n' "$CYAN" "$domain" "$panel_port" "$RESET"
  printf '用户名：  admin\n密码：    %s%s%s\n' "$YELLOW" "$admin_password" "$RESET"
  printf '总订阅：  %shttps://%s:%s/sub/%s%s\n' "$CYAN" "$domain" "$panel_port" "$token" "$RESET"
  warn "密码只显示这一次；请立即保存。云厂商安全组仍需手动放行 TCP/80、TCP/443、UDP/443、TCP/${panel_port}。"
  printf '以后可运行 sudo sbm：选择 1 重新查看地址，选择 4 重置管理员密码。\n'
}
json_string() {
  local key="$1" file="$2"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$file" | head -n1
}
json_number() {
  local key="$1" file="$2"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p" "$file" | head -n1
}
quota_exceeded() { grep -Eq '"quotaExceeded"[[:space:]]*:[[:space:]]*true' "$STATE_FILE" 2>/dev/null; }
show_status() {
  local domain panel_port token
  domain="$(json_string domain "$CONFIG_FILE")"; panel_port="$(json_number panelPort "$CONFIG_FILE")"; token="$(json_string subscriptionToken "$CONFIG_FILE")"
  printf '面板：  https://%s:%s/\n用户名：admin\n订阅：  https://%s:%s/sub/%s\n\n' "$domain" "$panel_port" "$domain" "$panel_port" "$token"
  systemctl --no-pager --full status sbm-panel.service sing-box.service | sed -n '1,24p' || true
}
restart_core() { quota_exceeded && { warn "流量已超限，禁止直接重启 sing-box。请在面板中重置流量或提高限额。"; return; }; systemctl restart sing-box.service; info "sing-box 已重启。"; }
reset_admin_password() {
  local password
  password="$("$SBM_BIN" admin reset)" || { warn "重置管理员密码失败。"; return 1; }
  if systemctl restart sbm-panel.service && systemctl is-active --quiet sbm-panel.service; then
    printf '新密码：%s%s%s\n' "$YELLOW" "$password" "$RESET"
    warn "旧密码与已有登录会话均已失效；请立即保存新密码。"
    return
  fi
  printf '新密码已写入：%s%s%s\n' "$YELLOW" "$password" "$RESET"
  warn "面板重启失败，请保存上述密码并执行 journalctl -u sbm-panel -e。"
  return 1
}
update_panel() { install_panel; systemctl restart sbm-panel.service; info "面板已更新。"; }
update_core() { install_sing_box; "$SBM_BIN" config apply --no-start; quota_exceeded || systemctl restart sing-box.service; info "sing-box 已更新。"; }
backup_config() {
  local target
  target="/root/sbm-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
  tar -czf "$target" -C / etc/sbm var/lib/sbm etc/sing-box/config.json etc/systemd/system/sbm-panel.service etc/systemd/system/sing-box.service
  chmod 0600 "$target"; info "备份已保存：${target}"
}
restore_config() {
  local archive entry
  read -r -p "备份文件绝对路径: " archive
  [[ "$archive" == /* && -f "$archive" ]] || { warn "备份文件不存在。"; return; }
  while IFS= read -r entry; do
    [[ "$entry" != /* && "$entry" != *".."* ]] || { warn "备份包含不安全路径，拒绝恢复。"; return; }
    case "$entry" in etc/sbm/*|var/lib/sbm/*|etc/sing-box/config.json|etc/systemd/system/sbm-panel.service|etc/systemd/system/sing-box.service) ;; *) warn "备份包含未知路径：${entry}"; return ;; esac
  done < <(tar -tzf "$archive")
  read -r -p "恢复会覆盖当前配置，继续？[y/N]: " answer; [[ "${answer,,}" == y ]] || return
  systemctl stop sbm-panel.service sing-box.service || true
  tar -xzf "$archive" -C /
  chmod 0600 "$CONFIG_FILE" "$STATE_FILE" "$CORE_CONFIG"
  systemctl daemon-reload; systemctl start sbm-panel.service; quota_exceeded || systemctl start sing-box.service
  info "备份已恢复。"
}
uninstall() {
  local answer domain
  read -r -p "这会删除 SBM、sing-box、配置、状态和证书。继续？[y/N]: " answer; [[ "${answer,,}" == y ]] || return
  domain="$(json_string domain "$CONFIG_FILE")"
  systemctl disable --now sbm-panel.service sing-box.service >/dev/null 2>&1 || true
  [[ -x "$ACME_BIN" && -n "$domain" ]] && "$ACME_BIN" --remove -d "$domain" >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/sbm-panel.service /etc/systemd/system/sing-box.service "$SBM_BIN" "$SING_BOX_BIN" "$SBM_CMD" "$CERT_RELOAD"
  rm -rf /etc/sbm /var/lib/sbm /etc/sing-box /var/lib/sing-box
  systemctl daemon-reload
  info "已删除面板、sing-box、配置、状态与证书；/root 下的手动备份和 acme.sh 本体仍保留。"
}
menu() {
  need_root
  while true; do
    printf '\n%s========= SBM 管理 =========%s\n' "$CYAN" "$RESET"
    printf '%s\n' '1. 查看面板地址和运行状态' '2. 重启面板' '3. 重启 sing-box' '4. 重置管理员密码' '5. 查看日志' '6. 更新面板' '7. 更新 sing-box' '8. 备份配置' '9. 恢复配置' '10. 卸载' '0. 退出'
    read -r -p "选择: " choice
    case "$choice" in
      1) show_status ;;
      2) systemctl restart sbm-panel.service; info "面板已重启。" ;;
      3) restart_core ;;
      4) reset_admin_password ;;
      5) journalctl -u sbm-panel.service -u sing-box.service -n 80 --no-pager ;;
      6) update_panel ;;
      7) update_core ;;
      8) backup_config ;;
      9) restore_config ;;
      10) uninstall; return ;;
      0) return ;;
      *) warn "无效选择。" ;;
    esac
  done
}
main() { if [[ -f "$CONFIG_FILE" ]]; then menu; else do_install; fi; }
main "$@"
