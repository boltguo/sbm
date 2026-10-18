package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

func TestCalibratedEntryUsageCountsTowardStopUntilReset(t *testing.T) {
	now := time.Date(2026, 10, 16, 23, 0, 0, 0, time.UTC)
	tracker, _, core := newStaleTest(t, &now, model.ResetConfig{Mode: "monthly", Day: 17, Timezone: "UTC"})
	ctx := context.Background()
	if _, err := tracker.ApplySample(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}
	// SBM is installed after the provider already counted 0.8 GB this period.
	if err := tracker.CalibrateEntryUsage(ctx, 800_000_000, false); err != nil {
		t.Fatal(err)
	}
	q := tracker.config.Get().TrafficQuota
	if usage, _ := tracker.EntryUsage(q); usage != 800_000_000 {
		t.Fatalf("calibrated usage = %d", usage)
	}
	if tracker.State().QuotaExceeded {
		t.Fatal("stopped below the 0.9 GB threshold")
	}
	now = now.Add(10 * time.Second)
	if _, err := tracker.ApplySample(ctx, 0, 150_000_000); err != nil {
		t.Fatal(err)
	}
	if !tracker.State().QuotaExceeded || core.running {
		t.Fatal("calibrated usage did not count toward the stop")
	}
	// A billing-mode change makes the old figure meaningless.
	q.BillingMode = model.TrafficBillingBidirectional
	if usage, _ := tracker.EntryUsage(q); usage != 2*150_000_000 {
		t.Fatalf("calibration applied to another billing mode: %d", usage)
	}
	now = time.Date(2026, 10, 17, 0, 1, 0, 0, time.UTC)
	snapshot := tracker.NetworkReader.(*networkFake).snapshots["entry"]
	snapshot.UpdatedAt = now
	snapshot.TX += 10
	snapshot.Buckets = append(snapshot.Buckets, nettraffic.Bucket{Start: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC).Unix(), Seconds: 300, TX: 10})
	tracker.NetworkReader.(*networkFake).snapshots["entry"] = snapshot
	if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState(EntryNetworkScope); st.Adjustment != (model.UsageAdjustment{}) {
		t.Fatalf("calibration survived the period reset: %+v", st.Adjustment)
	}
	if tracker.State().QuotaExceeded || !core.running {
		t.Fatal("new period did not resume the core")
	}
}

func TestClearingEntryCalibrationRestoresMeasuredUsage(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	tracker, _, _ := newStaleTest(t, &now, model.ResetConfig{Mode: "none", Day: 1, Timezone: "UTC"})
	ctx := context.Background()
	if _, err := tracker.ApplySample(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.CalibrateEntryUsage(ctx, 500, false); err != nil {
		t.Fatal(err)
	}
	if err := tracker.CalibrateEntryUsage(ctx, 0, true); err != nil {
		t.Fatal(err)
	}
	if usage, _ := tracker.EntryUsage(tracker.config.Get().TrafficQuota); usage != 0 {
		t.Fatalf("usage after clearing = %d", usage)
	}
	if err := tracker.CalibrateEntryUsage(ctx, -1, false); err == nil {
		t.Fatal("negative usage accepted")
	}
}

func TestCalibratedGatewayUsageClearsAtItsOwnReset(t *testing.T) {
	now := time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.EgressGateways = []model.EgressGateway{{ID: "sg", Enabled: true, Server: "203.0.113.1", ServerPort: 51820,
		TrafficQuota: model.TrafficQuotaConfig{Amount: 1, Unit: model.TrafficUnitGB, BillingMode: model.TrafficBillingSingle, HeadroomPercent: 10},
		Reset:        model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}}}
	tracker := NewForTest(model.DefaultState(now), &configSource{cfg: cfg}, nil, func() time.Time { return now })
	sampler := &fakeGatewaySampler{counters: map[string]GatewayCounters{"sg": {TX: 100, RX: 100, TXGeneration: "a", RXGeneration: "a"}}}
	tracker.Gateways = sampler
	ctx := context.Background()
	if err := tracker.SampleGateways(ctx); err != nil {
		t.Fatal(err)
	}
	sampler.counters["sg"] = GatewayCounters{TX: 1100, RX: 100, TXGeneration: "a", RXGeneration: "a"}
	if err := tracker.CalibrateGateway(ctx, "sg", 950_000_000, false); err != nil {
		t.Fatal(err)
	}
	g := cfg.EgressGateways[0]
	st := tracker.State().Egress["sg"]
	if used := st.ProviderUsage(g.TrafficQuota); used != 950_000_000 {
		t.Fatalf("calibrated gateway usage = %d", used)
	}
	sampler.counters["sg"] = GatewayCounters{TX: 1600, RX: 100, TXGeneration: "a", RXGeneration: "a"}
	now = time.Date(2026, 11, 1, 0, 0, 5, 0, time.UTC)
	if err := tracker.SampleGateways(ctx); err != nil {
		t.Fatal(err)
	}
	st = tracker.State().Egress["sg"]
	if st.Adjustment != (model.UsageAdjustment{}) || st.ProviderUsage(g.TrafficQuota) != 500 {
		t.Fatalf("gateway calibration survived its reset: %+v", st)
	}
	if err := tracker.CalibrateGateway(ctx, "missing", 1, false); err == nil {
		t.Fatal("unknown gateway accepted")
	}
}
