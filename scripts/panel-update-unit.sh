#!/usr/bin/env bash
# shellcheck disable=SC2317,SC2329 # Host mocks are invoked indirectly; ShellCheck versions use different codes.
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$root_dir/install.sh"
fixture="$(mktemp -d /tmp/sbm-update-unit.XXXXXX)"
trap 'rm -rf "$fixture"' EXIT

# Atomic replacement keeps an already executing Bash script's open inode intact.
printf 'old manager\n' > "$fixture/manager"
printf 'new manager\n' > "$fixture/candidate"
exec 7<"$fixture/manager"
atomic_install "$fixture/candidate" "$fixture/manager"
[[ "$(cat <&7)" == 'old manager' && "$(cat "$fixture/manager")" == 'new manager' && -x "$fixture/manager" ]]
exec 7<&-

# A damaged manager in an otherwise checksum-valid archive must fail before
# either installed file is replaced (and before source/service preflight).
(
  qa_archive_dir="$fixture/archive"; mkdir "$qa_archive_dir"
  qa_binary="$fixture/live-binary"; qa_manager="$fixture/live-manager"
  printf 'old binary\n' > "$qa_binary"; printf 'old manager\n' > "$qa_manager"
  printf '#!/usr/bin/env bash\nprintf "2.1.1\\n"\n' > "$qa_archive_dir/sbm-panel"
  chmod 0755 "$qa_archive_dir/sbm-panel"
  printf 'if true; then\n' > "$qa_archive_dir/sbm"
  qa_asset=sbm-panel_2.1.1_linux_amd64.tar.gz
  tar -C "$qa_archive_dir" -czf "$fixture/$qa_asset" sbm-panel sbm
  (cd "$fixture" && sha256sum "$qa_asset" > checksums.txt)
  definition="$(declare -f install_panel)"
  definition="${definition//SBM_BIN/qa_binary}"; definition="${definition//SBM_CMD/qa_manager}"
  eval "$definition"
  arch_tag() { printf 'amd64\n'; }
  curl() {
    if [[ "$*" == *checksums.txt* ]]; then cp "$fixture/checksums.txt" "${!#}";
    else cp "$fixture/$qa_asset" "${!#}"; fi
  }
  wait_vnstat_source() { echo 'preflight ran before manager validation' >&2; exit 2; }
  atomic_install() { echo 'damaged manager replaced live files' >&2; exit 2; }
  if (install_panel v2.1.1); then echo 'damaged manager was accepted' >&2; exit 1; fi
  [[ "$(cat "$qa_binary")" == 'old binary' && "$(cat "$qa_manager")" == 'old manager' ]]
)

# Type=simple active is not readiness: wait through connection failures, use
# genuine HTTPS against loopback, and accept a deliberately locked management UI.
for result in ready locked timeout; do
 (
  json_string() { printf 'node.example.com\n'; }
  json_number() { printf '2096\n'; }
  json_bool() { if [[ "$result" == locked ]]; then printf 'false\n'; else printf 'true\n'; fi; }
  systemctl() { [[ "$*" == 'is-active --quiet sbm-panel.service' ]]; }
  sleep() { SECONDS=$((SECONDS + 10)); }
  printf '0\n' > "$fixture/curl-count"
  curl() {
    [[ "$*" == *"--noproxy *"* && "$*" == *'--resolve node.example.com:2096:127.0.0.1'* && "$*" == *'https://node.example.com:2096/api/me'* ]] || exit 2
    local calls
    calls="$(cat "$fixture/curl-count")"; calls=$((calls + 1)); printf '%s\n' "$calls" > "$fixture/curl-count"
    if [[ "$result" == timeout ]]; then printf '200'; elif [[ "$result" == locked ]]; then printf '404'; elif ((calls < 3)); then printf '000'; else printf '401'; fi
  }
  if [[ "$result" == timeout ]]; then
    if wait_panel_https; then echo 'non-auth HTTPS falsely healthy' >&2; exit 1; fi
  else
    wait_panel_https
    if [[ "$result" == ready ]]; then [[ "$(cat "$fixture/curl-count")" == 3 ]]; fi
  fi
 )
done

# Finalization only updates/restarts the panel service. Touching the core or
# firewall fails this mock; both the legacy and current internal flags stay safe.
(
  install_deps() { :; }
  wait_vnstat_source() { :; }
  write_panel_service() { printf 'panel unit\n' >> "$fixture/finalize"; }
  wait_panel_https() { printf 'HTTPS ready\n' >> "$fixture/finalize"; }
  systemctl() {
    case "$*" in 'daemon-reload'|'enable sbm-panel.service'|'restart sbm-panel.service') printf '%s\n' "$*" >> "$fixture/finalize" ;; *) echo "unnecessary service action: $*" >&2; return 1 ;; esac
  }
  finish_panel_update
  [[ "$(cat "$fixture/finalize")" == $'panel unit\ndaemon-reload\nenable sbm-panel.service\nrestart sbm-panel.service\nHTTPS ready' ]]
  need_root() { :; }
  finish_panel_update() { printf 'safe finalization\n' >> "$fixture/flags"; }
  repair_runtime() { echo 'full repair called by update' >&2; exit 1; }
  main --finish-panel-update
  main --repair-runtime
  [[ "$(cat "$fixture/flags")" == $'safe finalization\nsafe finalization' ]]
)

# Run the worker in a fresh shell with strict mode, just as the CLI and systemd
# launcher do. Dependency failures stop before installation; startup failures
# restore both files and leave a persisted result for the restarted panel.
for scenario in success dependency-failure startup-failure; do
 qa_dir="$fixture/$scenario"; mkdir "$qa_dir"
 printf 'old binary\n' > "$qa_dir/binary"
 printf 'old manager\n' > "$qa_dir/manager"
 cat > "$qa_dir/worker" <<WORKER
#!/usr/bin/env bash
source "$root_dir/install.sh"
qa_dir="$qa_dir"
scenario="$scenario"
WORKER
 cat >> "$qa_dir/worker" <<'WORKER'
qa_binary="$qa_dir/binary"; qa_manager="$qa_dir/manager"; qa_status="$qa_dir/status.json"; qa_lock="$qa_dir/lock"
for fn in perform_panel_update panel_update_exit run_panel_update write_update_status; do
 definition="$(declare -f "$fn")"
 definition="${definition//SBM_BIN/qa_binary}"; definition="${definition//SBM_CMD/qa_manager}"
 definition="${definition//STATE_DIR/qa_dir}"; definition="${definition//UPDATE_STATUS/qa_status}"; definition="${definition//UPDATE_LOCK/qa_lock}"
 eval "$definition"
done
need_root() { :; }
flock() { :; } # Native flock concurrency is covered by Go tests.
panel_update_target_version() { printf 'v2.1.1\n'; }
assert_panel_config_supported() { :; }
install_deps() { [[ "$scenario" != dependency-failure ]]; }
configure_vnstat() { :; }
install_panel() {
 cp "$qa_binary" "$qa_binary.bak"; cp "$qa_manager" "$qa_manager.bak"
 printf 'new binary\n' > "$qa_dir/candidate"
 atomic_install "$qa_dir/candidate" "$qa_binary"
 printf '#!/usr/bin/env bash\n[[ "$1" == --finish-panel-update ]] || exit 2\n' > "$qa_dir/new-manager"
 if [[ "$scenario" == startup-failure ]]; then printf 'exit 1\n' >> "$qa_dir/new-manager"; fi
 atomic_install "$qa_dir/new-manager" "$qa_manager"
}
service_healthy_after_restart() { [[ "$1" == sbm-panel.service ]]; }
run_panel_update v2.1.1
WORKER
 if [[ "$scenario" == success ]]; then
   bash "$qa_dir/worker"
   [[ "$(cat "$qa_dir/binary")" == 'new binary' ]]
   expected=succeeded
 else
   if bash "$qa_dir/worker"; then echo "failure falsely succeeded: $scenario" >&2; exit 1; fi
   [[ "$(cat "$qa_dir/binary")" == 'old binary' && "$(cat "$qa_dir/manager")" == 'old manager' ]]
   expected=failed
 fi
 python3 - "$qa_dir/status.json" "$expected" "$scenario" <<'PY'
import json,sys
record=json.load(open(sys.argv[1]))
assert record['state']==sys.argv[2],record
assert record['rolledBack']==(sys.argv[3]=='startup-failure'),record
PY
done
printf 'Panel update regression checks passed.\n'
