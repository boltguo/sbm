package traffic

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/store"
)

type fakeGatewaySampler struct {
	counters map[string]GatewayCounters
	err      error
}

func (f *fakeGatewaySampler) Sample(context.Context, []model.EgressGateway) (map[string]GatewayCounters, error) {
	return f.counters, f.err
}
func gatewayTracker(t *testing.T) (*Tracker, *store.ConfigStore, *fakeGatewaySampler, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.EgressGateways = []model.EgressGateway{
		{ID: "aws", Enabled: true, Server: "203.0.113.1", ServerPort: 51820, Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}, TrafficQuota: model.TrafficQuotaConfig{Amount: 1, Unit: "GB", BillingMode: "bidirectional", HeadroomPercent: 10}},
		{ID: "jp", Enabled: true, Server: "203.0.113.2", ServerPort: 51820, Reset: model.ResetConfig{Mode: "monthly", Day: 18, Timezone: "Asia/Tokyo"}, TrafficQuota: cfg.TrafficQuota},
	}
	config := store.NewConfigStore(filepath.Join(t.TempDir(), "config.json"), cfg)
	tracker := NewForTest(model.DefaultState(now), config, nil, func() time.Time { return now })
	sampler := &fakeGatewaySampler{counters: map[string]GatewayCounters{"aws": {TX: 100, RX: 200, TXGeneration: "one", RXGeneration: "one"}, "jp": {TX: 1000, RX: 2000, TXGeneration: "one", RXGeneration: "one"}}}
	tracker.Gateways = sampler
	if err := tracker.ReconcileGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	return tracker, config, sampler, &now
}
func TestGatewayCountersRollbackGenerationAndPeerChange(t *testing.T) {
	tracker, config, sampler, _ := gatewayTracker(t)
	sampler.counters["aws"] = GatewayCounters{TX: 150, RX: 280, TXGeneration: "one", RXGeneration: "one"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := tracker.State().Egress["aws"]; s.TX != 50 || s.RX != 80 {
		t.Fatalf("deltas=%+v", s)
	}
	// Only the RX rule was rebuilt; its new counter is already larger than the
	// old one. The generation must still detect that reconstruction.
	sampler.counters["aws"] = GatewayCounters{TX: 170, RX: 400, TXGeneration: "one", RXGeneration: "two"}
	_ = tracker.SampleGateways(context.Background())
	if s := tracker.State().Egress["aws"]; s.TX != 70 || s.RX != 480 {
		t.Fatalf("generation deltas=%+v", s)
	}
	sampler.counters["aws"] = GatewayCounters{TX: 10, RX: 5, TXGeneration: "one", RXGeneration: "two"}
	_ = tracker.SampleGateways(context.Background())
	if s := tracker.State().Egress["aws"]; s.TX != 80 || s.RX != 485 {
		t.Fatal("counter rollback not folded")
	}
	old := config.Get()
	next := config.Get()
	next.EgressGateways[0].Server = "203.0.113.3"
	_ = config.Replace(next)
	release := tracker.BeginEgressChange(context.Background())
	err := tracker.CommitEgressChange(context.Background(), old, next)
	release()
	if err != nil {
		t.Fatal(err)
	}
	if s := tracker.State().Egress["aws"]; s.TX != 80 || s.RX != 485 {
		t.Fatal("peer change changed accumulated usage")
	}
	sampler.counters["aws"] = GatewayCounters{TX: 20, RX: 15, TXGeneration: "one", RXGeneration: "two"}
	_ = tracker.SampleGateways(context.Background())
	if s := tracker.State().Egress["aws"]; s.TX != 90 || s.RX != 495 {
		t.Fatal("new peer baseline incorrect")
	}
}
func TestIndependentGatewayResetAndFailure(t *testing.T) {
	tracker, config, sampler, now := gatewayTracker(t)
	sampler.counters["aws"] = GatewayCounters{TX: 200, RX: 300, TXGeneration: "one", RXGeneration: "one"}
	sampler.counters["jp"] = GatewayCounters{TX: 1100, RX: 2100, TXGeneration: "one", RXGeneration: "one"}
	_ = tracker.SampleGateways(context.Background())
	if err := tracker.ResetGateway(context.Background(), "aws"); err != nil {
		t.Fatal(err)
	}
	if tracker.State().Egress["aws"].TX != 0 || tracker.State().Egress["jp"].TX != 100 {
		t.Fatal("reset affected another gateway")
	}
	sampler.counters["aws"] = GatewayCounters{TX: 210, RX: 315, TXGeneration: "one", RXGeneration: "one"}
	_ = tracker.SampleGateways(context.Background())
	if tracker.State().Egress["aws"].TX != 10 {
		t.Fatal("reset lost baseline")
	}
	*now = time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	_ = tracker.SampleGateways(context.Background())
	if tracker.State().Egress["jp"].TX != 0 || tracker.State().Egress["aws"].TX != 10 {
		t.Fatal("monthly resets not independent")
	}
	sampler.err = errors.New("firewall unavailable")
	_ = tracker.SampleGateways(context.Background())
	if s := tracker.State().Egress["aws"]; s.Status != SampleStatusInterrupted || s.TX != 10 || s.FailureSince.IsZero() {
		t.Fatal("failure did not preserve stats")
	}
	sampler.err = nil
	_ = tracker.SampleGateways(context.Background())
	if s := tracker.State().Egress["aws"]; s.Status != SampleStatusHealthy || !s.FailureSince.IsZero() {
		t.Fatal("sampling did not recover")
	}
	old := config.Get()
	next := config.Get()
	next.EgressGateways = next.EgressGateways[1:]
	_ = config.Replace(next)
	release := tracker.BeginEgressChange(context.Background())
	err := tracker.CommitEgressChange(context.Background(), old, next)
	release()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := tracker.State().Egress["aws"]; exists {
		t.Fatal("deleted gateway state retained")
	}
}
func TestDisabledGatewayScheduledReset(t *testing.T) {
	tracker, config, sampler, now := gatewayTracker(t)
	sampler.counters["aws"] = GatewayCounters{TX: 200, RX: 300, TXGeneration: "one", RXGeneration: "one"}
	_ = tracker.SampleGateways(context.Background())
	cfg := config.Get()
	cfg.EgressGateways[0].Enabled = false
	_ = config.Replace(cfg)
	*now = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	_ = tracker.SampleGateways(context.Background())
	s := tracker.State().Egress["aws"]
	if s.TX != 0 || s.RX != 0 || s.Status != "disabled" || s.NextResetAt.Month() != time.December {
		t.Fatal("disabled gateway failed scheduled reset")
	}
}
func TestGatewaySnapshotIsIndependentAndConcurrent(t *testing.T) {
	tracker, _, _, _ := gatewayTracker(t)
	snapshot := tracker.State()
	delete(snapshot.Egress, "aws")
	if len(tracker.State().Egress) != 2 {
		t.Fatal("snapshot aliases live state")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				_ = tracker.State()
				_ = tracker.SampleGateways(context.Background())
				_ = tracker.Persist()
			}
		}()
	}
	wg.Wait()
}
