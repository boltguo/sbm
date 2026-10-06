package traffic

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/store"
)

func historyTracker(t *testing.T, state model.State, cfg model.Config, now *time.Time) (*Tracker, string, string, *configSource) {
	t.Helper()
	dir := t.TempDir()
	statePath, dbPath := filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db")
	if err := store.NewJSONFile[model.State](statePath).SaveWithoutBackup(state); err != nil {
		t.Fatal(err)
	}
	source := &configSource{cfg: cfg}
	tracker, err := OpenWithHistory(statePath, dbPath, source, &fakeCore{running: true}, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracker.Close() })
	return tracker, statePath, dbPath, source
}

func historyQuery(t *testing.T, tracker *Tracker, granularity, from, to string) HistoryResponse {
	t.Helper()
	result, err := tracker.History(context.Background(), granularity, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHistoryImportResetAndSQLiteRecovery(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	state := model.DefaultState(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	state.Upload, state.Download = 100, 200
	state.LastCoreUpload, state.LastCoreDownload = 100, 200
	state.UpdatedAt = now
	cfg := model.DefaultConfig()
	cfg.Reset.Timezone = "UTC"
	cfg.TrafficQuota.BillingMode = model.TrafficBillingBidirectional
	tracker, statePath, dbPath, source := historyTracker(t, state, cfg, &now)
	initial := historyQuery(t, tracker, "day", "2026-10-01", "2026-10-31")
	if len(initial.Rows) != 0 || len(initial.Imports) != 1 || initial.Imports[0].Upload != 100 {
		t.Fatalf("invented daily breakdown or lost import: %+v", initial)
	}
	if _, err := tracker.ApplySample(context.Background(), 125, 250); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := tracker.Persist(); err != nil {
			t.Fatal(err)
		}
	}
	// A reset also flushes unsaved daily deltas and must never clear history.
	if _, err := tracker.ApplySample(context.Background(), 150, 260); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().Total() != 0 {
		t.Fatal("period usage was not reset")
	}
	daily := historyQuery(t, tracker, "day", "2026-10-01", "2026-10-31")
	if len(daily.Rows) != 1 || daily.Rows[0].Upload != 50 || daily.Rows[0].Download != 60 || daily.Rows[0].EstimatedProviderUsedBytes != 220 {
		t.Fatalf("reset erased or repeated daily usage: %+v", daily.Rows)
	}
	monthly := historyQuery(t, tracker, "month", "2026-10", "2026-10")
	if len(monthly.Rows) != 1 || monthly.Rows[0].ProxyUsedBytes != 410 || monthly.Rows[0].EstimatedProviderUsedBytes != 820 || !monthly.Rows[0].Imported {
		t.Fatalf("incorrect imported monthly total: %+v", monthly.Rows)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	// A stale/corrupt compatibility mirror cannot override a committed DB.
	if err := os.WriteFile(statePath, []byte("broken JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithHistory(statePath, dbPath, source, &fakeCore{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.ApplySample(context.Background(), 150, 260); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Persist(); err != nil {
		t.Fatal(err)
	}
	if reopened.State().Total() != 0 || historyQuery(t, reopened, "month", "2026-10", "2026-10").Rows[0].ProxyUsedBytes != 410 {
		t.Fatal("restart re-counted committed traffic")
	}
	info, err := os.Stat(dbPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("database is not private: %v %v", info, err)
	}
}

func TestHistoryTimezoneMonthBoundaryAndCoreRestart(t *testing.T) {
	now := time.Date(2026, 9, 30, 15, 59, 58, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "Asia/Shanghai"}
	state := model.DefaultState(now)
	state.NextResetAt = now.Add(2 * time.Second)
	tracker, _, _, source := historyTracker(t, state, cfg, &now)
	if _, err := tracker.ApplySample(context.Background(), 110, 220); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if err := tracker.CheckScheduledReset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().Total() != 0 {
		t.Fatal("scheduled reset did not clear period usage")
	}
	if _, err := tracker.ApplySample(context.Background(), 120, 240); err != nil {
		t.Fatal(err)
	}
	// A core restart lowers its counters. Only new-generation bytes join the
	// daily record, with the billing mode effective for that sample.
	source.cfg.TrafficQuota.BillingMode = model.TrafficBillingBidirectional
	now = now.Add(time.Second)
	if _, err := tracker.applySample(context.Background(), 5, 7, "new-generation"); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	result := historyQuery(t, tracker, "month", "2026-09", "2026-10")
	if result.Timezone != "Asia/Shanghai" || len(result.Rows) != 2 || result.Rows[0].ProxyUsedBytes != 330 || result.Rows[1].ProxyUsedBytes != 42 || result.Rows[1].EstimatedProviderUsedBytes != 54 {
		t.Fatalf("wrong timezone, restart or billing-mode accounting: %+v", result)
	}
}

func TestHistoryCrossDayGapIsMarkedAndTimezoneStaysFixed(t *testing.T) {
	now := time.Date(2026, 10, 6, 23, 59, 58, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.Reset.Timezone = "UTC"
	tracker, statePath, dbPath, source := historyTracker(t, model.DefaultState(now), cfg, &now)
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if _, err := tracker.ApplySample(context.Background(), 150, 250); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	result := historyQuery(t, tracker, "day", "2026-10-06", "2026-10-07")
	if len(result.Rows) != 2 || !result.Rows[0].Partial || !result.Rows[1].Partial || result.Rows[0].ProxyUsedBytes != 300 || result.Rows[1].ProxyUsedBytes != 100 {
		t.Fatalf("gap was hidden or retroactively distributed: %+v", result.Rows)
	}
	tracker.Close()
	source.cfg.Reset.Timezone = "Asia/Shanghai"
	reopened, err := OpenWithHistory(statePath, dbPath, source, &fakeCore{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if result := historyQuery(t, reopened, "day", "2026-10-06", "2026-10-07"); result.Timezone != "UTC" || len(result.Rows) != 2 {
		t.Fatalf("timezone change reinterpreted saved dates: %+v", result)
	}
}

func TestHistoryCheckpointTransactionFailureCanRetry(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tracker, _, _, _ := historyTracker(t, model.DefaultState(now), model.DefaultConfig(), &now)
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.history.db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE ON checkpoint BEGIN SELECT RAISE(ABORT, 'write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err == nil {
		t.Fatal("checkpoint failure was ignored")
	}
	if got := historyQuery(t, tracker, "day", "2026-10-01", "2026-10-31"); len(got.Rows) != 0 {
		t.Fatal("daily writes survived a failed checkpoint transaction")
	}
	if _, err := tracker.history.db.Exec(`DROP TRIGGER fail_checkpoint`); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	if got := historyQuery(t, tracker, "day", "2026-10-01", "2026-10-31"); len(got.Rows) != 1 || got.Rows[0].ProxyUsedBytes != 300 {
		t.Fatalf("retry lost or repeated pending traffic: %+v", got)
	}
}

func TestHistoryConcurrentSamplingPersistenceAndQueries(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tracker, _, _, _ := historyTracker(t, model.DefaultState(now), model.DefaultConfig(), &now)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := int64(1); i <= 500; i++ {
			if _, err := tracker.ApplySample(context.Background(), i, i*2); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := tracker.Persist(); err != nil {
				t.Error(err)
				return
			}
			if _, err := tracker.History(context.Background(), "day", "2026-10-01", "2026-10-31"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	if got := historyQuery(t, tracker, "day", "2026-10-01", "2026-10-31"); len(got.Rows) != 1 || got.Rows[0].ProxyUsedBytes != 1500 {
		t.Fatalf("concurrent flush lost or repeated traffic: %+v", got)
	}
}

func TestHistoryDoesNotInventBreakdownForCrossMonthImport(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	state := model.DefaultState(now.AddDate(0, -1, 0))
	state.Upload, state.Download, state.UpdatedAt = 100, 200, now
	tracker, _, _, _ := historyTracker(t, state, model.DefaultConfig(), &now)
	result := historyQuery(t, tracker, "month", "2026-09", "2026-10")
	if len(result.Rows) != 0 || len(result.Imports) != 1 {
		t.Fatalf("invented a month for an unallocated total: %+v", result)
	}
}

func TestHistoryResetDuringOutageKeepsLastSampleAcrossRestart(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tracker, statePath, dbPath, source := historyTracker(t, model.DefaultState(now), model.DefaultConfig(), &now)
	now = time.Date(2026, 10, 5, 23, 59, 59, 0, time.UTC)
	if _, err := tracker.ApplySample(context.Background(), 0, 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	// Resetting the quota during a sampling outage is not a successful sample.
	// Its newer checkpoint timestamp must not hide the previous day's gap.
	now = now.Add(24*time.Hour + 10*time.Minute)
	if err := tracker.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	tracker.Close()
	reopened, err := OpenWithHistory(statePath, dbPath, source, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	now = now.Add(time.Second)
	if _, err := reopened.ApplySample(context.Background(), 150, 250); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Persist(); err != nil {
		t.Fatal(err)
	}
	rows := historyQuery(t, reopened, "day", "2026-10-06", "2026-10-07").Rows
	if len(rows) != 2 || !rows[0].Partial || !rows[1].Partial || rows[1].ProxyUsedBytes != 100 {
		t.Fatalf("reset checkpoint hid the sampling gap: %+v", rows)
	}
}

func TestHistoryJSONMirrorFailureDoesNotRepeatCommittedBytes(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	tracker, statePath, dbPath, source := historyTracker(t, model.DefaultState(now), model.DefaultConfig(), &now)
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err == nil {
		t.Fatal("JSON mirror failure was ignored")
	}
	if got := historyQuery(t, tracker, "day", "2026-10-06", "2026-10-06"); len(got.Rows) != 1 || got.Rows[0].ProxyUsedBytes != 300 {
		t.Fatalf("SQLite commit was lost with the JSON mirror: %+v", got)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	tracker.Close()
	reopened, err := OpenWithHistory(statePath, dbPath, source, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Persist(); err != nil {
		t.Fatal(err)
	}
	if got := historyQuery(t, reopened, "day", "2026-10-06", "2026-10-06"); len(got.Rows) != 1 || got.Rows[0].ProxyUsedBytes != 300 {
		t.Fatalf("mirror retry or restart repeated committed bytes: %+v", got)
	}
}

func TestHistoryAndGatewayCheckpointsRemainIndependent(t *testing.T) {
	old, config, sampler, now := gatewayTracker(t)
	tracker, statePath, dbPath, source := historyTracker(t, old.State(), config.Get(), now)
	tracker.Gateways = sampler
	sampler.counters["aws"] = GatewayCounters{TX: 150, RX: 280, TXGeneration: "one", RXGeneration: "one"}
	sampler.counters["jp"] = GatewayCounters{TX: 1100, RX: 2100, TXGeneration: "one", RXGeneration: "one"}
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.ApplySample(context.Background(), 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	tracker.Close()
	if err := os.WriteFile(statePath, []byte("broken compatibility mirror"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithHistory(statePath, dbPath, source, nil, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.Gateways = sampler
	if err := reopened.ReconcileGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := reopened.State(); s.Total() != 300 || s.Egress["aws"].TX != 50 || s.Egress["aws"].RX != 80 || s.Egress["jp"].TX != 100 || s.Egress["jp"].RX != 100 {
		t.Fatalf("SQLite restart recounted or mixed independent counters: %+v", s)
	}
	if err := reopened.ResetGateway(context.Background(), "aws"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ApplySample(context.Background(), 105, 210); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Persist(); err != nil {
		t.Fatal(err)
	}
	if s := reopened.State(); s.Total() != 315 || s.Egress["aws"].TX != 0 || s.Egress["aws"].RX != 0 || s.Egress["jp"].TX != 100 || s.Egress["jp"].RX != 100 {
		t.Fatalf("gateway reset affected global usage or another gateway: %+v", s)
	}
	rows := historyQuery(t, reopened, "day", "2026-10-05", "2026-10-05").Rows
	if len(rows) != 1 || rows[0].ProxyUsedBytes != 315 {
		t.Fatalf("tunnel counters were added to global history or history was cleared: %+v", rows)
	}
}

func TestDifferentResetDaysKeepGlobalAndGatewayUsageIndependent(t *testing.T) {
	for _, zones := range [][2]string{{"UTC", "UTC"}, {"Asia/Shanghai", "Asia/Tokyo"}} {
		t.Run(zones[0]+"_"+zones[1], func(t *testing.T) {
			gatewayZone, err := time.LoadLocation(zones[1])
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 30, 23, 59, 58, 0, gatewayZone)
			cfg := model.DefaultConfig()
			cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 15, Timezone: zones[0]}
			cfg.EgressGateways = []model.EgressGateway{
				{ID: "first", Enabled: true, Server: "203.0.113.1", ServerPort: 51820, Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: zones[1]}, TrafficQuota: cfg.TrafficQuota},
				{ID: "second", Enabled: true, Server: "203.0.113.2", ServerPort: 51820, Reset: model.ResetConfig{Mode: "monthly", Day: 18, Timezone: zones[0]}, TrafficQuota: cfg.TrafficQuota},
			}
			state := model.DefaultState(now)
			state.NextResetAt, err = NextMonthlyReset(now, cfg.Reset)
			if err != nil {
				t.Fatal(err)
			}
			tracker, statePath, dbPath, source := historyTracker(t, state, cfg, &now)
			sampler := &fakeGatewaySampler{counters: map[string]GatewayCounters{
				"first":  {TX: 100, RX: 200, TXGeneration: "one", RXGeneration: "one"},
				"second": {TX: 1000, RX: 2000, TXGeneration: "one", RXGeneration: "one"},
			}}
			tracker.Gateways = sampler
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			coreSample := func(upload, download int64) {
				t.Helper()
				_, err := tracker.ApplySample(context.Background(), upload, download)
				must(err)
			}
			assertUsage := func(global, first, second int64) {
				t.Helper()
				s := tracker.State()
				if s.Total() != global || s.Egress["first"].TX+s.Egress["first"].RX != first || s.Egress["second"].TX+s.Egress["second"].RX != second {
					t.Fatalf("reset mixed independent usage: global=%d first=%d second=%d, state=%+v", global, first, second, s)
				}
			}
			must(tracker.ReconcileGateways(context.Background()))
			coreSample(100, 200)
			now = now.Add(time.Second)
			sampler.counters["first"] = GatewayCounters{TX: 150, RX: 280, TXGeneration: "one", RXGeneration: "one"}
			sampler.counters["second"] = GatewayCounters{TX: 1100, RX: 2200, TXGeneration: "one", RXGeneration: "one"}
			must(tracker.SampleGateways(context.Background()))
			coreSample(120, 230)
			assertUsage(350, 130, 300)

			// On the first, only the first gateway enters a new allowance cycle.
			now = tracker.State().Egress["first"].NextResetAt
			must(tracker.CheckScheduledReset(context.Background()))
			must(tracker.SampleGateways(context.Background()))
			coreSample(125, 235)
			assertUsage(360, 0, 300)
			if tracker.State().NextResetAt.Day() != 15 || tracker.State().Egress["first"].NextResetAt.Day() != 1 {
				t.Fatal("one reset overwrote another reset schedule")
			}
			now = now.Add(time.Second)
			sampler.counters["first"] = GatewayCounters{TX: 170, RX: 300, TXGeneration: "one", RXGeneration: "one"}
			sampler.counters["second"] = GatewayCounters{TX: 1110, RX: 2220, TXGeneration: "one", RXGeneration: "one"}
			must(tracker.SampleGateways(context.Background()))
			coreSample(140, 260)
			assertUsage(400, 40, 330)
			must(tracker.Persist())
			must(tracker.Close())
			tracker, err = OpenWithHistory(statePath, dbPath, source, nil, func() time.Time { return now })
			must(err)
			defer tracker.Close()
			tracker.Gateways = sampler
			must(tracker.CheckScheduledReset(context.Background()))
			must(tracker.ReconcileGateways(context.Background()))
			coreSample(140, 260)
			assertUsage(400, 40, 330)

			// On the fifteenth, only the entry's plan total resets. The core and
			// kernel counters remain cumulative, and history retains all bytes.
			now = tracker.State().NextResetAt
			must(tracker.CheckScheduledReset(context.Background()))
			assertUsage(0, 40, 330)
			coreSample(140, 260)
			must(tracker.SampleGateways(context.Background()))
			assertUsage(0, 40, 330)
			now = now.Add(time.Second)
			coreSample(145, 270)
			sampler.counters["first"] = GatewayCounters{TX: 180, RX: 315, TXGeneration: "one", RXGeneration: "one"}
			sampler.counters["second"] = GatewayCounters{TX: 1115, RX: 2230, TXGeneration: "one", RXGeneration: "one"}
			must(tracker.SampleGateways(context.Background()))
			assertUsage(15, 65, 345)
			must(tracker.Persist())
			rows := historyQuery(t, tracker, "day", "2026-09-01", "2026-10-31").Rows
			var historical int64
			for _, row := range rows {
				historical += row.ProxyUsedBytes
			}
			if historical != 415 {
				t.Fatalf("independent plan resets cleared or repeated history: %d", historical)
			}

			// The second gateway's own reset must preserve the other two totals.
			now = tracker.State().Egress["second"].NextResetAt
			must(tracker.CheckScheduledReset(context.Background()))
			must(tracker.SampleGateways(context.Background()))
			assertUsage(15, 65, 0)
			source.cfg.EgressGateways[0].Enabled = false
			now = tracker.State().Egress["first"].NextResetAt
			must(tracker.CheckScheduledReset(context.Background()))
			must(tracker.SampleGateways(context.Background()))
			assertUsage(15, 0, 0)
			if tracker.State().Egress["first"].Status != "disabled" {
				t.Fatal("disabled gateway lost its independent cycle")
			}
		})
	}
}

func TestGatewayMonthlyResetDoesNotReleaseEntryQuota(t *testing.T) {
	now := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	cfg := configWithLimit(100)
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 15, Timezone: "UTC"}
	cfg.EgressGateways = []model.EgressGateway{{ID: "first", Enabled: true, Server: "203.0.113.1", ServerPort: 51820, Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}, TrafficQuota: model.DefaultConfig().TrafficQuota}}
	state := model.DefaultState(now)
	state.Upload, state.LastCoreUpload, state.QuotaExceeded = 100, 100, true
	state.NextResetAt, _ = NextMonthlyReset(now, cfg.Reset)
	tracker, _, _, _ := historyTracker(t, state, cfg, &now)
	core := tracker.core.(*fakeCore)
	sampler := &fakeGatewaySampler{counters: map[string]GatewayCounters{"first": {TX: 100, RX: 200, TXGeneration: "one", RXGeneration: "one"}}}
	tracker.Gateways = sampler
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReconcileGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = tracker.State().Egress["first"].NextResetAt
	if err := tracker.SampleGateways(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ResetGateway(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if !tracker.State().QuotaExceeded || tracker.State().Total() != 100 || core.running || core.starts != 0 || core.stops != 1 {
		t.Fatal("gateway reset bypassed entry quota enforcement")
	}
	now = tracker.State().NextResetAt
	if err := tracker.CheckScheduledReset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded || tracker.State().Total() != 0 || !core.running || core.starts != 1 {
		t.Fatal("entry's own reset did not release its quota")
	}
}

func TestHistoryRejectsInvalidRangeAndCorruptDatabase(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, args := range [][3]string{{"hour", "", ""}, {"day", "2026-02-30", "2026-03-01"}, {"day", "2026-10-02", "2026-10-01"}, {"day", "2020-01-01", "2026-10-01"}, {"month", "2026-1", "2026-10"}, {"month", "2000-01", "2026-10"}} {
		if _, _, err := historyRange(args[0], args[1], args[2], time.UTC, now); err != ErrHistoryRange {
			t.Fatalf("invalid range accepted: %v %v", args, err)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "traffic.db")
	if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithHistory(filepath.Join(dir, "state.json"), path, &configSource{cfg: model.DefaultConfig()}, &fakeCore{}, nil); err == nil {
		t.Fatal("corrupt history was silently replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "not a database" {
		t.Fatalf("corrupt database was modified: %q %v", data, err)
	}
}
