package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/store"
)

type configSource struct{ cfg model.Config }

func (s *configSource) Get() model.Config {
	cfg := s.cfg
	if cfg.TrafficQuota.BillingMode == "" {
		cfg.TrafficQuota = model.DefaultConfig().TrafficQuota
	}
	return cfg
}

func configWithLimit(limit int64) model.Config {
	cfg := model.DefaultConfig()
	cfg.TrafficQuota.AmountGB = float64(limit) / 1_000_000_000
	cfg.TrafficQuota.HeadroomPercent = 0
	return cfg
}

type fakeCore struct {
	running                     bool
	starts, stops               int
	startErr, stopErr, stateErr error
}

func (f *fakeCore) Active(context.Context) (bool, error) { return f.running, f.stateErr }
func (f *fakeCore) Start(context.Context) error {
	f.starts++
	if f.startErr != nil {
		return f.startErr
	}
	f.running = true
	return nil
}
func (f *fakeCore) Stop(context.Context) error {
	f.stops++
	if f.stopErr != nil {
		return f.stopErr
	}
	f.running = false
	return nil
}

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

func TestCoreRestartUsesBothCountersAsOneGeneration(t *testing.T) {
	tests := []struct {
		name                           string
		currentUpload, currentDownload int64
	}{
		{name: "download counter moved backwards", currentUpload: 200, currentDownload: 10},
		{name: "upload counter moved backwards", currentUpload: 10, currentDownload: 2_000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := stateAt(time.Now())
			state.Upload, state.Download = 1_000, 2_000
			state.LastCoreUpload, state.LastCoreDownload = 100, 1_000
			tracker := NewForTest(state, &configSource{}, nil, time.Now)
			if _, err := tracker.ApplySample(context.Background(), test.currentUpload, test.currentDownload); err != nil {
				t.Fatal(err)
			}
			got := tracker.State()
			if got.Upload != 1_000+test.currentUpload || got.Download != 2_000+test.currentDownload {
				t.Fatalf("restart generation split across directions: %+v", got)
			}
		})
	}
}

func TestCoreGenerationDetectsRestartAfterBothCountersExceedBaseline(t *testing.T) {
	state := stateAt(time.Now())
	state.Upload, state.Download = 1_000, 2_000
	state.LastCoreUpload, state.LastCoreDownload = 100, 150
	state.CoreGeneration = "old-generation"
	tracker := NewForTest(state, &configSource{}, nil, time.Now)
	if _, err := tracker.applySample(context.Background(), 200, 300, "new-generation"); err != nil {
		t.Fatal(err)
	}
	got := tracker.State()
	if got.Upload != 1_200 || got.Download != 2_300 || got.CoreGeneration != "new-generation" {
		t.Fatalf("generation restart was not counted as a new baseline: %+v", got)
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

// Two samplers exist in production: the poller in Run and captureTraffic on the
// HTTP mutation paths. If both could read the core concurrently, the older
// reading applied last would look like a counter reset and add the whole
// counter again. Sample must serialise them.
func TestConcurrentSamplesNeverDoubleCount(t *testing.T) {
	state := stateAt(time.Now())
	const base = int64(500 << 30)
	state.Upload = base
	state.LastCoreUpload = base
	tracker := NewForTest(state, &configSource{}, nil, time.Now)

	var counter atomic.Int64
	counter.Store(base)
	// Each read advances the core counter and takes a varying amount of time,
	// so responses would come back out of order without serialisation.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := counter.Add(1_000)
		time.Sleep(time.Duration(value%5) * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]int64{"uploadTotal": value, "downloadTotal": 0})
	}))
	defer server.Close()
	client := ClashClient{URL: server.URL, Client: server.Client()}

	const samplers = 8
	var wg sync.WaitGroup
	for range samplers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tracker.Sample(context.Background(), client); err != nil {
				t.Errorf("sample failed: %v", err)
			}
		}()
	}
	wg.Wait()

	want := counter.Load()
	if got := tracker.State().Upload; got != want {
		t.Fatalf("concurrent samples miscounted: got=%d want=%d (drift %+d bytes)", got, want, got-want)
	}
	if got := tracker.State().LastCoreUpload; got != want {
		t.Fatalf("baseline drifted: got=%d want=%d", got, want)
	}
}

func TestSampleReportsUnavailableCoreDistinctly(t *testing.T) {
	tracker := NewForTest(stateAt(time.Now()), &configSource{}, nil, time.Now)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := tracker.Sample(context.Background(), ClashClient{URL: server.URL, Client: server.Client()})
	if !errors.Is(err, ErrSampleUnavailable) {
		t.Fatalf("got %v, want ErrSampleUnavailable", err)
	}
	if tracker.State().Upload != 0 {
		t.Fatal("a failed read changed the totals")
	}
}

func TestSampleHealthFailureAndRecovery(t *testing.T) {
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewForTest(stateAt(now), &configSource{}, nil, func() time.Time { return now })
	if got := tracker.SampleHealth(); got.Status != SampleStatusWaiting {
		t.Fatalf("initial health=%+v", got)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer failing.Close()
	_, _ = tracker.Sample(context.Background(), ClashClient{URL: failing.URL, Client: failing.Client()})
	firstFailure := tracker.SampleHealth()
	if firstFailure.Status != SampleStatusInterrupted || !firstFailure.FailureSince.Equal(now) {
		t.Fatalf("failure health=%+v", firstFailure)
	}
	now = now.Add(30 * time.Second)
	_, _ = tracker.Sample(context.Background(), ClashClient{URL: failing.URL, Client: failing.Client()})
	if got := tracker.SampleHealth(); !got.FailureSince.Equal(firstFailure.FailureSince) {
		t.Fatalf("repeated failure reset failureSince: %+v", got)
	}
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int64{"uploadTotal": 4, "downloadTotal": 5})
	}))
	defer healthy.Close()
	if _, err := tracker.Sample(context.Background(), ClashClient{URL: healthy.URL, Client: healthy.Client()}); err != nil {
		t.Fatal(err)
	}
	if got := tracker.SampleHealth(); got.Status != SampleStatusHealthy || !got.LastSuccessAt.Equal(now) || !got.FailureSince.IsZero() {
		t.Fatalf("recovered health=%+v", got)
	}
}

func TestSampleResetsBeforeApplyingBoundaryReading(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}
	state := stateAt(now.Add(-30 * 24 * time.Hour))
	state.Upload, state.Download = 10_000, 20_000
	state.LastCoreUpload, state.LastCoreDownload = 100, 200
	state.NextResetAt = now.Add(-time.Second)
	tracker := NewForTest(state, &configSource{cfg: cfg}, nil, func() time.Time { return now })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int64{"uploadTotal": 125, "downloadTotal": 225})
	}))
	defer server.Close()
	if _, err := tracker.Sample(context.Background(), ClashClient{URL: server.URL, Client: server.Client()}); err != nil {
		t.Fatal(err)
	}
	got := tracker.State()
	if got.Upload != 25 || got.Download != 25 || !got.PeriodStartedAt.Equal(now) {
		t.Fatalf("boundary sample was not assigned to the new period: %+v", got)
	}
}

func TestSampleSkipsReadingWhenScheduledResetFails(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "Invalid/Zone"}
	state := stateAt(now.Add(-30 * 24 * time.Hour))
	state.NextResetAt = now.Add(-time.Second)
	tracker := NewForTest(state, &configSource{cfg: cfg}, nil, func() time.Time { return now })
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]int64{"uploadTotal": 1, "downloadTotal": 1})
	}))
	defer server.Close()
	if _, err := tracker.Sample(context.Background(), ClashClient{URL: server.URL, Client: server.Client()}); err == nil {
		t.Fatal("expected reset error")
	}
	if requests.Load() != 0 || tracker.SampleHealth().Status != SampleStatusInterrupted {
		t.Fatalf("sample ran despite reset failure: requests=%d health=%+v", requests.Load(), tracker.SampleHealth())
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

func TestInvalidScheduleDoesNotEraseCurrentResetTime(t *testing.T) {
	now := time.Now()
	existing := now.Add(24 * time.Hour)
	state := stateAt(now)
	state.NextResetAt = existing
	source := &configSource{cfg: model.Config{Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "Not/A-Timezone"}}}
	tracker := NewForTest(state, source, nil, func() time.Time { return now })
	if err := tracker.UpdateSchedule(); err == nil {
		t.Fatal("expected invalid timezone error")
	}
	if got := tracker.State().NextResetAt; !got.Equal(existing) {
		t.Fatalf("invalid schedule erased the current reset time: got=%v want=%v", got, existing)
	}
}

func TestQuotaExceededUnlimitedAndRecovery(t *testing.T) {
	now := time.Now()
	source := &configSource{cfg: configWithLimit(100)}
	core := &fakeCore{running: true}
	tracker := NewForTest(stateAt(now), source, core, time.Now)
	exceeded, err := tracker.ApplySample(context.Background(), 60, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !exceeded || !tracker.State().QuotaExceeded || core.stops != 1 {
		t.Fatalf("quota not enforced: state=%+v core=%+v", tracker.State(), core)
	}
	source.cfg.TrafficQuota.AmountGB = 0
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded || core.starts != 1 || tracker.State().Total() != 100 {
		t.Fatalf("quota recovery wrong: state=%+v core=%+v", tracker.State(), core)
	}
	unlimited := NewForTest(stateAt(now), &configSource{cfg: configWithLimit(0)}, &fakeCore{}, time.Now)
	exceeded, _ = unlimited.ApplySample(context.Background(), 1<<40, 1<<40)
	if exceeded || unlimited.State().QuotaExceeded {
		t.Fatal("unlimited quota was exceeded")
	}
}

func TestQuotaEnforcementRetriesAfterStopFailure(t *testing.T) {
	source := &configSource{cfg: configWithLimit(100)}
	core := &fakeCore{running: true, stopErr: errors.New("systemctl failed")}
	tracker := NewForTest(stateAt(time.Now()), source, core, time.Now)
	if exceeded, err := tracker.ApplySample(context.Background(), 100, 0); !exceeded || err == nil {
		t.Fatalf("first sample should exceed quota and report stop failure: exceeded=%v err=%v", exceeded, err)
	}
	if !tracker.State().QuotaExceeded || core.stops != 1 || !core.running {
		t.Fatalf("unexpected state after failed stop: state=%+v core=%+v", tracker.State(), core)
	}
	core.stopErr = nil
	if exceeded, err := tracker.ApplySample(context.Background(), 101, 0); exceeded || err != nil {
		t.Fatalf("retry sample failed: exceeded=%v err=%v", exceeded, err)
	}
	if core.stops != 2 || core.running {
		t.Fatalf("stop was not retried: %+v", core)
	}
}

func TestQuotaEnforcementContinuesWhenPersistenceFails(t *testing.T) {
	source := &configSource{cfg: configWithLimit(100)}
	core := &fakeCore{running: true}
	tracker := NewForTest(stateAt(time.Now()), source, core, time.Now)
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	tracker.file = store.NewJSONFile[model.State](filepath.Join(blocked, "state.json"))
	if exceeded, err := tracker.ApplySample(context.Background(), 100, 0); !exceeded || err == nil {
		t.Fatalf("expected quota transition and persistence error: exceeded=%v err=%v", exceeded, err)
	}
	if core.stops != 1 || core.running || !tracker.State().QuotaExceeded {
		t.Fatalf("core was not stopped after persistence failure: state=%+v core=%+v", tracker.State(), core)
	}
}

func TestReconcileQuotaConvergesWithoutStateTransition(t *testing.T) {
	source := &configSource{cfg: configWithLimit(100)}
	state := stateAt(time.Now())
	state.Upload = 100
	state.QuotaExceeded = true
	core := &fakeCore{running: true}
	tracker := NewForTest(state, source, core, time.Now)
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if core.stops != 1 || core.running {
		t.Fatalf("persisted quota state did not stop the core: %+v", core)
	}
}

func TestQuotaRecoveryRetriesAfterStartFailure(t *testing.T) {
	state := stateAt(time.Now())
	state.QuotaExceeded = true
	state.Upload = 100
	core := &fakeCore{startErr: errors.New("systemctl failed")}
	tracker := NewForTest(state, &configSource{}, core, time.Now)
	if err := tracker.Reset(context.Background()); err == nil {
		t.Fatal("expected start failure")
	}
	if tracker.State().QuotaExceeded || core.starts != 1 || core.running {
		t.Fatalf("unexpected state after failed start: state=%+v core=%+v", tracker.State(), core)
	}
	core.startErr = nil
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if core.starts != 2 || !core.running {
		t.Fatalf("start was not retried: %+v", core)
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

// A systemd state that cannot be read must not be treated as "already stopped".
// Doing so would skip the stop while the core keeps serving traffic past the
// quota, with the panel reporting the limit as enforced.
func TestUnreadableCoreStateStillEnforcesQuota(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	state := stateAt(now)
	state.Upload = 200
	state.QuotaExceeded = true
	core := &fakeCore{running: true, stateErr: errors.New("Failed to connect to bus")}
	tracker := NewForTest(state, &configSource{cfg: configWithLimit(100)}, core, func() time.Time { return now })
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if core.stops != 1 {
		t.Fatalf("stops = %d, want 1", core.stops)
	}
}

// With nothing to enforce there is no reason to touch the core, so an
// unreadable state is only reported.
func TestUnreadableCoreStateWithoutQuotaLeavesCoreAlone(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	core := &fakeCore{running: true, stateErr: errors.New("Failed to connect to bus")}
	tracker := NewForTest(stateAt(now), &configSource{cfg: configWithLimit(100)}, core, func() time.Time { return now })
	if err := tracker.ReconcileQuota(context.Background()); err == nil {
		t.Fatal("an unreadable state should be reported")
	}
	if core.stops != 0 || core.starts != 0 {
		t.Fatalf("stops = %d starts = %d, want the core untouched", core.stops, core.starts)
	}
}
