#!/usr/bin/env bash
# Run only the isolated integration tests; never changes host INPUT/OUTPUT.
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d /tmp/sbm-egress-test.XXXXXX)"
trap 'rm -rf "$test_dir"' EXIT
cd "$root_dir"
go test -c -o "$test_dir/traffic.test" ./internal/traffic
if [[ "$(id -u)" == 0 ]]; then
  unshare --net env SBM_TEST_IPTABLES=1 "$test_dir/traffic.test" -test.run '^TestLinuxAccountingIntegration$' -test.v
else
  sudo unshare --net env SBM_TEST_IPTABLES=1 "$test_dir/traffic.test" -test.run '^TestLinuxAccountingIntegration$' -test.v
fi

if [[ -n "${SBM_TEST_SING_BOX:-}" ]]; then
  go test -c -o "$test_dir/server.test" ./internal/server
  if [[ "$(id -u)" == 0 ]]; then
    unshare --net env SBM_TEST_RUNTIME=1 SBM_TEST_SING_BOX="$SBM_TEST_SING_BOX" "$test_dir/server.test" -test.run '^TestEgressRuntimeIntegration$' -test.v
  else
    sudo unshare --net env SBM_TEST_RUNTIME=1 SBM_TEST_SING_BOX="$SBM_TEST_SING_BOX" "$test_dir/server.test" -test.run '^TestEgressRuntimeIntegration$' -test.v
  fi
fi
