#!/usr/bin/env bash
set -Eeuo pipefail
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d /tmp/sbm-singbox-test.XXXXXX)"
trap 'rm -rf "$test_dir"' EXIT
# Verify the official release asset's SHA-256 before running its binary.
python3 - "$test_dir" <<'PY'
import hashlib, json, os, pathlib, platform, subprocess, tarfile, urllib.request, sys
out = pathlib.Path(sys.argv[1])
os_name = {'Linux': 'linux', 'Darwin': 'darwin'}[platform.system()]
arch = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}[platform.machine()]
name = f'sing-box-1.13.14-{os_name}-{arch}'
request = urllib.request.Request('https://api.github.com/repos/SagerNet/sing-box/releases/tags/v1.13.14', headers={'User-Agent': 'SBM-integration'})
if os.environ.get('GITHUB_TOKEN'):
    request.add_header('Authorization', 'Bearer ' + os.environ['GITHUB_TOKEN'])
with urllib.request.urlopen(request, timeout=20) as response:
    release = json.load(response)
asset = next(a for a in release['assets'] if a['name'] == name + '.tar.gz')
archive = out / 'sing-box.tar.gz'
subprocess.run(['curl', '-fsSL', '--retry', '3', '--retry-all-errors', '--max-time', '90', asset['browser_download_url'], '-o', str(archive)], check=True)
data = archive.read_bytes()
if asset.get('digest') != 'sha256:' + hashlib.sha256(data).hexdigest():
    raise SystemExit('Official sing-box asset digest did not verify')
with tarfile.open(archive) as source:
    source.extractall(out, filter='data')
(out / 'binary-path').write_text(str(out / name / 'sing-box'))
PY
binary="$(cat "$test_dir/binary-path")"
cd "$root_dir"
SBM_TEST_SING_BOX="$binary" go test ./internal/core -run '^TestSingBoxEgressIntegration$' -v
if [[ "$(uname -s)" == Linux ]]; then
  SBM_TEST_SING_BOX="$binary" bash scripts/egress-linux-test.sh
fi
