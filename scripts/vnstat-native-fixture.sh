#!/usr/bin/env bash
# Run only under sudo unshare --mount --net. All processes/files belong to this fixture.
set -Eeuo pipefail
[[ "$(readlink /proc/self/ns/net)" != "$(readlink /proc/1/ns/net)" ]] || { echo 'requires a disposable network namespace' >&2; exit 1; }
[[ "$(readlink /proc/self/ns/mnt)" != "$(readlink /proc/1/ns/mnt)" ]] || { echo 'requires a disposable mount namespace' >&2; exit 1; }
fixture="$1"
[[ "$fixture" == /tmp/sbm-vnstat-native.* ]] || { echo 'invalid fixture path' >&2; exit 1; }
chown root:root "$fixture"
export PATH="$fixture/extracted/usr/bin:$PATH"
mkdir -p "$fixture/db"
rm -f "$fixture/db/vnstat.db" "$fixture/db/vnstat.db-shm" "$fixture/db/vnstat.db-wal"
mount --make-rprivate /
cat > "$fixture/vnstat.conf" <<CONFIG
DatabaseDir "$fixture/db"
DaemonUser ""
DaemonGroup ""
UseUTC 1
PollInterval 2
UpdateInterval 2
SaveInterval 1
DailyDays 90
HourlyDays 40
5MinuteHours 48
TrafficlessEntries 1
UseLogging 0
CONFIG
ip link set lo up
ip link add stat0 type veth peer name stat1
ip addr add 10.244.1.1/24 dev stat0
ip addr add 10.244.1.2/24 dev stat1
ip link set stat0 up
ip link set stat1 up
ip link add stat0b type veth peer name stat2
ip addr add 10.244.2.1/24 dev stat0b
ip addr add 10.244.2.2/24 dev stat2
ip link set stat0b up
ip link set stat2 up
"$fixture/extracted/usr/sbin/vnstatd" -n --config "$fixture/vnstat.conf" > "$fixture/vnstatd.log" 2>&1 &
daemon=$!
cleanup(){ [[ -z "$daemon" ]] || kill "$daemon" 2>/dev/null || true; wait 2>/dev/null || true; }
trap cleanup EXIT
sleep 3
# Send Ethernet frames over each real veth pair; both directions have distinct
# byte totals. No host routes or production interfaces participate.
python3 - <<'PYTHON'
import socket,subprocess,json,time
links={x['ifname']:bytes.fromhex(x['address'].replace(':','')) for x in json.loads(subprocess.check_output(['ip','-j','link','show']))}
for left,right,count in [('stat0','stat1',1000),('stat0b','stat2',2000)]:
 for src,dst in [(left,right),(right,left)]:
  s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW);s.bind((src,0))
  frame=links[dst]+links[src]+b'\x88\xb5'+b'x'*1024
  for i in range(count):
   s.send(frame);time.sleep(.0005)
  s.close()
time.sleep(3)
PYTHON
# Graceful termination writes daemon cache immediately; no minute-long wait.
kill -TERM "$daemon"
wait "$daemon"
daemon=""
export SBM_VNSTAT_TEST_CONFIG="$fixture/vnstat.conf"
"$fixture/nettraffic.test" -test.run '^TestNativeVnStat$' -test.v
