#!/usr/bin/env bash
# Source this helper from install.sh. The optional path is used by unit tests.
configure_vnstat() {
  local config="${1:-/etc/vnstat.conf}" staging
  [[ -f "$config" ]] || { warn "缺少 vnStat 配置：$config"; return 1; }
  staging="$(mktemp "${config}.sbm.XXXXXX")" || return 1
  # Keep longer / unlimited retention. Never delete or reset the source DB.
  awk '
    BEGIN {want["UseUTC"]=1;want["SaveInterval"]=1;want["UpdateInterval"]=10;want["DailyDays"]=90;want["HourlyDays"]=40;want["5MinuteHours"]=48;want["TrafficlessEntries"]=1}
    $1 in want {
      key=$1;value=want[key];old=$2;gsub(/"/, "", old)
      if (key=="DailyDays" || key=="HourlyDays" || key=="5MinuteHours") {
        if (old==-1 || old+0>value) value=old
      }
      if (!seen[key]++) print key " " value
      next
    }
    {print}
    END {for(key in want) if(!seen[key]) print key " " want[key]}
  ' "$config" > "$staging" || { rm -f "$staging"; return 1; }
  if ! cmp -s "$config" "$staging"; then
    [[ -f "${config}.before-sbm" ]] || cp -p "$config" "${config}.before-sbm" || { rm -f "$staging"; return 1; }
    # Write through the existing file to preserve ownership and permissions.
    cat "$staging" > "$config" || { rm -f "$staging"; return 1; }
  fi
  rm -f "$staging"
  systemctl enable --now vnstat.service >/dev/null 2>&1 || return 1
  systemctl restart vnstat.service || return 1
}
