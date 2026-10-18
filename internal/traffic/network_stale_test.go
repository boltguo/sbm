package traffic

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

const testGB = 1_000_000_000

func newStaleTest(t *testing.T, now *time.Time, reset model.ResetConfig) (*Tracker, *networkFake, *fakeCore) {
	t.Helper()
	cfg := model.DefaultConfig()
	cfg.VnStatInterface = "ens5"
	cfg.Reset = reset
	cfg.TrafficQuota = model.TrafficQuotaConfig{Amount: 1, Unit: model.TrafficUnitGB, BillingMode: model.TrafficBillingSingle, HeadroomPercent: 10}
	dir := t.TempDir()
	core := &fakeCore{running: true}
	tracker, err := OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), &configSource{cfg: cfg}, core, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracker.Close() })
	snapshot := nettraffic.Snapshot{Interface: "ens5", CreatedAt: now.Add(-time.Hour), UpdatedAt: *now, RX: 1000, TX: 1000}
	reader := &networkFake{snapshots: map[string]nettraffic.Snapshot{"entry": snapshot}, fail: map[string]bool{}}
	tracker.NetworkReader = reader
	return tracker, reader, core
}

func TestStaleVnStatStillStopsCoreFromProxyCounters(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	tracker, _, core := newStaleTest(t, &now, model.ResetConfig{Mode: "none", Day: 1, Timezone: "UTC"})
	ctx := context.Background()
	if _, err := tracker.ApplySample(ctx, 100, 100); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err != nil {
		t.Fatal(err)
	}
	// vnStat stops updating while the proxy keeps moving traffic.
	now = now.Add(time.Minute)
	if _, err := tracker.ApplySample(ctx, 2*testGB, 100); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReconcileQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded {
		t.Fatal("a fresh vnStat sample must remain the source of truth")
	}
	now = now.Add(3 * time.Minute)
	if _, err := tracker.ApplySample(ctx, 2*testGB+500, 100); err != nil {
		t.Fatal(err)
	}
	_ = tracker.SampleNetwork(ctx, EntryNetworkScope)
	if err := tracker.ReconcileQuota(ctx); err != nil {
		t.Fatal(err)
	}
	_, estimated := tracker.EntryUsage(tracker.config.Get().TrafficQuota)
	if estimated != 2*testGB+500+100-200 {
		t.Fatalf("estimated proxy bytes = %d", estimated)
	}
	if !tracker.State().QuotaExceeded || core.running {
		t.Fatalf("stale vnStat left the core running: exceeded=%v running=%v", tracker.State().QuotaExceeded, core.running)
	}
}

func TestUnreadableVnStatStillStopsCoreFromProxyCounters(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	tracker, reader, core := newStaleTest(t, &now, model.ResetConfig{Mode: "none", Day: 1, Timezone: "UTC"})
	ctx := context.Background()
	if _, err := tracker.ApplySample(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err != nil {
		t.Fatal(err)
	}
	reader.fail["entry"] = true
	now = now.Add(10 * time.Minute)
	if _, err := tracker.ApplySample(ctx, testGB, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err == nil {
		t.Fatal("failed read accepted")
	}
	if err := tracker.ReconcileQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if !tracker.State().QuotaExceeded || core.running {
		t.Fatal("unreadable vnStat left the core running")
	}
}

func TestStaleEstimateSkipsBytesBeforeFirstCoreSample(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	tracker, _, core := newStaleTest(t, &now, model.ResetConfig{Mode: "none", Day: 1, Timezone: "UTC"})
	ctx := context.Background()
	// The panel restarts: the saved state knows an older core total, and
	// vnStat already covers the traffic moved while the panel was down.
	tracker.state.Upload, tracker.state.LastCoreUpload = 1000, 1000
	if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.ApplySample(ctx, 3*testGB, 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Minute)
	if _, err := tracker.ApplySample(ctx, 3*testGB+700, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReconcileQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if _, estimated := tracker.EntryUsage(tracker.config.Get().TrafficQuota); estimated != 700 {
		t.Fatalf("downtime bytes counted on top of vnStat: estimated=%d", estimated)
	}
	if tracker.State().QuotaExceeded || !core.running {
		t.Fatal("downtime bytes caused a false stop")
	}
}

func TestStaleEstimateStartsAtNewBillingPeriod(t *testing.T) {
	now := time.Date(2026, 10, 16, 23, 50, 0, 0, time.UTC)
	tracker, _, core := newStaleTest(t, &now, model.ResetConfig{Mode: "monthly", Day: 17, Timezone: "UTC"})
	ctx := context.Background()
	if _, err := tracker.ApplySample(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err != nil {
		t.Fatal(err)
	}
	// vnStat freezes; 2 GB move before the reset and belong to the old period.
	now = time.Date(2026, 10, 16, 23, 59, 30, 0, time.UTC)
	if _, err := tracker.ApplySample(ctx, 2*testGB, 0); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 10, 17, 0, 0, 30, 0, time.UTC)
	_ = tracker.SampleNetwork(ctx, EntryNetworkScope)
	now = time.Date(2026, 10, 17, 0, 5, 0, 0, time.UTC)
	if _, err := tracker.ApplySample(ctx, 2*testGB+100_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReconcileQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if _, estimated := tracker.EntryUsage(tracker.config.Get().TrafficQuota); estimated != 100_000_000 {
		t.Fatalf("old-period bytes leaked into the new period: estimated=%d", estimated)
	}
	if tracker.State().QuotaExceeded || !core.running {
		t.Fatal("old-period bytes stopped the new period")
	}
}
