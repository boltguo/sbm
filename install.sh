#!/usr/bin/env bash
#
# sbm.sh — sing-box one-click script
# Protocols: VLESS + Vision + REALITY (TCP) / Hysteria2 (UDP)
# Cert: acme.sh HTTP-01 (Let's Encrypt)
# Extras: BBR, vnstat traffic stats, base64 subscription (Shadowrocket / v2rayNG / NekoBox / Hiddify)
#
# Usage: sudo bash sbm.sh   (first run installs, later runs open the menu)
#

set -euo pipefail

# ============================== Globals ==============================
SB_DIR="/etc/sing-box"
SB_CONF="$SB_DIR/config.json"
SB_ENV="$SB_DIR/sbm.env"
SB_CERT_DIR="$SB_DIR/cert"
SB_BIN="/usr/local/bin/sing-box"
SUB_DIR="/var/www/sub"
ACME="$HOME/.acme.sh/acme.sh"
SELF="/usr/local/bin/sbm"
# Remote URL of this script (used to install the sbm command when run via curl | bash)
SELF_URL="https://raw.githubusercontent.com/boltguo/sing-box/main/install.sh"

RED=$'\e[31m'; GRN=$'\e[32m'; YLW=$'\e[33m'; BLU=$'\e[36m'; NC=$'\e[0m'
info(){ echo "${GRN}[*]${NC} $*"; }
warn(){ echo "${YLW}[!]${NC} $*"; }
err(){  echo "${RED}[x]${NC} $*" >&2; }

# ============================== Pre-checks ==============================
need_root(){ [ "$(id -u)" -eq 0 ] || { err "Please run as root: sudo bash $0"; exit 1; }; }

check_os(){
  command -v apt-get >/dev/null 2>&1 || { err "This script only supports Debian / Ubuntu (apt)"; exit 1; }
}

arch_tag(){
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) err "Unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
}

pubip(){ curl -fsSL4 https://api.ipify.org 2>/dev/null || curl -fsSL https://ifconfig.me 2>/dev/null || true; }

# Convert a 2-letter country code to an emoji flag; empty on failure
cc_flag(){
  local cc="${1^^}" a b
  [ "${#cc}" -eq 2 ] || return 0
  printf -v a '%d' "'${cc:0:1}" 2>/dev/null || return 0
  printf -v b '%d' "'${cc:1:1}" 2>/dev/null || return 0
  printf "\U$(printf '%08x' $((0x1F1E6 + a - 65)))\U$(printf '%08x' $((0x1F1E6 + b - 65)))" 2>/dev/null || true
}

# Detect server geo info into globals: GEO_CC / GEO_COUNTRY / GEO_CITY / GEO_ISP
detect_geo(){
  GEO_CC=""; GEO_COUNTRY=""; GEO_CITY=""; GEO_ISP=""
  local line; line="$(curl -fsSL4 --max-time 5 'http://ip-api.com/line/?fields=status,countryCode,country,city,isp' 2>/dev/null)"
  [ -n "$line" ] || return 0
  local status; status="$(echo "$line" | sed -n '1p')"
  [ "$status" = "success" ] || return 0
  GEO_CC="$(echo "$line"      | sed -n '2p')"
  GEO_COUNTRY="$(echo "$line" | sed -n '3p')"
  GEO_CITY="$(echo "$line"    | sed -n '4p')"
  GEO_ISP="$(echo "$line"     | sed -n '5p')"
}

# ============================== Dependencies ==============================
install_deps(){
  info "Installing dependencies (curl socat qrencode nginx vnstat openssl)..."
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y >/dev/null
  apt-get install -y curl socat qrencode nginx vnstat openssl ca-certificates >/dev/null
  systemctl enable --now vnstat >/dev/null 2>&1 || true
  # Remove the nginx default site so it doesn't hold port 80 (acme.sh HTTP-01 needs 80 free)
  rm -f /etc/nginx/sites-enabled/default
  systemctl restart nginx >/dev/null 2>&1 || true
}

# ============================== BBR ==============================
enable_bbr(){
  info "Enabling BBR..."
  cat > /etc/sysctl.d/99-bbr.conf <<'EOF'
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
EOF
  sysctl --system >/dev/null 2>&1 || true
  local cc; cc="$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo '?')"
  if [ "$cc" = "bbr" ]; then info "BBR enabled (congestion control: $cc)"; else
    warn "Congestion control is $cc; a newer kernel and reboot may be needed (BBR requires kernel >= 4.9)"; fi
}

# ============================== sing-box install ==============================
install_singbox(){
  local arch tag ver url tmp
  arch="$(arch_tag)"
  info "Fetching latest sing-box version..."
  tag="$(curl -fsSL https://api.github.com/repos/SagerNet/sing-box/releases/latest \
        | grep -oP '"tag_name":\s*"\K[^"]+')"
  [ -n "$tag" ] || { err "Failed to get version tag"; exit 1; }
  ver="${tag#v}"
  url="https://github.com/SagerNet/sing-box/releases/download/${tag}/sing-box-${ver}-linux-${arch}.tar.gz"
  tmp="$(mktemp -d)"
  info "Downloading $tag ..."
  curl -fsSL "$url" -o "$tmp/sb.tar.gz"
  tar -xzf "$tmp/sb.tar.gz" -C "$tmp"
  install -m 0755 "$tmp/sing-box-${ver}-linux-${arch}/sing-box" "$SB_BIN"
  rm -rf "$tmp"
  info "Installed sing-box $("$SB_BIN" version | head -n1)"

  mkdir -p "$SB_DIR" "$SB_CERT_DIR" /var/lib/sing-box
  cat > /etc/systemd/system/sing-box.service <<EOF
[Unit]
Description=sing-box service
After=network.target nss-lookup.target

[Service]
ExecStartPre=$SB_BIN check -c $SB_CONF
ExecStart=$SB_BIN run -c $SB_CONF -D /var/lib/sing-box
Restart=on-failure
RestartSec=3
LimitNOFILE=infinity

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

# ============================== Certificate (acme.sh) ==============================
issue_cert(){
  local domain="$1"
  if [ ! -f "$ACME" ]; then
    info "Installing acme.sh..."
    curl -fsSL https://get.acme.sh | sh >/dev/null
  fi
  # Drop any stored contact email and register the ACME account without one
  # (avoids "invalid contact email" failures from a bad/derived address)
  [ -f "$HOME/.acme.sh/account.conf" ] && sed -i '/^ACCOUNT_EMAIL=/d' "$HOME/.acme.sh/account.conf" 2>/dev/null || true
  "$ACME" --set-default-ca --server letsencrypt >/dev/null
  "$ACME" --register-account >/dev/null 2>&1 || true
  systemctl stop nginx >/dev/null 2>&1 || true   # make sure port 80 is free
  info "Issuing certificate (HTTP-01, port 80 must be free)..."
  if ! "$ACME" --issue -d "$domain" --standalone --keylength 2048 >/dev/null 2>&1; then
    "$ACME" --issue -d "$domain" --standalone --keylength 2048 --force
  fi
  "$ACME" --install-cert -d "$domain" \
    --key-file       "$SB_CERT_DIR/key.pem" \
    --fullchain-file "$SB_CERT_DIR/cert.pem" \
    --reloadcmd      "systemctl restart sing-box && systemctl reload nginx 2>/dev/null || true" >/dev/null
  [ -s "$SB_CERT_DIR/cert.pem" ] || { err "Certificate request failed. Check: domain resolves here, port 80 is free, Cloudflare is grey cloud (DNS only)"; exit 1; }
  info "Certificate ready: $SB_CERT_DIR/cert.pem"
}

# ============================== Write sing-box config ==============================
write_config(){
  cat > "$SB_CONF" <<EOF
{
  "log": { "level": "warn", "timestamp": true },
  "inbounds": [
    {
      "type": "vless",
      "tag": "vless-in",
      "listen": "::",
      "listen_port": ${VLESS_PORT},
      "users": [
        { "uuid": "${UUID}", "flow": "xtls-rprx-vision" }
      ],
      "tls": {
        "enabled": true,
        "server_name": "${REALITY_SNI}",
        "reality": {
          "enabled": true,
          "handshake": { "server": "${REALITY_SNI}", "server_port": 443 },
          "private_key": "${REALITY_PRIVATE_KEY}",
          "short_id": ["${SHORT_ID}"]
        }
      }
    },
    {
      "type": "hysteria2",
      "tag": "hy2-in",
      "listen": "::",
      "listen_port": ${HY2_PORT},
      "users": [
        { "password": "${HY2_PASSWORD}" }
      ],
      "tls": {
        "enabled": true,
        "alpn": ["h3"],
        "certificate_path": "${SB_CERT_DIR}/cert.pem",
        "key_path": "${SB_CERT_DIR}/key.pem"
      }
    }
  ],
  "outbounds": [
    { "type": "direct", "tag": "direct" }
  ],
  "route": { "rules": [], "final": "direct" }
}
EOF
  "$SB_BIN" check -c "$SB_CONF" || { err "Config validation failed"; exit 1; }
}

# ============================== Subscription / nginx ==============================
write_nginx(){
  cat > /etc/nginx/sites-available/sbm-sub.conf <<EOF
server {
    listen ${SUB_PORT} ssl;
    listen [::]:${SUB_PORT} ssl;
    server_name ${DOMAIN};

    ssl_certificate     ${SB_CERT_DIR}/cert.pem;
    ssl_certificate_key ${SB_CERT_DIR}/key.pem;

    root ${SUB_DIR};
    default_type text/plain;
    charset utf-8;
    autoindex off;

    location / { try_files \$uri =404; }
}
EOF
  ln -sf /etc/nginx/sites-available/sbm-sub.conf /etc/nginx/sites-enabled/sbm-sub.conf
  mkdir -p "$SUB_DIR"
  nginx -t >/dev/null 2>&1 && systemctl reload nginx 2>/dev/null || systemctl restart nginx
}

# Generate the two share links into globals VLESS_LINK / HY2_LINK
gen_links(){
  VLESS_LINK="vless://${UUID}@${DOMAIN}:${VLESS_PORT}?encryption=none&flow=xtls-rprx-vision&security=reality&sni=${REALITY_SNI}&fp=chrome&pbk=${REALITY_PUBLIC_KEY}&sid=${SHORT_ID}&type=tcp#${NODE_NAME}-VLESS"
  HY2_LINK="hysteria2://${HY2_PASSWORD}@${DOMAIN}:${HY2_PORT}?sni=${DOMAIN}&alpn=h3#${NODE_NAME}-HY2"
}

write_sub(){
  gen_links
  printf '%s\n%s\n' "$VLESS_LINK" "$HY2_LINK" | base64 -w0 > "${SUB_DIR}/${SUB_TOKEN}"
  chmod 644 "${SUB_DIR}/${SUB_TOKEN}"
}

# ============================== Firewall ==============================
open_firewall(){
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    info "Opening firewall ports (ufw)..."
    ufw allow 80/tcp           >/dev/null 2>&1 || true
    ufw allow "${VLESS_PORT}/tcp" >/dev/null 2>&1 || true
    ufw allow "${HY2_PORT}/udp"   >/dev/null 2>&1 || true
    ufw allow "${SUB_PORT}/tcp"   >/dev/null 2>&1 || true
  fi
}

# ============================== Save / load params ==============================
save_env(){
  cat > "$SB_ENV" <<EOF
DOMAIN="${DOMAIN}"
NODE_NAME="${NODE_NAME}"
VLESS_PORT="${VLESS_PORT}"
HY2_PORT="${HY2_PORT}"
SUB_PORT="${SUB_PORT}"
UUID="${UUID}"
REALITY_SNI="${REALITY_SNI}"
REALITY_PRIVATE_KEY="${REALITY_PRIVATE_KEY}"
REALITY_PUBLIC_KEY="${REALITY_PUBLIC_KEY}"
SHORT_ID="${SHORT_ID}"
HY2_PASSWORD="${HY2_PASSWORD}"
SUB_TOKEN="${SUB_TOKEN}"
EOF
  chmod 600 "$SB_ENV"
}
# shellcheck source=/dev/null
load_env(){ [ -f "$SB_ENV" ] && . "$SB_ENV"; }

# ============================== Output ==============================
show_result(){
  gen_links
  local sub="https://${DOMAIN}:${SUB_PORT}/${SUB_TOKEN}"
  echo
  echo "${BLU}==================== NODE INFO ====================${NC}"
  echo "${GRN}VLESS-REALITY link (${NODE_NAME}-VLESS):${NC}"; echo "$VLESS_LINK"; echo
  qrencode -t ANSIUTF8 "$VLESS_LINK" 2>/dev/null || echo "(qrencode unavailable)"; echo
  echo "${GRN}Hysteria2 link (${NODE_NAME}-HY2):${NC}"; echo "$HY2_LINK"; echo
  qrencode -t ANSIUTF8 "$HY2_LINK" 2>/dev/null || echo "(qrencode unavailable)"; echo
  echo "${GRN}Subscription (Shadowrocket / v2rayNG / NekoBox / Hiddify):${NC}"
  echo "$sub"; echo
  qrencode -t ANSIUTF8 "$sub" 2>/dev/null || echo "(qrencode unavailable)"
  echo "${BLU}==================================================${NC}"
  echo "${YLW}Note: the subscription link contains secrets. Do not share it.${NC}"
}

# ============================== Install flow ==============================
do_install(){
  need_root; check_os

  if [ -f "$SB_ENV" ]; then
    warn "Existing installation detected. Uninstall from the menu before reinstalling."
    return
  fi

  echo "${BLU}===== sing-box installer =====${NC}"

  # Detect geo location to build a friendlier default node name
  info "Detecting server location..."
  detect_geo
  local def_name="MyNode"
  if [ -n "$GEO_CC" ]; then
    local flag; flag="$(cc_flag "$GEO_CC")"
    def_name="${flag}${GEO_CC}-${GEO_CITY// /}"
    info "Location: ${flag} ${GEO_COUNTRY} ${GEO_CITY} | ISP: ${GEO_ISP}"
  else
    warn "Location detection failed (install continues); node name defaults to MyNode"
  fi

  read -rp "Domain (grey-cloud, resolved to this host): " DOMAIN
  # Clean common input mistakes: strip spaces, scheme and any path
  DOMAIN="$(echo "$DOMAIN" | tr -d '[:space:]' | sed -E 's#^https?://##; s#/.*$##')"
  [ -n "$DOMAIN" ] || { err "Domain is required"; exit 1; }
  [[ "$DOMAIN" =~ ^[A-Za-z0-9.-]+$ ]] || { err "Invalid domain: only letters, digits, '.' and '-' are allowed. Re-enter in half-width characters."; exit 1; }
  read -rp "Node name [${def_name}]: " NODE_NAME; NODE_NAME="${NODE_NAME:-$def_name}"
  read -rp "REALITY borrow SNI [www.apple.com]: " REALITY_SNI; REALITY_SNI="${REALITY_SNI:-www.apple.com}"
  read -rp "VLESS TCP port [443]: " VLESS_PORT; VLESS_PORT="${VLESS_PORT:-443}"
  read -rp "Hysteria2 UDP port [443]: " HY2_PORT; HY2_PORT="${HY2_PORT:-443}"
  read -rp "Subscription TCP port [2096]: " SUB_PORT; SUB_PORT="${SUB_PORT:-2096}"
  read -rp "Hysteria2 password [blank = random]: " HY2_PASSWORD

  # Soft resolution check
  local sip rip; rip="$(pubip)"; sip="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk 'NR==1{print $1}')"
  if [ -n "$rip" ] && [ -n "$sip" ] && [ "$rip" != "$sip" ]; then
    warn "Domain resolves to $sip but this host's public IP is $rip — ensure Cloudflare is grey cloud and the A record points here."
    read -rp "Continue anyway? [y/N]: " go; [ "${go,,}" = "y" ] || exit 1
  fi

  install_deps
  enable_bbr
  install_singbox

  # Generate key material
  info "Generating UUID / REALITY keypair / short-id..."
  UUID="$("$SB_BIN" generate uuid)"
  local kp; kp="$("$SB_BIN" generate reality-keypair)"
  REALITY_PRIVATE_KEY="$(echo "$kp" | awk '/PrivateKey/{print $2}')"
  REALITY_PUBLIC_KEY="$(echo "$kp"  | awk '/PublicKey/{print $2}')"
  SHORT_ID="$(openssl rand -hex 8)"
  [ -n "$HY2_PASSWORD" ] || HY2_PASSWORD="$(openssl rand -base64 16 | tr -d '/+=' | cut -c1-20)"
  SUB_TOKEN="$(openssl rand -hex 16)"

  open_firewall
  issue_cert "$DOMAIN"
  write_config
  write_nginx

  systemctl enable sing-box >/dev/null 2>&1
  systemctl restart sing-box

  save_env
  write_sub

  # Install the sbm command: copy local file, or pull from remote when run via curl | bash
  if [ -f "$0" ] && [ "$0" != "bash" ] && [ "$0" != "sh" ]; then
    cp -f "$0" "$SELF" 2>/dev/null || true
  else
    curl -fsSL "$SELF_URL" -o "$SELF" 2>/dev/null || true
  fi
  chmod +x "$SELF" 2>/dev/null || true
  [ -s "$SELF" ] || warn "Failed to install the sbm command; download the script manually to /usr/local/bin/sbm"

  systemctl is-active --quiet sing-box && info "sing-box is running" || { err "sing-box failed to start: journalctl -u sing-box -e"; exit 1; }
  show_result
  echo; info "Run ${GRN}sbm${NC} anytime to open the management menu."
}

# ============================== Menu ==============================
do_traffic(){
  echo "${BLU}—— This month ——${NC}"; vnstat -m 2>/dev/null || true
  echo "${BLU}—— Recent ——${NC}"; vnstat 2>/dev/null || true
  echo "${YLW}Live: vnstat -l   Daily: vnstat -d${NC}"
}

do_update(){
  need_root; install_singbox; systemctl restart sing-box
  systemctl is-active --quiet sing-box && info "Updated and restarted" || err "Restart failed, check logs"
}

do_uninstall(){
  need_root
  read -rp "Confirm uninstall of sing-box and the subscription service? [y/N]: " c; [ "${c,,}" = "y" ] || return
  load_env || true
  systemctl disable --now sing-box >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/sing-box.service; systemctl daemon-reload
  rm -f /etc/nginx/sites-enabled/sbm-sub.conf /etc/nginx/sites-available/sbm-sub.conf
  systemctl reload nginx 2>/dev/null || true
  [ -n "${DOMAIN:-}" ] && "$ACME" --remove -d "$DOMAIN" >/dev/null 2>&1 || true
  rm -rf "$SB_DIR" "$SUB_DIR/${SUB_TOKEN:-__none__}" "$SB_BIN" "$SELF"
  info "Uninstalled (vnstat / BBR / acme.sh kept)."
}

menu(){
  load_env
  while true; do
    echo
    echo "${BLU}========= sing-box management (sbm) =========${NC}"
    echo " 1) Show node links / subscription / QR codes"
    echo " 2) Show traffic stats (vnstat)"
    echo " 3) Restart sing-box"
    echo " 4) Update sing-box core"
    echo " 5) Show status / logs"
    echo " 6) Uninstall"
    echo " 0) Exit"
    read -rp "Choice: " op
    case "$op" in
      1) show_result ;;
      2) do_traffic ;;
      3) systemctl restart sing-box && info "Restarted" ;;
      4) do_update ;;
      5) systemctl status sing-box --no-pager -l | head -n 15; echo; journalctl -u sing-box -n 20 --no-pager ;;
      6) do_uninstall; exit 0 ;;
      0) exit 0 ;;
      *) warn "Invalid choice" ;;
    esac
  done
}

# ============================== Entry ==============================
main(){
  if [ -f "$SB_ENV" ]; then menu; else do_install; fi
}
main "$@"
