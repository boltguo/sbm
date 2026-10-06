package panelupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/store"
)

type fakeCommands struct {
	active   bool
	startErr error
	calls    [][]string
	onStart  func()
}

func (f *fakeCommands) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if name == "systemctl" {
		if f.active {
			return nil, nil
		}
		return nil, errors.New("inactive")
	}
	if f.onStart != nil {
		f.onStart()
	}
	return nil, f.startErr
}
func fixture(t *testing.T) (*Manager, *fakeCommands) {
	t.Helper()
	dir := t.TempDir()
	c := &fakeCommands{}
	return &Manager{Commands: c, StatusPath: filepath.Join(dir, "status.json"), LockPath: filepath.Join(dir, "lock"), Script: "/usr/local/bin/sbm"}, c
}
func TestStartDetachedAndSerializesWebAndCLI(t *testing.T) {
	m, c := fixture(t)
	c.onStart = func() {
		another := *m
		if err := another.Start(context.Background(), "v2.1.2"); !errors.Is(err, ErrBusy) {
			t.Fatalf("second launcher: %v", err)
		}
		status, err := m.Status(context.Background())
		if err != nil || status.State != "running" || status.TargetVersion != "v2.1.1" {
			t.Fatalf("launch status: %+v %v", status, err)
		}
	}
	if err := m.Start(context.Background(), "v2.1.1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemd-run", "--quiet", "--collect", "--no-ask-password", "--unit=" + Unit, "--property=Type=exec", "--property=UMask=0077", "/bin/bash", "/usr/local/bin/sbm", "--update-panel", "v2.1.1"}
	if len(c.calls) != 2 || !reflect.DeepEqual(c.calls[1], want) {
		t.Fatalf("unsafe launch: %v", c.calls)
	}
	info, err := os.Stat(m.StatusPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("status permissions: %v %v", info, err)
	}
}
func TestStartRejectsCommandsDowngradesAndActiveUnit(t *testing.T) {
	for _, tag := range []string{"v2.1.1;reboot", "$(reboot)", "--repair-runtime", "v2.0.2", "v3.0.0", "v2.1.1-beta", "v2.1.1\n"} {
		m, c := fixture(t)
		if err := m.Start(context.Background(), tag); err == nil || len(c.calls) != 0 {
			t.Fatalf("accepted %q: %v", tag, err)
		}
	}
	m, c := fixture(t)
	c.active = true
	if err := m.Start(context.Background(), "v2.1.1"); !errors.Is(err, ErrBusy) || len(c.calls) != 1 {
		t.Fatalf("active unit: %v %v", err, c.calls)
	}
}
func TestLaunchFailureAndLostLauncherResponse(t *testing.T) {
	m, c := fixture(t)
	c.startErr = errors.New("cannot launch")
	if err := m.Start(context.Background(), "v2.1.1"); err == nil {
		t.Fatal("launch falsely succeeded")
	}
	status, err := m.Status(context.Background())
	if err != nil || status.State != "failed" || status.Phase != "launch" {
		t.Fatalf("failed status: %+v %v", status, err)
	}
	m, c = fixture(t)
	c.startErr = context.DeadlineExceeded
	c.onStart = func() { c.active = true }
	if err := m.Start(context.Background(), "v2.1.1"); err != nil {
		t.Fatal(err)
	}
	status, err = m.Status(context.Background())
	if err != nil || status.State != "running" {
		t.Fatalf("lost response overwrote active job: %+v %v", status, err)
	}
}
func TestStatusSurvivesPanelRestartAndDetectsInterruptedWorker(t *testing.T) {
	m, c := fixture(t)
	record := Status{State: "running", Phase: "restarting", TargetVersion: "v2.1.1", UpdatedAt: time.Now().UTC()}
	if err := store.NewJSONFile[Status](m.StatusPath).SaveWithoutBackup(record); err != nil {
		t.Fatal(err)
	}
	lock, err := m.lock()
	if err != nil {
		t.Fatal(err)
	}
	restarted := *m
	got, err := restarted.Status(context.Background())
	if err != nil || got.State != "running" || len(c.calls) != 0 {
		t.Fatalf("lost independent worker: %+v %v", got, err)
	}
	unlock(lock)
	got, err = restarted.Status(context.Background())
	if err != nil || got.State != "failed" || got.Phase != "interrupted" {
		t.Fatalf("stale spinner: %+v %v", got, err)
	}
	record.State = "succeeded"
	record.Phase = "done"
	if err := store.NewJSONFile[Status](m.StatusPath).SaveWithoutBackup(record); err != nil {
		t.Fatal(err)
	}
	got, err = restarted.Status(context.Background())
	if err != nil || got.State != "succeeded" {
		t.Fatalf("lost result: %+v %v", got, err)
	}
}
