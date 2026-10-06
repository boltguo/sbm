package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/nettraffic"
)

func reviewSnapshot(created, updated time.Time) nettraffic.Snapshot {
	s := nettraffic.Snapshot{Interface: "ens5", CreatedAt: created, UpdatedAt: updated}
	for at := created; at.Before(updated); at = at.AddDate(0, 0, 1) {
		s.Buckets = append(s.Buckets, nettraffic.Bucket{Start: at.Unix(), Seconds: 86400, RX: 100, TX: 200})
		s.RX += 100
		s.TX += 200
	}
	return s
}

func TestVnStatRegressionUnlimitedWithoutSource(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tracker, _, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	tracker.state.Network = nil
	tracker.state.QuotaExceeded = true
	tracker.config.(*configSource).cfg.TrafficQuota.Amount = 0
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded {
		t.Fatal("unlimited still retains quota stop without first vnStat sample")
	}
}

func TestVnStatRegressionRecreationAcrossBillingReset(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tracker, reader, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	reader.snapshots["entry"] = reviewSnapshot(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), now)
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	s := tracker.NetworkState("entry")
	if s.RX != 100 || s.TX != 200 {
		t.Fatalf("current period should be RX100/TX200, got RX%d/TX%d", s.RX, s.TX)
	}
}

func TestVnStatRegressionSourceSwitchPersistenceRetry(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tracker, reader, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	old := tracker.NetworkState("entry")
	if _, err := tracker.history.db.Exec(`CREATE TRIGGER report_reject BEFORE UPDATE ON checkpoint BEGIN SELECT RAISE(ABORT,'fault'); END`); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 1)
	switchAt := now
	tracker.config.(*configSource).cfg.VnStatInterface = "ens6"
	s := networkFixture(now)
	s.Interface = "ens6"
	reader.snapshots["entry"] = s
	if err := tracker.SampleNetwork(ctx, "entry"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if _, err := tracker.history.db.Exec(`DROP TRIGGER report_reject`); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 1)
	s = networkFixture(now)
	s.Interface = "ens6"
	reader.snapshots["entry"] = s
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	var ended int64
	if err := tracker.history.db.QueryRow(`SELECT ended_at FROM network_source WHERE generation=?`, old.Generation).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	if ended != switchAt.Unix() {
		t.Fatalf("source switch was %s, persisted previous-source end is %s", switchAt, time.Unix(ended, 0))
	}
}

func TestVnStatRegressionGenerationGapMarksMonthPartial(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tracker, reader, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	reader.snapshots["entry"] = reviewSnapshot(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), now)
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	h, err := tracker.NetworkHistory(ctx, "entry", "month", "2026-10", "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Rows) != 1 || !h.Rows[0].NetworkPartial {
		t.Fatalf("Oct1-2 gap not marked partial: %+v", h.Rows)
	}
}
