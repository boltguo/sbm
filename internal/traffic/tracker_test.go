package traffic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
)

type configSource struct{ cfg model.Config }

func (s *configSource) Get() model.Config { return s.cfg }

type fakeCore struct{ starts, stops int }

func (f *fakeCore) Start(context.Context) error { f.starts++; return nil }
func (f *fakeCore) Stop(context.Context) error  { f.stops++; return nil }

func stateAt(now time.Time) model.State {
	return model.State{Version: 1, PeriodStartedAt: now, UpdatedAt: now}
}

func TestNormalDelta(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	state := stateAt(now)
	state.LastCoreUpload = 100
	state.LastCoreDownload = 200
	tracker := NewForTest(state, &configSource{}, nil, func() time.Time { return now })
	if _, err := tracker.ApplySample(context.Background(), 150, 260); err != nil {
		t.Fatal(err)
	}
	got := tracker.State()
	if got.Upload != 50 || got.Download != 60 {
		t.Fatalf("got upload=%d download=%d", got.Upload, got.Download)
	}
}

func TestCoreRestartCountersReset(t *testing.T) {
	now := time.Now()
	state := stateAt(now)
	state.Upload = 1000
	state.Download = 2000
	state.LastCoreUpload = 900
	state.LastCoreDownload = 1800
	tracker := NewForTest(state, &configSource{}, nil, time.Now)
	_, _ = tracker.ApplySample(context.Background(), 20, 30)
	got := tracker.State()
	if got.Upload != 1020 || got.Download != 2030 {
		t.Fatalf("restart delta was wrong: %+v", got)
	}
}

func TestPanelRestartDoesNotDoubleCount(t *testing.T) {
	now := time.Now()
	state := stateAt(now)
	state.Upload = 500
	state.Download = 800
	state.LastCoreUpload = 200
	state.LastCoreDownload = 400
	restartedPanel := NewForTest(state, &configSource{}, nil, time.Now)
	_, _ = restartedPanel.ApplySample(context.Background(), 200, 400)
	got := restartedPanel.State()
	if got.Upload != 500 || got.Download != 800 {
		t.Fatalf("panel restart duplicated counters: %+v", got)
	}
}

func TestManualResetKeepsCoreBaseline(t *testing.T) {
	now := time.Now()
	state := stateAt(now)
	state.Upload = 500
	state.Download = 800
	state.LastCoreUpload = 200
	state.LastCoreDownload = 400
	tracker := NewForTest(state, &configSource{}, nil, time.Now)
	if err := tracker.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = tracker.ApplySample(context.Background(), 225, 450)
	got := tracker.State()
	if got.Upload != 25 || got.Download != 50 {
		t.Fatalf("reset re-counted old bytes: %+v", got)
	}
}

func TestMonthlyResetAndNextTime(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 1, 28, 23, 30, 0, 0, location)
	next, err := NextMonthlyReset(now, model.ResetConfig{Mode: "monthly", Day: 28, Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 2, 28, 0, 0, 0, 0, location)
	if !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
	source := &configSource{cfg: model.Config{Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}}}
	state := stateAt(now)
	state.Upload = 42
	state.NextResetAt = now.Add(-time.Minute)
	tracker := NewForTest(state, source, nil, func() time.Time { return now })
	if err := tracker.CheckScheduledReset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().Upload != 0 {
		t.Fatal("scheduled reset did not clear traffic")
	}
}

func TestQuotaExceededUnlimitedAndRecovery(t *testing.T) {
	now := time.Now()
	source := &configSource{cfg: model.Config{TotalBytes: 100}}
	core := &fakeCore{}
	tracker := NewForTest(stateAt(now), source, core, time.Now)
	exceeded, err := tracker.ApplySample(context.Background(), 60, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !exceeded || !tracker.State().QuotaExceeded || core.stops != 1 {
		t.Fatalf("quota not enforced: state=%+v core=%+v", tracker.State(), core)
	}
	source.cfg.TotalBytes = 0
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded || core.starts != 1 || tracker.State().Total() != 100 {
		t.Fatalf("quota recovery wrong: state=%+v core=%+v", tracker.State(), core)
	}
	unlimited := NewForTest(stateAt(now), &configSource{cfg: model.Config{TotalBytes: 0}}, &fakeCore{}, time.Now)
	exceeded, _ = unlimited.ApplySample(context.Background(), 1<<40, 1<<40)
	if exceeded || unlimited.State().QuotaExceeded {
		t.Fatal("unlimited quota was exceeded")
	}
}

func TestResetRestartsExceededCore(t *testing.T) {
	state := stateAt(time.Now())
	state.QuotaExceeded = true
	state.Upload = 100
	core := &fakeCore{}
	tracker := NewForTest(state, &configSource{}, core, time.Now)
	if err := tracker.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if core.starts != 1 || tracker.State().QuotaExceeded {
		t.Fatalf("reset did not recover core: %+v", core)
	}
}

func TestCorruptStateAndBackupRefusesSilentReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, &configSource{}, nil, time.Now); err == nil {
		t.Fatal("corrupt existing state was silently reset")
	}
}
