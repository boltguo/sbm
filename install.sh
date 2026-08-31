#!/usr/bin/env bash
# SBM — 极简单人 sing-box 面板安装与管理脚本
set -Eeuo pipefail

readonly REPO="boltguo/sbm"
readonly SBM_RELEASE_VERSION="2.0.0"
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
readonly ACME_BIN="/root/.acme.sh/acme.sh"
readonly CERT_RELOAD="/usr/local/lib/sbm/cert-reload.sh"
readonly FIREWALL_HELPER="/usr/local/lib/sbm/open-port.sh"
readonly FIREWALL_MODE="/etc/sbm/firewall-mode"
readonly FIREWALL_PORTS="/etc/sbm/firewall-ports"
readonly FIREWALL_SERVICE="/etc/systemd/system/sbm-firewall.service"
readonly CORE_GUARD="/usr/local/lib/sbm/core-start-allowed.sh"
readonly SELF_URL="https://raw.githubusercontent.com/${REPO}/main/install.sh"
readonly -a RUNTIME_PACKAGES=(
  bash ca-certificates coreutils cron curl gawk grep gzip iproute2 iptables
  libc-bin openssl procps sed socat tar
)

RED=$'\e[31m'; GREEN=$'\e[32m'; YELLOW=$'\e[33m'; CYAN=$'\e[36m'; RESET=$'\e[0m'
info() { printf '%s[*]%s %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '%s[!]%s %s\n' "$YELLOW" "$RESET" "$*"; }
die() { printf '%s[x]%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

need_root() { [[ $(id -u) -eq 0 ]] || die "需要 root 权限：安装请在命令前加 sudo，管理请执行 sudo sbm。"; }
check_os() {
  [[ -r /etc/os-release ]] || die "无法识别操作系统。"
  # shellcheck source=/dev/null
  . /etc/os-release
  case "${ID:-}" in debian|ubuntu) ;; *) die "仅支持 Debian 和 Ubuntu。" ;; esac
  command -v apt-get >/dev/null || die "未找到 apt-get。"
}
check_systemd() {
  command -v systemctl >/dev/null || die "未找到 systemctl；SBM 需要使用 systemd 的 Debian/Ubuntu。"
  [[ -d /run/systemd/system ]] || die "当前环境没有运行 systemd；不支持未启用 systemd 的容器或 WSL。"
  systemctl show-environment >/dev/null 2>&1 || die "无法连接 systemd，请确认当前系统以 systemd 作为 PID 1。"
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
  value="$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')"
  value="${value//[[:space:]]/}"
  value="${value#http://}"; value="${value#https://}"; value="${value%%/*}"; value="${value%%:*}"
  printf '%s\n' "$value"
}
validate_domain() {
  local domain="$1"
  [[ ${#domain} -le 253 && "$domain" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || die "域名格式无效。"
}
public_ipv4() { curl -4fsS --max-time 8 https://api.ipify.org 2>/dev/null || true; }
cloud_provider_from_identity() {
  local identity
  identity="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
  case "$identity" in
    *oracle*|*oci*) printf 'oracle\n' ;;
    *amazon*|*ec2*|*aws*) printf 'aws\n' ;;
    *google*|*compute-engine*|*gce*) printf 'gcp\n' ;;
    *microsoft*|*azure*) printf 'azure\n' ;;
    *alibaba*|*aliyun*) printf 'alibaba\n' ;;
    *tencent*|*qcloud*) printf 'tencent\n' ;;
    *digitalocean*) printf 'digitalocean\n' ;;
    *hetzner*) printf 'hetzner\n' ;;
    *vultr*) printf 'vultr\n' ;;
    *linode*|*akamai*) printf 'linode\n' ;;
    *) printf 'generic\n' ;;
  esac
}
detect_cloud_provider() {
  local cloud_id="" identity=""
  if command -v cloud-id >/dev/null 2>&1; then
    cloud_id="$(cloud-id 2>/dev/null || true)"
  fi
  for identity_file in /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name /sys/class/dmi/id/chassis_asset_tag; do
    [[ -r "$identity_file" ]] && identity+=" $(<"$identity_file")"
  done
  cloud_provider_from_identity "$cloud_id $identity"
}
cloud_provider_name() {
  case "$1" in
    oracle) printf 'Oracle Cloud (OCI)' ;;
    aws) printf 'AWS EC2 / Lightsail' ;;
    gcp) printf 'Google Cloud (GCP)' ;;
    azure) printf 'Microsoft Azure' ;;
    alibaba) printf 'Alibaba Cloud ECS / 阿里云' ;;
    tencent) printf 'Tencent Cloud CVM / 腾讯云' ;;
    digitalocean) printf 'DigitalOcean' ;;
    hetzner) printf 'Hetzner Cloud' ;;
    vultr) printf 'Vultr' ;;
    linode) printf 'Akamai/Linode' ;;
    *) printf '通用 KVM（含 DMIT 等）' ;;
  esac
}
show_cloud_firewall_guide() {
  local provider="$1" panel_port="$2" required_rules
  required_rules="$(desired_firewall_rules "$panel_port" "$CONFIG_FILE" | awk '{ item=toupper($1) "/" $2; if (result == "") result=item; else result=result "、" item } END { print result }')"
  warn "云平台外层防火墙无法从虚拟机内自动修改，请确认入站：${required_rules}。面板端口也供客户端更新订阅，来源限制需覆盖实际客户端。"
  warn "请保持云防火墙允许全部出站，并让域名 A 记录直连本机；Cloudflare 必须使用 DNS only / 灰云。"
  case "$provider" in
    oracle) warn "OCI：VNIC NSG 与子网 Security List 的允许规则取并集，任选实际生效的一层添加即可；系统防火墙仍需放行。脚本只增量处理 iptables，不会清空原规则。" ;;
    aws) warn "AWS：检查网卡实际关联的 EC2 Security Group；仅当使用自定义 Network ACL 时才需额外允许双向返回流量。Lightsail 的 IPv4/IPv6 防火墙需分别配置。" ;;
    gcp) warn "GCP：VPC 防火墙规则必须命中本机的目标标签或服务账号，并且不能被更高优先级的拒绝规则覆盖。" ;;
    azure) warn "Azure：NIC 和子网关联的 NSG 都必须允许这些端口；优先级数字越小越先执行。" ;;
    alibaba) warn "阿里云：分别放行 TCP 与 UDP，不要只选预置 HTTPS 而漏掉 UDP/443；企业安全组还要确认出方向允许。" ;;
    tencent) warn "腾讯云：分别添加 TCP 与 UDP；多个安全组按优先级执行，自定义安全组还要确认出站允许。" ;;
    digitalocean) warn "DigitalOcean：Cloud Firewall 必须关联当前 Droplet；没有出站规则时也会阻止全部出站流量。" ;;
    hetzner) warn "Hetzner：Cloud Firewall 入站为隐式拒绝，并要在 Apply to 中确认目标 Server/Label。" ;;
    vultr) warn "Vultr：确认 Firewall Group 已关联到当前实例，并分别添加 TCP/UDP 规则。" ;;
    linode) warn "Linode：Cloud Firewall 默认入站策略通常为 Drop，需添加允许规则，并确认状态为 Enabled、设备已关联。" ;;
    *) warn "DMIT/通用 KVM：若商家控制台启用了 Firewall/Security Group，也必须在那里放行上述端口。" ;;
  esac
  warn "云防火墙与 VPS 兼容要点：https://github.com/${REPO}/blob/main/README.zh-CN.md#云防火墙与-vps-兼容性"
  case "$provider" in
    aws) warn "EC2 普通公网 IPv4 在 Stop/Start 后通常会变化；域名长期使用前建议绑定 Elastic IP。" ;;
    gcp) warn "GCP 临时外部 IP 会在停止/挂起后释放；域名长期使用前建议提升为静态外部 IP。" ;;
    azure) warn "Azure 动态公网 IP 在 Stop/Deallocate 后可能变化；域名长期使用前建议设为 Static。" ;;
  esac
}
country_flag() {
  local country_code first second first_escape second_escape
  country_code="$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')"
  [[ ${#country_code} -eq 2 && "$country_code" =~ ^[A-Z]{2}$ ]] || return 0
  printf -v first '%d' "'${country_code:0:1}"
  printf -v second '%d' "'${country_code:1:1}"
  printf -v first_escape '\\U%08x' "$((0x1F1E6 + first - 65))"
  printf -v second_escape '\\U%08x' "$((0x1F1E6 + second - 65))"
  printf '%b%b' "$first_escape" "$second_escape"
}
location_node_name() {
  local country="${1:-}" country_code="${2:-}" city="${3:-}" location
  # Prefer the two-letter code: it keeps names short and predictable where the
  # full country name would be long or multi-word ("United States-Seattle").
  if [[ -n "$country_code" ]]; then
    location="$(printf '%s' "$country_code" | tr '[:lower:]' '[:upper:]')"
  else
    location="$country"
  fi
  if [[ -n "$city" && "$city" != "$location" ]]; then
    # When a country code is available, keep the city even if the country and
    # city share a name (Singapore -> SG-Singapore). Without a country code,
    # still avoid duplicated names such as Singapore-Singapore.
    if [[ -n "$country_code" || "$city" != "$country" ]]; then
      location="${location}-${city}"
    fi
  fi
  location="${location//[[:space:]]/}"
  printf '%s\n' "$location"
}
detect_geo() {
  GEO_CC=""; GEO_COUNTRY=""; GEO_CITY=""; GEO_ISP=""
  local response trace
  response="$(curl -4fsSL --max-time 5 'https://ipwho.is/' 2>/dev/null || true)"
  if grep -Eq '"success"[[:space:]]*:[[:space:]]*true' <<<"$response"; then
    GEO_CC="$(sed -n 's/.*"country_code"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$response" | head -n 1)"
    GEO_COUNTRY="$(sed -n 's/.*"country"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$response" | head -n 1)"
    GEO_CITY="$(sed -n 's/.*"city"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$response" | head -n 1)"
    GEO_ISP="$(sed -n 's/.*"isp"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$response" | head -n 1)"
  fi
  if [[ ! "$GEO_CC" =~ ^[A-Za-z]{2}$ ]]; then
    GEO_CC=""; GEO_COUNTRY=""; GEO_CITY=""; GEO_ISP=""
    trace="$(curl -4fsSL --max-time 5 'https://www.cloudflare.com/cdn-cgi/trace' 2>/dev/null || true)"
    GEO_CC="$(sed -n 's/^loc=\([A-Za-z][A-Za-z]\)$/\1/p' <<<"$trace" | head -n 1)"
    GEO_COUNTRY="$GEO_CC"
  fi
}
validate_node_name() {
  local node_name="$1"
  [[ -n "${node_name//[[:space:]]/}" ]] || die "节点名称不能为空。"
  [[ ! "$node_name" =~ [[:cntrl:]] ]] || die "节点名称不能包含控制字符。"
  (( ${#node_name} <= 74 )) || die "节点名称不能超过 74 个字符。"
}
urlencode_fragment() {
  local hex byte output="" character
  hex="$(printf '%s' "$1" | od -An -v -tx1 | tr -d ' \n' | tr '[:lower:]' '[:upper:]')"
  while [[ -n "$hex" ]]; do
    byte="${hex:0:2}"; hex="${hex:2}"
    case "$byte" in
      2D|2E|5F|7E|3[0-9]|4[1-9A-F]|5[0-9A]|6[1-9A-F]|7[0-9A])
        printf -v character '%b' "\\x${byte}"
        output+="$character"
        ;;
      *) output+="%${byte}" ;;
    esac
  done
  printf '%s\n' "$output"
}
check_dns() {
  local domain="$1" public resolved
  public="$(public_ipv4)"
  [[ -n "$public" ]] || die "无法获取本机公网 IPv4，请确认服务器具有可用的 IPv4 出口。"
  resolved="$(getent ahostsv4 "$domain" 2>/dev/null | awk '{print $1}' | sort -u || true)"
  [[ -n "$resolved" ]] || die "域名 ${domain} 没有可用的 A 记录。"
  if ! grep -Fxq "$public" <<<"$resolved"; then
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
  if ! [[ "$port" =~ ^[0-9]+$ ]] || ! (( port >= 1 && port <= 65535 )); then
    die "面板端口必须是 1 到 65535 的整数。"
  fi
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
  info "本机端口预检通过：TCP/80、TCP/443、UDP/443、TCP/${panel_port} 均可用。"
}
# systemd calls a Type=simple unit active as soon as it forks, so probing right
# after systemctl start races the listener's bind. sing-box wins that race only
# because it is started first; the panel, started last and probed immediately,
# loses it on a slower VPS. Poll to a deadline instead of sampling once.
wait_for_listener() {
  local port="$1" network="$2" deadline=$((SECONDS + 20))
  while true; do
    port_free "$port" "$network" || return 0
    (( SECONDS < deadline )) || return 1
    sleep 0.5
  done
}
# Reports failures instead of dying: the caller has already generated the admin
# password by this point, and exiting here would take it to the grave.
post_install_check() {
  local domain="$1" panel_port="$2" status="" deadline
  wait_for_listener 443 tcp || { warn "安装后检查失败：sing-box 未监听 TCP/443。"; return 1; }
  wait_for_listener 443 udp || { warn "安装后检查失败：sing-box 未监听 UDP/443。"; return 1; }
  wait_for_listener "$panel_port" tcp || { warn "安装后检查失败：面板未监听 TCP/${panel_port}。"; return 1; }
  deadline=$((SECONDS + 20))
  while true; do
    status="$(curl -sS --max-time 8 --resolve "${domain}:${panel_port}:127.0.0.1" -o /dev/null -w '%{http_code}' "https://${domain}:${panel_port}/api/me" 2>/dev/null || true)"
    if [[ "$status" == 401 ]]; then break; fi
    (( SECONDS < deadline )) || { warn "安装后检查失败：面板 HTTPS 健康检查未通过（HTTP ${status:-无响应}）。"; return 1; }
    sleep 0.5
  done
  info "安装后检查通过：TCP/443、UDP/443 和 TCP/${panel_port} 正在监听，面板 HTTPS 响应正常。"
}
install_deps() {
  local package status missing=()
  info "检查最少运行依赖…"
  export DEBIAN_FRONTEND=noninteractive
  for package in "${RUNTIME_PACKAGES[@]}"; do
    status="$(dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true)"
    [[ "$status" == "install ok installed" ]] || missing+=("$package")
  done
  if ((${#missing[@]} > 0)); then
    info "安装缺少的运行依赖：${missing[*]}"
    apt-get update -y >/dev/null || die "apt 软件源更新失败，请检查 /etc/apt/sources.list 和服务器网络。"
    apt-get install -y --no-install-recommends "${missing[@]}" >/dev/null \
      || die "运行依赖安装失败，请修复 apt 报错后重新运行安装命令。"
  fi
  check_required_commands
  command -v crontab >/dev/null || die "cron 已安装，但未找到 crontab，无法配置证书自动续期。"
  systemctl enable --now cron.service >/dev/null 2>&1 || die "无法启动 cron 服务，证书将不能自动续期。"
}
check_required_commands() {
  local command_name missing=()
  for command_name in awk chmod cp crontab curl cut date df getent grep gzip head install iptables journalctl mktemp od openssl rm sed sha256sum sort ss sysctl systemctl systemd-analyze tar touch tr; do
    command -v "$command_name" >/dev/null 2>&1 || missing+=("$command_name")
  done
  ((${#missing[@]} == 0)) || die "依赖安装后仍缺少命令：${missing[*]}。请检查 apt 软件源后重试。"
  info "运行依赖检查通过。"
}
check_network() {
  local target
  for target in \
    "GitHub API|https://api.github.com/repos/${REPO}/releases/latest" \
    "GitHub Release|https://github.com/${REPO}/releases/latest" \
    "acme.sh|https://get.acme.sh" \
    "Let's Encrypt|https://acme-v02.api.letsencrypt.org/directory" \
    "公网 IPv4 检测|https://api.ipify.org"; do
    if ! curl -4fsSL --max-time 15 -o /dev/null "${target#*|}"; then
      die "无法访问 ${target%%|*}（${target#*|}）。请检查 DNS、防火墙或服务器网络后重试。"
    fi
  done
  info "外部网络检查通过：GitHub、acme.sh、Let's Encrypt 均可访问。"
}
check_disk_space() {
  local available_kb
  available_kb="$(df -Pk / | awk 'NR == 2 { print $4 }')"
  [[ "$available_kb" =~ ^[0-9]+$ ]] || die "无法读取根分区可用空间。"
  (( available_kb >= 200 * 1024 )) || die "根分区可用空间不足 200 MiB，请清理磁盘后重试。"
  info "磁盘空间检查通过。"
}
enable_bbr() {
  install -d -m 0755 /etc/sysctl.d
  printf '%s\n' 'net.core.default_qdisc=fq' 'net.ipv4.tcp_congestion_control=bbr' > /etc/sysctl.d/99-sbm-bbr.conf
  if sysctl --system >/dev/null 2>&1; then
    info "BBR 网络参数已应用。"
  else
    warn "当前内核未能应用 BBR 参数；这不影响 SBM 安装，可稍后检查内核支持。"
  fi
}
github_latest_tag() {
  local repository="$1"
  curl -fsSL "https://api.github.com/repos/${repository}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p'
}
panel_update_target_version() {
  local latest
  if [[ -n "${SBM_VERSION:-}" ]]; then
    requested_sbm_version
    return
  fi
  latest="$(github_latest_tag "$REPO")"
  [[ -n "$latest" ]] || die "无法查询 SBM 最新版本。"
  normalize_tag "$latest"
}
installed_panel_version() {
  "$SBM_BIN" version
}
core_update_target_version() {
  requested_sing_box_version "$(installed_panel_version)"
}
normalize_tag() {
  local tag="$1"
  [[ "$tag" == v* ]] && printf '%s\n' "$tag" || printf 'v%s\n' "$tag"
}
requested_sbm_version() {
  local tag
  tag="$(normalize_tag "${SBM_VERSION:-$SBM_RELEASE_VERSION}")"
  [[ "$tag" == v2.* ]] || die "当前安装器只支持 SBM 2.x 全新安装，不提供旧版安装或降级。"
  printf '%s\n' "$tag"
}
compatible_sing_box_version() {
  local sbm_version
  sbm_version="$(normalize_tag "$1")"
  case "$sbm_version" in
    v2.0.0) printf 'v1.13.14\n' ;;
    *) die "SBM ${sbm_version#v} 没有内置已验证的 sing-box 版本；请同时设置 SING_BOX_VERSION。" ;;
  esac
}
requested_sing_box_version() {
  local sbm_version="$1"
  if [[ -n "${SING_BOX_VERSION:-}" ]]; then
    normalize_tag "$SING_BOX_VERSION"
    return
  fi
  compatible_sing_box_version "$sbm_version"
}
github_asset_sha256() {
  local repository="$1" tag="$2" asset="$3"
  curl -fsSL "https://api.github.com/repos/${repository}/releases/tags/${tag}" | awk -v target="$asset" '
    index($0, "\"name\": \"" target "\"") { found=1 }
    found && result == "" && /"digest": "sha256:/ {
      value=$0
      sub(/^.*"digest": "sha256:/, "", value)
      sub(/".*$/, "", value)
      result=value
      found=0
    }
    found && /"browser_download_url"/ { found=0 }
    END { if (result != "") print result }
  '
}
install_sing_box() {
  local arch tag release_version asset url temp_dir expected actual candidate
  arch="$(arch_tag)"; tag="${1:-$(requested_sing_box_version "$(requested_sbm_version)")}"
  tag="$(normalize_tag "$tag")"; release_version="${tag#v}"; asset="sing-box-${release_version}-linux-${arch}.tar.gz"
  url="https://github.com/SagerNet/sing-box/releases/download/${tag}/${asset}"
  expected="$(github_asset_sha256 SagerNet/sing-box "$tag" "$asset")"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || die "无法获取 sing-box Release 的 SHA-256 摘要。"
  temp_dir="$(mktemp -d /tmp/sbm-sing-box.XXXXXX)"
  curl -fsSL "$url" -o "${temp_dir}/${asset}" || { rm -rf "$temp_dir"; die "下载 sing-box 失败。"; }
  actual="$(sha256sum "${temp_dir}/${asset}" | awk '{print $1}')"
  [[ "$expected" == "$actual" ]] || { rm -rf "$temp_dir"; die "sing-box 下载校验失败。"; }
  tar -xzf "${temp_dir}/${asset}" -C "$temp_dir"
  candidate="${temp_dir}/sing-box-${release_version}-linux-${arch}/sing-box"
  [[ -x "$candidate" ]] || { rm -rf "$temp_dir"; die "sing-box Release 内容无效。"; }
  "$candidate" version >/dev/null || { rm -rf "$temp_dir"; die "sing-box 新版本无法运行。"; }
  [[ -x "$SING_BOX_BIN" ]] && cp -p "$SING_BOX_BIN" "${SING_BOX_BIN}.bak"
  install -m 0755 "$candidate" "$SING_BOX_BIN"
  rm -rf "$temp_dir"
  info "已安装 $($SING_BOX_BIN version | sed -n '1p')。"
}
install_panel() {
  local arch tag release_version asset base_url temp_dir expected actual
  arch="$(arch_tag)"; tag="${1:-$(requested_sbm_version)}"
  tag="$(normalize_tag "$tag")"; release_version="${tag#v}"; asset="sbm-panel_${release_version}_linux_${arch}.tar.gz"
  base_url="https://github.com/${REPO}/releases/download/${tag}"
  temp_dir="$(mktemp -d /tmp/sbm-panel.XXXXXX)"
  curl -fsSL "${base_url}/${asset}" -o "${temp_dir}/${asset}" || { rm -rf "$temp_dir"; die "下载 sbm-panel 失败；请确认仓库已有对应架构的 Release。"; }
  curl -fsSL "${base_url}/checksums.txt" -o "${temp_dir}/checksums.txt" || { rm -rf "$temp_dir"; die "下载校验文件失败。"; }
  expected="$(awk -v name="$asset" '$2 == name {print $1}' "${temp_dir}/checksums.txt")"
  actual="$(sha256sum "${temp_dir}/${asset}" | awk '{print $1}')"
  [[ -n "$expected" && "$expected" == "$actual" ]] || { rm -rf "$temp_dir"; die "sbm-panel 下载校验失败。"; }
  tar -xzf "${temp_dir}/${asset}" -C "$temp_dir"
  [[ -x "${temp_dir}/sbm-panel" && "$("${temp_dir}/sbm-panel" version)" == "$release_version" ]] || { rm -rf "$temp_dir"; die "sbm-panel Release 内容或版本无效。"; }
  [[ -x "$SBM_BIN" ]] && cp -p "$SBM_BIN" "${SBM_BIN}.bak"
  install -m 0755 "${temp_dir}/sbm-panel" "$SBM_BIN"
  if [[ -f "${temp_dir}/sbm" ]]; then
    bash -n "${temp_dir}/sbm" || { rm -rf "$temp_dir"; die "Release 中的 sbm 管理脚本校验失败。"; }
    [[ -f "$SBM_CMD" ]] && cp -p "$SBM_CMD" "${SBM_CMD}.bak"
    install -m 0755 "${temp_dir}/sbm" "$SBM_CMD"
  fi
  rm -rf "$temp_dir"
  info "已安装 sbm-panel $($SBM_BIN version)。"
}
# acme.sh refuses to run when it believes sudo was used to launch acme.sh itself,
# and it decides that by looking for its own name inside SUDO_COMMAND. README asks
# for sudo -i first, which keeps SUDO_COMMAND down to the shell, but running this
# script as sudo bash -c "$(curl ...)" puts the whole script — every mention of
# acme.sh included — into SUDO_COMMAND and trips the check on an otherwise normal
# install. It only fires when stdout is a terminal, so it hides from redirected
# calls. We already require root here, so the sudo breadcrumbs carry no meaning.
acme() { env -u SUDO_COMMAND -u SUDO_USER -u SUDO_UID -u SUDO_GID "$ACME_BIN" "$@"; }
issue_certificate() {
  local domain="$1"
  if [[ ! -x "$ACME_BIN" ]]; then
    info "安装 acme.sh…"
    if ! curl -fsSL https://get.acme.sh | sh >/dev/null; then
      die "acme.sh 安装失败。请确认 cron 服务和网络正常后重试。"
    fi
    [[ -x "$ACME_BIN" ]] || die "acme.sh 安装未生成 ${ACME_BIN}，已停止后续证书操作。"
  fi
  acme --version >/dev/null 2>&1 || die "acme.sh 客户端不可用，请删除 /root/.acme.sh 后重试。"
  ensure_acme_cron
  acme --set-default-ca --server letsencrypt >/dev/null || die "无法设置 Let's Encrypt 为默认证书机构。"
  acme --register-account >/dev/null 2>&1 || true
  info "通过 Let's Encrypt HTTP-01 申请证书…"
  acme --issue --standalone -d "$domain" --keylength 2048 \
    || die "Let's Encrypt 证书申请失败，请检查域名 A 记录、Cloudflare 灰云、云平台外层防火墙与系统防火墙的 TCP/80，以及系统时间。"
  install -d -m 0700 "$CERT_DIR" /usr/local/lib/sbm
  write_cert_reload_hook
  acme --install-cert -d "$domain" \
    --key-file "${CERT_DIR}/key.pem" \
    --fullchain-file "${CERT_DIR}/fullchain.pem" \
    --reloadcmd "$CERT_RELOAD" \
    || die "证书签发成功，但安装到 ${CERT_DIR} 失败。"
  chmod 0600 "${CERT_DIR}/key.pem" "${CERT_DIR}/fullchain.pem"
  [[ -s "${CERT_DIR}/key.pem" && -s "${CERT_DIR}/fullchain.pem" ]] || die "证书安装失败：证书或私钥文件为空。"
  openssl pkey -in "${CERT_DIR}/key.pem" -noout >/dev/null 2>&1 || die "证书安装失败：私钥文件无效。"
  openssl x509 -in "${CERT_DIR}/fullchain.pem" -noout -checkend 86400 >/dev/null 2>&1 || die "证书安装失败：证书无效或将在 24 小时内过期。"
  info "证书与私钥检查通过，自动续期任务已启用。"
}
ensure_acme_cron() {
  local cron_file
  if crontab -l 2>/dev/null | grep -E 'acme\.sh.*--cron' >/dev/null; then
    return
  fi
  warn "acme.sh 未创建自动续期任务，正在自动补齐。"
  cron_file="$(mktemp /tmp/sbm-crontab.XXXXXX)"
  crontab -l > "$cron_file" 2>/dev/null || true
  [[ ! -s "$cron_file" ]] || printf '\n' >> "$cron_file"
  printf '17 3 * * * "%s" --cron --home "/root/.acme.sh" > /dev/null\n' "$ACME_BIN" >> "$cron_file"
  crontab "$cron_file" || { rm -f "$cron_file"; die "无法写入证书自动续期任务。"; }
  rm -f "$cron_file"
  crontab -l 2>/dev/null | grep -E 'acme\.sh.*--cron' >/dev/null || die "证书自动续期任务验证失败。"
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
write_firewall_helper() {
  install -d -m 0755 /usr/local/lib/sbm
  cat > "$FIREWALL_HELPER" <<'HELPER'
#!/usr/bin/env bash
set -Eeuo pipefail
readonly MODE_FILE="${SBM_FIREWALL_MODE_FILE:-/etc/sbm/firewall-mode}"
readonly PORTS_FILE="${SBM_FIREWALL_PORTS_FILE:-/etc/sbm/firewall-ports}"

valid_rule() {
  [[ "${1:-}" == tcp || "${1:-}" == udp ]] && [[ "${2:-}" =~ ^[0-9]+$ ]] && (( $2 >= 1 && $2 <= 65535 ))
}
apply_rule() {
  local network="$1" port="$2" mode
  if [[ -r "$MODE_FILE" ]]; then mode="$(<"$MODE_FILE")"; else mode="none"; fi
  case "$mode" in
    ufw)
      ufw allow "${port}/${network}" >/dev/null
      ;;
    firewalld)
      firewall-cmd --quiet --permanent --query-port="${port}/${network}" || firewall-cmd --quiet --permanent --add-port="${port}/${network}"
      firewall-cmd --quiet --query-port="${port}/${network}" || firewall-cmd --quiet --add-port="${port}/${network}"
      ;;
    iptables)
      iptables -w -C INPUT -p "$network" --dport "$port" -j ACCEPT 2>/dev/null || iptables -w -I INPUT 1 -p "$network" --dport "$port" -j ACCEPT
      ;;
    none) ;;
    *) printf '未知的 SBM 防火墙模式：%s\n' "$mode" >&2; exit 1 ;;
  esac
}
revoke_rule() {
  local network="$1" port="$2" mode
  if [[ -r "$MODE_FILE" ]]; then mode="$(<"$MODE_FILE")"; else mode="none"; fi
  case "$mode" in
    ufw)
      ufw delete allow "${port}/${network}" >/dev/null 2>&1 || true
      ;;
    firewalld)
      firewall-cmd --quiet --permanent --remove-port="${port}/${network}" >/dev/null 2>&1 || true
      firewall-cmd --quiet --remove-port="${port}/${network}" >/dev/null 2>&1 || true
      ;;
    iptables)
      # The same rule can have been inserted more than once over time.
      while iptables -w -C INPUT -p "$network" --dport "$port" -j ACCEPT 2>/dev/null; do
        iptables -w -D INPUT -p "$network" --dport "$port" -j ACCEPT 2>/dev/null || break
      done
      ;;
    none) ;;
    *) printf '未知的 SBM 防火墙模式：%s\n' "$mode" >&2; exit 1 ;;
  esac
}
record_rule() {
  local rule="$1 $2"
  install -d -m 0700 "$(dirname "$PORTS_FILE")"
  touch "$PORTS_FILE"; chmod 0600 "$PORTS_FILE"
  grep -Fqx "$rule" "$PORTS_FILE" 2>/dev/null || printf '%s\n' "$rule" >> "$PORTS_FILE"
}
forget_rule() {
  local rule="$1 $2" kept
  [[ -r "$PORTS_FILE" ]] || return 0
  kept="$(grep -Fxv "$rule" "$PORTS_FILE" || true)"
  if [[ -n "$kept" ]]; then printf '%s\n' "$kept" > "$PORTS_FILE"; else : > "$PORTS_FILE"; fi
  chmod 0600 "$PORTS_FILE"
}

if [[ "${1:-}" == --restore ]]; then
  [[ -r "$PORTS_FILE" ]] || exit 0
  while read -r network port; do
    [[ -n "${network:-}" ]] || continue
    valid_rule "$network" "$port" || { printf '忽略无效的防火墙规则：%s %s\n' "$network" "$port" >&2; continue; }
    apply_rule "$network" "$port"
  done < "$PORTS_FILE"
  exit 0
fi

if [[ "${1:-}" == --revoke-all ]]; then
  [[ -r "$PORTS_FILE" ]] || exit 0
  while read -r network port; do
    valid_rule "$network" "$port" || continue
    revoke_rule "$network" "$port"
  done < "$PORTS_FILE"
  : > "$PORTS_FILE"
  exit 0
fi

if [[ "${1:-}" == --close ]]; then
  shift
  valid_rule "${1:-}" "${2:-}" || { printf '用法：open-port.sh --close <tcp|udp> <1-65535>\n' >&2; exit 2; }
  # Renewal needs TCP/80 whatever the panel thinks it no longer uses.
  if [[ "$1 $2" == "tcp 80" ]]; then
    exit 0
  fi
  forget_rule "$1" "$2"
  revoke_rule "$1" "$2"
  exit 0
fi

valid_rule "${1:-}" "${2:-}" || { printf '用法：open-port.sh [--close|--restore|--revoke-all] <tcp|udp> <1-65535>\n' >&2; exit 2; }
record_rule "$1" "$2"
apply_rule "$1" "$2"
HELPER
  chmod 0755 "$FIREWALL_HELPER"
}
write_core_guard() {
  install -d -m 0755 /usr/local/lib/sbm
  cat > "$CORE_GUARD" <<'GUARD'
#!/usr/bin/env bash
set -euo pipefail
if grep -Eq '"quotaExceeded"[[:space:]]*:[[:space:]]*true' /var/lib/sbm/state.json 2>/dev/null; then
  echo "SBM 已达到代理安全阈值，sing-box 保持停止。" >&2
  exit 1
fi
GUARD
  chmod 0755 "$CORE_GUARD"
}
write_services() {
  install -d -m 0700 "$CONFIG_DIR" "$STATE_DIR" "$CORE_DIR"
  write_firewall_helper
  write_core_guard
  cat > "$FIREWALL_SERVICE" <<EOF
[Unit]
Description=Restore SBM host firewall rules
After=local-fs.target ufw.service firewalld.service netfilter-persistent.service
Before=sing-box.service sbm-panel.service

[Service]
Type=oneshot
ExecStart=${FIREWALL_HELPER} --restore
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
  cat > /etc/systemd/system/sing-box.service <<EOF
[Unit]
Description=sing-box proxy core
After=network-online.target sbm-firewall.service
Wants=network-online.target sbm-firewall.service
StartLimitIntervalSec=0

[Service]
Type=simple
ExecCondition=${CORE_GUARD}
ExecStartPre=${SING_BOX_BIN} check -c ${CORE_CONFIG}
ExecStart=${SING_BOX_BIN} run -c ${CORE_CONFIG} -D /var/lib/sing-box
Restart=always
RestartSec=5s
TimeoutStartSec=30s
TimeoutStopSec=30s
LimitNOFILE=1048576
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
  cat > /etc/systemd/system/sbm-panel.service <<EOF
[Unit]
Description=SBM sing-box management panel
After=network-online.target sbm-firewall.service sing-box.service
Wants=network-online.target sbm-firewall.service
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=${SBM_BIN} serve
Restart=always
RestartSec=5s
TimeoutStartSec=30s
TimeoutStopSec=30s
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
detect_host_firewall_mode() {
  local provider="$1" input_rules
  if [[ "$provider" == oracle ]]; then
    printf 'iptables\n'; return
  fi
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
    printf 'ufw\n'; return
  fi
  if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld.service; then
    printf 'firewalld\n'; return
  fi
  input_rules="$(iptables -S INPUT 2>/dev/null || true)"
  if grep -Eq '^-P INPUT (DROP|REJECT)|[[:space:]]-j (DROP|REJECT)([[:space:]]|$)' <<<"$input_rules"; then
    printf 'iptables\n'; return
  fi
  printf 'none\n'
}
desired_firewall_rules() {
  local panel_port="$1" config_file="${2:-}"
  {
    printf 'tcp 80\ntcp %s\n' "$panel_port"
    if [[ -n "$config_file" && -r "$config_file" ]]; then
      awk '
        BEGIN { RS="{" }
        /"enabled"[[:space:]]*:[[:space:]]*true/ && /"port"[[:space:]]*:[[:space:]]*[0-9]+/ {
          network=""
          if ($0 ~ /"type"[[:space:]]*:[[:space:]]*"vless-reality"/) network="tcp"
          if ($0 ~ /"type"[[:space:]]*:[[:space:]]*"hysteria2"/) network="udp"
          if (network != "") {
            value=$0
            sub(/^.*"port"[[:space:]]*:[[:space:]]*/, "", value)
            sub(/[^0-9].*$/, "", value)
            if (value ~ /^[0-9]+$/) print network, value
          }
        }
      ' "$config_file"
    else
      printf 'tcp 443\nudp 443\n'
    fi
  } | sort -u
}

open_firewall() {
  local provider="$1" panel_port="$2" mode desired_file tracked_file
  mode="$(detect_host_firewall_mode "$provider")"
  printf '%s\n' "$mode" > "$FIREWALL_MODE"; chmod 0600 "$FIREWALL_MODE"
  touch "$FIREWALL_PORTS"; chmod 0600 "$FIREWALL_PORTS"
  desired_file="$(mktemp /tmp/sbm-firewall-desired.XXXXXX)"
  tracked_file="$(mktemp /tmp/sbm-firewall-tracked.XXXXXX)"
  desired_firewall_rules "$panel_port" "$CONFIG_FILE" > "$desired_file"
  cp "$FIREWALL_PORTS" "$tracked_file"

  # Open every currently required port before removing stale entries, so a
  # repair cannot create an avoidable interruption when ports have changed.
  while read -r inbound_network inbound_port; do
    [[ -n "${inbound_network:-}" ]] && "$FIREWALL_HELPER" "$inbound_network" "$inbound_port"
  done < "$desired_file"
  while read -r inbound_network inbound_port; do
    [[ -n "${inbound_network:-}" ]] || continue
    grep -Fqx "$inbound_network $inbound_port" "$desired_file" || "$FIREWALL_HELPER" --close "$inbound_network" "$inbound_port"
  done < "$tracked_file"
  rm -f "$desired_file" "$tracked_file"
  case "$mode" in
    ufw) info "已通过 UFW 放行 SBM 端口，规则会在重启后保留。" ;;
    firewalld) info "已通过 firewalld 永久放行 SBM 端口。" ;;
    iptables) info "已增量放行 iptables 规则，并配置开机自动恢复；未清空系统原有规则。" ;;
    none) info "未检测到启用的宿主机入站防火墙；未额外修改系统防火墙规则。" ;;
  esac
}
repair_runtime() {
  local provider panel_port
  install_deps
  provider="$(detect_cloud_provider)"
  panel_port="$(json_number panelPort "$CONFIG_FILE")"
  [[ -n "$panel_port" ]] || { warn "无法从配置读取面板端口。"; return 1; }
  info "修复运行环境：$(cloud_provider_name "$provider")"
  show_cloud_firewall_guide "$provider" "$panel_port"
  write_services
  open_firewall "$provider" "$panel_port"
  verify_boot_services
  systemctl restart sbm-firewall.service || return 1
  if quota_exceeded; then systemctl stop sing-box.service || true; else systemctl restart sing-box.service || return 1; fi
  systemctl restart sbm-panel.service || return 1
  systemctl is-active --quiet sbm-panel.service || return 1
  quota_exceeded || systemctl is-active --quiet sing-box.service || return 1
  info "开机启动、宿主机防火墙和运行服务均已修复并通过检查。"
}
verify_boot_services() {
  systemd-analyze verify "$FIREWALL_SERVICE" /etc/systemd/system/sing-box.service /etc/systemd/system/sbm-panel.service >/dev/null 2>&1 \
    || die "systemd 开机启动配置校验失败。"
  systemctl enable sbm-firewall.service sing-box.service sbm-panel.service >/dev/null \
    || die "无法启用 SBM 开机启动服务。"
  for service_name in sbm-firewall.service sing-box.service sbm-panel.service; do
    systemctl is-enabled --quiet "$service_name" || die "${service_name} 未成功设为开机启动。"
  done
  info "开机恢复检查通过：防火墙、sing-box 和面板均已启用，服务异常退出会持续重试。"
}
install_sbm_command() {
  # BASH_SOURCE is empty when the script is run as `bash -c "$(curl ...)"`, and
  # a /dev/fd path when run via process substitution; neither can be copied.
  local source_path="${BASH_SOURCE[0]:-}"
  if [[ -n "$source_path" && -f "$source_path" && "$source_path" != /dev/fd/* ]]; then
    install -m 0755 "$source_path" "$SBM_CMD"
  elif [[ -x "$SBM_CMD" ]]; then
    return
  else
    curl -fsSL "$SELF_URL" -o "$SBM_CMD"
    chmod 0755 "$SBM_CMD"
  fi
}
do_install() {
  need_root; check_os; check_systemd; arch_tag >/dev/null
  printf '%s===== SBM 极简 sing-box 面板安装 =====%s\n' "$CYAN" "$RESET"
  local domain panel_port node_name default_node_name flag location cloud_provider admin_password password_file encoded_node_name target_sbm_version target_sing_box_version
  target_sbm_version="$(requested_sbm_version)"
  target_sing_box_version="$(requested_sing_box_version "$target_sbm_version")"
  info "安装版本：SBM ${target_sbm_version#v} + sing-box ${target_sing_box_version#v}"
  read -r -p "Domain: " domain
  domain="$(cleanup_domain "$domain")"; validate_domain "$domain"
  read -r -p "面板端口 [2096]: " panel_port
  panel_port="${panel_port:-2096}"; validate_panel_port "$panel_port"
  install_deps
  check_ports "$panel_port"
  cloud_provider="$(detect_cloud_provider)"
  info "云平台识别：$(cloud_provider_name "$cloud_provider")"
  show_cloud_firewall_guide "$cloud_provider" "$panel_port"
  info "检测服务器所在地区，用于生成国家—城市节点名称…"
  detect_geo
  default_node_name="MyNode"
  if [[ -n "$GEO_CC" ]]; then
    flag="$(country_flag "$GEO_CC")"
    location="$(location_node_name "$GEO_COUNTRY" "$GEO_CC" "$GEO_CITY")"
    default_node_name="$location"
    info "检测到：${flag} ${GEO_COUNTRY:-$GEO_CC} ${GEO_CITY}${GEO_ISP:+ · $GEO_ISP}"
  else
    warn "地区检测失败，将使用 MyNode；安装仍可继续，之后可在面板中修改节点名称。"
  fi
  read -r -p "节点名称 [${default_node_name}]: " node_name
  node_name="${node_name:-$default_node_name}"; node_name="${node_name//$'\r'/}"; validate_node_name "$node_name"
  check_disk_space; check_network; check_dns "$domain"
  enable_bbr
  install_sing_box "$target_sing_box_version"
  install_panel "$target_sbm_version"
  write_services
  open_firewall "$cloud_provider" "$panel_port"
  issue_certificate "$domain"
  admin_password="$(openssl rand -base64 32 | tr -d '/+=' | cut -c1-24)"
  password_file="$(mktemp /tmp/sbm-password.XXXXXX)"; chmod 0600 "$password_file"; printf '%s' "$admin_password" > "$password_file"
  if ! "$SBM_BIN" init --domain "$domain" --panel-port "$panel_port" --node-name "$node_name" --admin-password-file "$password_file"; then rm -f "$password_file"; die "初始化面板配置失败。"; fi
  rm -f "$password_file"
  "$SBM_BIN" config apply --no-start
  install_sbm_command
  verify_boot_services
  systemctl start sbm-firewall.service >/dev/null || die "宿主机防火墙恢复服务启动失败。"
  systemctl start sing-box.service >/dev/null || die "sing-box 启动失败，请执行 journalctl -u sing-box -e。"
  systemctl start sbm-panel.service >/dev/null || die "面板启动失败，请执行 journalctl -u sbm-panel -e。"
  systemctl is-active --quiet sing-box.service || die "sing-box 启动失败，请执行 journalctl -u sing-box -e。"
  systemctl is-active --quiet sbm-panel.service || die "面板启动失败，请执行 journalctl -u sbm-panel -e。"
  local checked=1
  post_install_check "$domain" "$panel_port" || checked=0
  local token; token="$(json_string subscriptionToken "$CONFIG_FILE")"
  encoded_node_name="$(urlencode_fragment "$node_name")"
  if (( checked )); then
    printf '\n%s安装完成%s\n' "$GREEN" "$RESET"
  else
    printf '\n%s安装已完成，但检查未通过%s\n' "$YELLOW" "$RESET"
  fi
  printf '面板地址：%shttps://%s:%s/%s\n' "$CYAN" "$domain" "$panel_port" "$RESET"
  printf '用户名：  admin\n密码：    %s%s%s\n' "$YELLOW" "$admin_password" "$RESET"
  printf '总订阅：  %shttps://%s:%s/sub/%s#%s%s\n' "$CYAN" "$domain" "$panel_port" "$token" "$encoded_node_name" "$RESET"
  warn "密码只显示这一次；请立即保存。本机检查无法判断云厂商安全组，请手动确认已放行 TCP/80、TCP/443、UDP/443、TCP/${panel_port}。"
  printf '以后可运行 sudo sbm：选择 1 重新查看地址，选择 4 重置管理员密码。\n'
  (( checked )) || die "请按上面的检查结果排查：journalctl -u sbm-panel -e、journalctl -u sing-box -e。凭据已在上方打印。"
}
json_string() {
  local key="$1" file="$2"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$file"
}
json_number() {
  local key="$1" file="$2"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p" "$file"
}
json_bool() {
  local key="$1" file="$2"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\(true\|false\).*/\1/p" "$file"
}
subscription_name_from_config() {
  local name
  name="$(awk -F'"' '/"inbounds"[[:space:]]*:/ { inbounds=1; next } inbounds && $2 == "name" { print $4; exit }' "$CONFIG_FILE")"
  case "$name" in
    *-VLESS) name="${name%-VLESS}" ;;
    *-HY2) name="${name%-HY2}" ;;
  esac
  printf '%s\n' "${name:-SBM}"
}
quota_exceeded() { grep -Eq '"quotaExceeded"[[:space:]]*:[[:space:]]*true' "$STATE_FILE" 2>/dev/null; }
show_status() {
  local domain panel_port token subscription_name encoded_subscription_name
  domain="$(json_string domain "$CONFIG_FILE")"; panel_port="$(json_number panelPort "$CONFIG_FILE")"; token="$(json_string subscriptionToken "$CONFIG_FILE")"; subscription_name="$(subscription_name_from_config)"
  encoded_subscription_name="$(urlencode_fragment "$subscription_name")"
  printf '面板：  https://%s:%s/\n用户名：admin\n订阅：  https://%s:%s/sub/%s#%s\n\n' "$domain" "$panel_port" "$domain" "$panel_port" "$token" "$encoded_subscription_name"
  systemctl --no-pager --full status sbm-panel.service sing-box.service | sed -n '1,24p' || true
}
restart_core() { quota_exceeded && { warn "已达到代理安全阈值，禁止直接重启 sing-box。请在面板中重置流量或提高限额。"; return; }; systemctl restart sing-box.service; info "sing-box 已重启。"; }
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
toggle_web_management() {
  local enabled action
  enabled="$(json_bool webManagementEnabled "$CONFIG_FILE")"
  case "$enabled" in
    true) action=lock ;;
    false) action=unlock ;;
    *) warn "无法读取 Web 管理入口状态。"; return 1 ;;
  esac
  "$SBM_BIN" admin "$action" || return 1
  systemctl restart sbm-panel.service || { warn "面板重启失败，请执行 journalctl -u sbm-panel -e。"; return 1; }
  if [[ "$action" == lock ]]; then
    info "Web 页面、登录和管理 API 已锁定；订阅地址继续可用。再次选择此菜单项可解锁。"
  else
    info "Web 管理入口已重新开启。"
  fi
}
service_healthy_after_restart() {
  local service="$1"
  systemctl restart "$service" || return 1
  sleep 1
  systemctl is-active --quiet "$service"
}
restore_binary() {
  local target="$1"
  [[ -f "${target}.bak" ]] || return 1
  install -m 0755 "${target}.bak" "$target"
}
update_panel() {
	local target
	target="$(panel_update_target_version)"
	assert_panel_config_supported "$target" "$CONFIG_FILE"
	install_panel "$target"
  if repair_runtime; then
    info "面板已更新并通过运行检查。"
    return
  fi
  warn "新面板启动失败，正在恢复上一版本。"
  restore_binary "$SBM_BIN" || { warn "没有可恢复的面板版本。"; return 1; }
  restore_binary "$SBM_CMD" || true
  service_healthy_after_restart sbm-panel.service || { warn "恢复后面板仍未正常运行，请查看 journalctl -u sbm-panel -e。"; return 1; }
  warn "已恢复上一版面板。"
  return 1
}

assert_panel_config_supported() {
	local target config_path config_version
	target="$(normalize_tag "$1")"
	config_path="${2:-$CONFIG_FILE}"
	case "$target" in
		v2.*)
			[[ -f "$config_path" ]] || return 0
			config_version="$(json_number version "$config_path")"
			[[ "$config_version" == 3 ]] || die "SBM 2.x 只支持全新 v3 配置，不支持从旧配置原地升级。请先备份，再在新环境全新安装。"
			;;
	esac
}
update_core() {
  install_sing_box "$(core_update_target_version)"
  if ! "$SBM_BIN" config apply --no-start; then
    warn "新 sing-box 无法验证当前配置，正在恢复上一版本。"
    restore_binary "$SING_BOX_BIN" || { warn "没有可恢复的 sing-box 版本。"; return 1; }
    return 1
  fi
  if quota_exceeded; then
    info "sing-box 已更新；当前已达到代理安全阈值，核心保持停止。"
    return
  fi
  if service_healthy_after_restart sing-box.service; then
    info "sing-box 已更新并通过运行检查。"
    return
  fi
  warn "新 sing-box 启动失败，正在恢复上一版本。"
  restore_binary "$SING_BOX_BIN" || { warn "没有可恢复的 sing-box 版本。"; return 1; }
  "$SBM_BIN" config apply --no-start || true
  service_healthy_after_restart sing-box.service || { warn "恢复后 sing-box 仍未正常运行，请查看 journalctl -u sing-box -e。"; return 1; }
  warn "已恢复上一版 sing-box。"
  return 1
}
backup_config() {
  local target
  target="/root/sbm-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
  if ! tar -czf "$target" -C / etc/sbm var/lib/sbm etc/sing-box/config.json etc/systemd/system/sbm-firewall.service etc/systemd/system/sbm-panel.service etc/systemd/system/sing-box.service usr/local/lib/sbm; then
    rm -f "$target"
    warn "备份失败，未生成任何文件。"
    return 1
  fi
  chmod 0600 "$target"; info "备份已保存：${target}"
}
restore_config() {
  local archive entry
  read -r -p "备份文件绝对路径: " archive
  [[ "$archive" == /* && -f "$archive" ]] || { warn "备份文件不存在。"; return; }
  while IFS= read -r entry; do
    [[ "$entry" != /* && "$entry" != *".."* ]] || { warn "备份包含不安全路径，拒绝恢复。"; return; }
    case "$entry" in etc/sbm/*|var/lib/sbm/*|etc/sing-box/config.json|etc/systemd/system/sbm-firewall.service|etc/systemd/system/sbm-panel.service|etc/systemd/system/sing-box.service|usr/local/lib/sbm/*) ;; *) warn "备份包含未知路径：${entry}"; return ;; esac
  done < <(tar -tzf "$archive")
  read -r -p "恢复会覆盖当前配置，继续？[y/N]: " answer
  case "$answer" in y|Y) ;; *) return ;; esac
  systemctl stop sbm-panel.service sing-box.service || true
  # Do not carry on past a failed extraction: the system would be half restored
  # and the services below would come up on a mix of old and new files.
  if ! tar -xzf "$archive" -C /; then
    warn "解包备份失败，配置可能只恢复了一部分；请修复备份文件后重试。"
    return 1
  fi
  chmod 0600 "$CONFIG_FILE" "$STATE_FILE" "$CORE_CONFIG" || true
  systemctl daemon-reload
  systemctl enable sbm-firewall.service sbm-panel.service sing-box.service >/dev/null 2>&1 || true
  if ! systemctl start sbm-firewall.service sbm-panel.service; then
    warn "恢复后面板未能启动，请执行 journalctl -u sbm-panel -e。"
    return 1
  fi
  if ! quota_exceeded && ! systemctl start sing-box.service; then
    warn "恢复后 sing-box 未能启动，请执行 journalctl -u sing-box -e。"
    return 1
  fi
  info "备份已恢复。"
}
uninstall() {
  local answer domain
  read -r -p "这会删除 SBM、sing-box、配置、状态和证书。继续？[y/N]: " answer
  case "$answer" in y|Y) ;; *) return ;; esac
  domain="$(json_string domain "$CONFIG_FILE")"
  systemctl disable --now sbm-panel.service sing-box.service sbm-firewall.service >/dev/null 2>&1 || true
  if [[ -x "$FIREWALL_HELPER" ]]; then
    "$FIREWALL_HELPER" --revoke-all >/dev/null 2>&1 || warn "未能撤销 SBM 添加的防火墙放行规则，请手动检查 ufw/firewalld/iptables。"
  fi
  if [[ -x "$ACME_BIN" && -n "$domain" ]]; then
    acme --remove -d "$domain" >/dev/null 2>&1 || true
  fi
  rm -f "$FIREWALL_SERVICE" /etc/systemd/system/sbm-panel.service /etc/systemd/system/sing-box.service "$SBM_BIN" "$SING_BOX_BIN" "$SBM_CMD" "$CERT_RELOAD" "$FIREWALL_HELPER" "$CORE_GUARD"
  rmdir /usr/local/lib/sbm 2>/dev/null || true
  rm -rf /etc/sbm /var/lib/sbm /etc/sing-box /var/lib/sing-box
  systemctl daemon-reload
  info "已删除面板、sing-box、配置、状态与证书；/root 下的手动备份和 acme.sh 本体仍保留。"
}
restart_panel() { systemctl restart sbm-panel.service && info "面板已重启。"; }
show_logs() { journalctl -u sbm-panel.service -u sing-box.service -n 80 --no-pager; }

# Runs one menu action and always returns success, so a failed action reports
# itself and drops back to the menu instead of ending the session. Relying on
# errexit here is not portable: bash ignores it inside a function called from a
# condition, and menu used to be reached exactly that way.
#
# The action runs in a subshell because die exits, and an action that dies would
# otherwise take the whole menu session with it.
run_action() {
  local label="$1"; shift
  if ! ( "$@" ); then
    warn "「${label}」未成功完成，请查看上面的输出。"
  fi
  return 0
}
menu() {
  need_root
  while true; do
    printf '\n%s========= SBM 管理 =========%s\n' "$CYAN" "$RESET"
    printf '%s\n' '1. 查看面板地址和运行状态' '2. 重启面板' '3. 重启 sing-box' '4. 重置管理员密码' '5. 查看日志' '6. 更新面板' '7. 安装/恢复当前绑定版 sing-box' '8. 备份配置' '9. 恢复配置' '10. 卸载' '11. 修复开机启动与防火墙' '12. 锁定/解锁 Web 管理入口' '0. 退出'
    read -r -p "选择: " choice
    case "$choice" in
      1) run_action '查看运行状态' show_status ;;
      2) run_action '重启面板' restart_panel ;;
      3) run_action '重启 sing-box' restart_core ;;
      4) run_action '重置管理员密码' reset_admin_password ;;
      5) run_action '查看日志' show_logs ;;
      6) run_action '更新面板' update_panel ;;
      7) run_action '安装/恢复当前绑定版 sing-box' update_core ;;
      8) run_action '备份配置' backup_config ;;
      9) run_action '恢复配置' restore_config ;;
      10) run_action '卸载' uninstall; return ;;
      11) run_action '修复开机启动与防火墙' repair_runtime ;;
      12) run_action '切换 Web 管理入口状态' toggle_web_management ;;
      0) return ;;
      *) warn "无效选择。" ;;
    esac
  done
}
main() {
  if [[ -f "$CONFIG_FILE" ]]; then
    menu
    return
  fi
  do_install
}
# Runs when executed directly, via process substitution, or as bash -c "$(curl …)".
# The fallback matters: with bash -c the array is empty and set -u would abort.
if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
  main "$@"
fi
