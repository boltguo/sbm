package traffic

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/store"
)

func durableGatewayTracker(t *testing.T, now *time.Time) (*Tracker, string, string, *configSource, *fakeGatewaySampler) {
	t.Helper()
	cfg := model.DefaultConfig()
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 15, Timezone: "UTC"}
	cfg.EgressGateways = []model.EgressGateway{{ID: "aws", Enabled: true, Server: "203.0.113.1", ServerPort: 51820, Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}, TrafficQuota: cfg.TrafficQuota}}
	tracker, statePath, dbPath, source := historyTracker(t, model.DefaultState(*now), cfg, now)
	sampler := &fakeGatewaySampler{counters: map[string]GatewayCounters{"aws": {TX: 100, RX: 200, TXGeneration: "one", RXGeneration: "one"}}}
	tracker.Gateways = sampler
	if err := tracker.ReconcileGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	return tracker, statePath, dbPath, source, sampler
}

type incrementingGatewaySampler struct{ calls int64 }

func (s *incrementingGatewaySampler) Sample(context.Context, []model.EgressGateway) (map[string]GatewayCounters, error) {
	s.calls++
	return map[string]GatewayCounters{"aws": {TX: s.calls * 2, RX: s.calls * 3, TXGeneration: "one", RXGeneration: "one"}}, nil
}

func TestGatewayHistoryConcurrentSamplingAndPersistence(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tracker, _, _, _, _ := durableGatewayTracker(t, &now)
	sampler := &incrementingGatewaySampler{calls: 100}
	tracker.Gateways = sampler
	// Switching the test source increments its existing generation counters.
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	beforeTX, beforeRX := gatewayRecordedTotals(t, tracker, "aws")
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range 30 {
			if err := tracker.SampleGateways(context.Background()); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := int64(1); i <= 30; i++ {
			if _, err := tracker.ApplySample(context.Background(), i, i*2); err != nil {
				t.Error(err)
				return
			}
			if err := tracker.Persist(); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 10 {
			if _, err := tracker.History(context.Background(), "day", "", ""); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, tracker, beforeTX+60, beforeRX+90)
	s := tracker.State()
	if s.Total() != 90 || s.Egress["aws"].TX != beforeTX+60 || s.Egress["aws"].RX != beforeRX+90 {
		t.Fatalf("concurrent checkpoint diverged from records: %+v", s)
	}
	rows := historyQuery(t, tracker, "day", "2026-10-06", "2026-10-06").Rows
	if len(rows) != 1 || rows[0].ProxyUsedBytes != 90 {
		t.Fatalf("concurrent persistence lost/repeated entry deltas: %+v", rows)
	}
}

func gatewayRecordedTotals(t *testing.T, tracker *Tracker, id string) (int64, int64) {
	t.Helper()
	var tx, rx int64
	err := tracker.history.db.QueryRow(`SELECT COALESCE(SUM(tx),0), COALESCE(SUM(rx),0) FROM
		(SELECT tx,rx FROM gateway_daily WHERE gateway_id=? UNION ALL
		SELECT tx,rx FROM gateway_imports WHERE gateway_id=?)`, id, id).Scan(&tx, &rx)
	if err != nil {
		t.Fatal(err)
	}
	return tx, rx
}

func assertGatewayRecorded(t *testing.T, tracker *Tracker, tx, rx int64) {
	t.Helper()
	actualTX, actualRX := gatewayRecordedTotals(t, tracker, "aws")
	if actualTX != tx || actualRX != rx {
		t.Fatalf("recorded TX/RX=%d/%d, want %d/%d", actualTX, actualRX, tx, rx)
	}
}

func TestGatewaySampleIsDurableWithoutPeriodicSave(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tracker, statePath, dbPath, source, sampler := durableGatewayTracker(t, &now)
	now = now.Add(5 * time.Second)
	sampler.counters["aws"] = GatewayCounters{TX: 150, RX: 280, TXGeneration: "one", RXGeneration: "one"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, tracker, 50, 80)
	// Close deliberately does not persist. A successful sample is already
	// durable even if the panel never reaches its periodic/final save.
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("broken mirror"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithHistory(statePath, dbPath, source, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.Gateways = sampler
	if err := reopened.ReconcileGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, reopened, 50, 80)
	if s := reopened.State().Egress["aws"]; s.TX != 50 || s.RX != 80 || s.LastTX != 150 || s.LastRX != 280 {
		t.Fatalf("checkpoint disagrees with durable history: %+v", s)
	}
	now = now.Add(5 * time.Second)
	sampler.counters["aws"] = GatewayCounters{TX: 200, RX: 350, TXGeneration: "one", RXGeneration: "one"}
	if err := reopened.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, reopened, 100, 150)
}

func TestGatewayFailureAcrossResetKeepsRecoverableBytes(t *testing.T) {
	now := time.Date(2026, 9, 30, 23, 59, 50, 0, time.UTC)
	tracker, statePath, dbPath, source, sampler := durableGatewayTracker(t, &now)
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	sampler.counters["aws"] = GatewayCounters{TX: 150, RX: 280, TXGeneration: "one", RXGeneration: "one"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	sampler.err = errors.New("iptables unavailable")
	if err := tracker.SampleGateways(context.Background()); err == nil {
		t.Fatal("sampling failure was hidden")
	}
	if s := tracker.State().Egress["aws"]; s.TX != 0 || s.RX != 0 || !s.Initialized || s.LastTX != 150 || s.LastRX != 280 {
		t.Fatalf("reset discarded the recovery baseline: %+v", s)
	}
	tracker.Close()
	reopened, err := OpenWithHistory(statePath, dbPath, source, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.Gateways = sampler
	now = now.Add(5 * time.Minute)
	sampler.err = nil
	sampler.counters["aws"] = GatewayCounters{TX: 250, RX: 400, TXGeneration: "one", RXGeneration: "one"}
	if err := reopened.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, reopened, 150, 200)
	if s := reopened.State(); s.Total() != 300 || s.Egress["aws"].TX != 100 || s.Egress["aws"].RX != 120 || !s.Egress["aws"].Partial {
		t.Fatalf("recovery lost bytes, affected entry usage or hid uncertain billing attribution: %+v", s)
	}
	var tx, rx int64
	var partial bool
	if err := reopened.history.db.QueryRow(`SELECT tx,rx,partial FROM gateway_daily WHERE gateway_id='aws' AND date='2026-10-01'`).Scan(&tx, &rx, &partial); err != nil || tx != 100 || rx != 120 || !partial {
		t.Fatalf("recovery record=%d/%d partial=%v err=%v", tx, rx, partial, err)
	}
	if err := reopened.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, reopened, 150, 200)
}

func TestGatewayNormalResetKeepsBoundaryDelta(t *testing.T) {
	now := time.Date(2026, 9, 30, 23, 59, 55, 0, time.UTC)
	tracker, _, _, _, sampler := durableGatewayTracker(t, &now)
	now = now.Add(5 * time.Second)
	sampler.counters["aws"] = GatewayCounters{TX: 110, RX: 220, TXGeneration: "one", RXGeneration: "one"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, tracker, 10, 20)
	if s := tracker.State().Egress["aws"]; s.TX != 10 || s.RX != 20 || s.Partial {
		t.Fatalf("normal sampling lost the boundary delta or raised an outage warning: %+v", s)
	}
}

func TestGatewayPersistenceFailuresRetryExactlyOnce(t *testing.T) {
	for _, failure := range []string{"database", "mirror"} {
		t.Run(failure, func(t *testing.T) {
			now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
			tracker, statePath, _, _, sampler := durableGatewayTracker(t, &now)
			if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
				t.Fatal(err)
			}
			if failure == "database" {
				if _, err := tracker.history.db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE ON checkpoint BEGIN SELECT RAISE(ABORT, 'write failed'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				tracker.file = store.NewJSONFile[model.State](t.TempDir()) // Rename over a directory fails.
			}
			now = now.Add(5 * time.Second)
			sampler.counters["aws"] = GatewayCounters{TX: 150, RX: 280, TXGeneration: "one", RXGeneration: "one"}
			if err := tracker.SampleGateways(context.Background()); err == nil {
				t.Fatal("save failure was hidden")
			}
			if h := tracker.PersistenceHealth(); h.Status != SampleStatusInterrupted || h.FailureSince.IsZero() {
				t.Fatalf("save failure not reported separately from sampling: %+v", h)
			}
			if failure == "database" {
				assertGatewayRecorded(t, tracker, 0, 0)
				state, _, err := tracker.history.loadState()
				if err != nil || state.Egress["aws"].LastTX != 100 {
					t.Fatalf("failed transaction advanced checkpoint: %+v %v", state, err)
				}
				if _, err := tracker.history.db.Exec(`DROP TRIGGER fail_checkpoint`); err != nil {
					t.Fatal(err)
				}
			} else {
				assertGatewayRecorded(t, tracker, 50, 80)
				tracker.file = store.NewJSONFile[model.State](statePath)
			}
			now = now.Add(5 * time.Second)
			sampler.counters["aws"] = GatewayCounters{TX: 170, RX: 300, TXGeneration: "one", RXGeneration: "one"}
			if err := tracker.SampleGateways(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := tracker.Persist(); err != nil {
				t.Fatal(err)
			}
			assertGatewayRecorded(t, tracker, 70, 100)
			if h := tracker.PersistenceHealth(); h.Status != SampleStatusHealthy || !h.FailureSince.IsZero() {
				t.Fatalf("save health did not recover: %+v", h)
			}
			rows := historyQuery(t, tracker, "day", "2026-10-06", "2026-10-06").Rows
			if len(rows) != 1 || rows[0].ProxyUsedBytes != 300 {
				t.Fatalf("gateway retry duplicated entry history: %+v", rows)
			}
		})
	}
}

func TestGatewayManualResetAndRuleReplacementRetainRecords(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tracker, _, _, source, sampler := durableGatewayTracker(t, &now)
	now = now.Add(5 * time.Second)
	sampler.counters["aws"] = GatewayCounters{TX: 200, RX: 300, TXGeneration: "one", RXGeneration: "one"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	sampler.err = errors.New("iptables unavailable")
	if err := tracker.ResetGateway(context.Background(), "aws"); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, tracker, 100, 100)
	now = now.Add(5 * time.Second)
	sampler.err = nil
	sampler.counters["aws"] = GatewayCounters{TX: 250, RX: 350, TXGeneration: "one", RXGeneration: "one"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, tracker, 150, 150)
	if s := tracker.State().Egress["aws"]; s.TX != 50 || s.RX != 50 || !s.Partial {
		t.Fatalf("failed manual reset discarded recovered bytes: %+v", s)
	}
	// A new generation can exceed the old counter; it still starts at zero.
	sampler.counters["aws"] = GatewayCounters{TX: 300, RX: 400, TXGeneration: "two", RXGeneration: "two"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, tracker, 450, 550)
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGatewayRecorded(t, tracker, 450, 550)
	old, next := source.cfg, source.cfg
	next.EgressGateways = nil
	release := tracker.BeginEgressChange(context.Background())
	source.cfg = next
	err := tracker.CommitEgressChange(context.Background(), old, next)
	release()
	if err != nil {
		t.Fatal(err)
	}
	if len(tracker.State().Egress) != 0 {
		t.Fatal("deleted gateway still active")
	}
	assertGatewayRecorded(t, tracker, 450, 550)
}

func TestGatewayHistoryMigrationImportsPeriodTotalsOnce(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tracker, statePath, dbPath, source, sampler := durableGatewayTracker(t, &now)
	sampler.counters["aws"] = GatewayCounters{TX: 180, RX: 300, TXGeneration: "one", RXGeneration: "one"}
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the prior SQLite schema, which stored gateway period totals
	// only in checkpoint. The new schema must not invent daily breakdowns.
	if _, err := tracker.history.db.Exec(`DROP TABLE gateway_daily; DROP TABLE gateway_imports;
		DELETE FROM metadata WHERE key='gateway_history_started_at'; PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	tracker.Close()
	for i := 0; i < 2; i++ {
		reopened, err := OpenWithHistory(statePath, dbPath, source, nil, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		assertGatewayRecorded(t, reopened, 80, 100)
		var rows, imports int
		if err := reopened.history.db.QueryRow(`SELECT COUNT(*) FROM gateway_daily`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if err := reopened.history.db.QueryRow(`SELECT COUNT(*) FROM gateway_imports`).Scan(&imports); err != nil || rows != 0 || imports != 1 {
			t.Fatalf("migration repeated or invented records: rows=%d imports=%d err=%v", rows, imports, err)
		}
		history := historyQuery(t, reopened, "day", "2026-10-06", "2026-10-06")
		if len(history.Rows) != 1 || history.Rows[0].ProxyUsedBytes != 300 {
			t.Fatal("migration affected entry history")
		}
		if i == 1 {
			reopened.Gateways = sampler
			sampler.counters["aws"] = GatewayCounters{TX: 200, RX: 330, TXGeneration: "one", RXGeneration: "one"}
			if err := reopened.SampleGateways(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertGatewayRecorded(t, reopened, 100, 130)
		}
		reopened.Close()
	}
}
