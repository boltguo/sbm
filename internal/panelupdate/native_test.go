package panelupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/core"
)

// The launcher runs inside a separate systemd service. The update worker stops
// that service (like restarting sbm-panel), then proves it can still finish.
func TestNativeUpdateLauncher(t *testing.T) {
	if os.Getenv("SBM_UPDATE_NATIVE_LAUNCHER") != "1" {
		t.Skip("native launcher helper")
	}
	m := New(core.ExecCommander{})
	m.StatusPath = os.Getenv("SBM_UPDATE_NATIVE_DIR") + "/status.json"
	m.LockPath = os.Getenv("SBM_UPDATE_NATIVE_DIR") + "/lock"
	m.Script = os.Getenv("SBM_UPDATE_NATIVE_DIR") + "/worker"
	if err := m.Start(context.Background(), "v2.1.1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
}
func TestNativeSystemdUpdateSurvivesPanelStop(t *testing.T) {
	if os.Getenv("SBM_SYSTEMD_TEST") != "1" {
		t.Skip("set SBM_SYSTEMD_TEST=1 on a systemd Linux host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("native test needs root")
	}
	if err := exec.Command("systemctl", "is-active", "--quiet", Unit).Run(); err == nil {
		t.Fatal("an existing update is active; refusing to touch it")
	}
	// Stay outside /tmp for PrivateTmp, and outside /run which may be noexec.
	dir, err := os.MkdirTemp("/var/lib", "sbm-panel-update-qa-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	const caller = "sbm-update-native-caller.service"
	worker := `#!/bin/bash
set -Eeuo pipefail
qa_dir="$(dirname "$0")"
exec 9>"$qa_dir/lock"
flock -w 10 9
printf '{"state":"running","phase":"restarting","targetVersion":"v2.1.1"}\n' > "$qa_dir/status.json"
systemctl stop sbm-update-native-caller.service
sleep 1
printf '{"state":"succeeded","phase":"done","targetVersion":"v2.1.1"}\n' > "$qa_dir/status.json"
`
	if err := os.WriteFile(filepath.Join(dir, "worker"), []byte(worker), 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(dir, "launcher")
	if err := os.WriteFile(exe, data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("systemctl", "stop", caller, Unit).Run() })
	out, err := exec.Command("systemd-run", "--quiet", "--collect", "--unit="+caller, "--property=Type=exec",
		"--property=NoNewPrivileges=yes", "--property=PrivateTmp=yes", "--property=ProtectHome=yes",
		"--property=ProtectKernelTunables=yes", "--property=ProtectKernelModules=yes", "--property=ProtectControlGroups=yes",
		"--property=RestrictRealtime=yes", "--property=LockPersonality=yes", "--property=UMask=0077",
		"--setenv=SBM_UPDATE_NATIVE_LAUNCHER=1", "--setenv=SBM_UPDATE_NATIVE_DIR="+dir, exe, "-test.run=^TestNativeUpdateLauncher$", "-test.timeout=40s").CombinedOutput()
	if err != nil {
		t.Fatalf("start fixture: %s %v", out, err)
	}
	m := New(core.ExecCommander{})
	m.StatusPath = filepath.Join(dir, "status.json")
	m.LockPath = filepath.Join(dir, "lock")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, err := m.Status(context.Background())
		if err == nil && status.State == "succeeded" {
			if exec.Command("systemctl", "is-active", "--quiet", caller).Run() == nil {
				t.Fatal("caller was not stopped")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := exec.Command("journalctl", "-u", caller, "-u", Unit, "-n", "30", "--no-pager").CombinedOutput()
	t.Fatalf("detached update did not survive caller stop: %s", logs)
}
