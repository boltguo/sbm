package traffic

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

type networkFake struct {
	snapshots map[string]nettraffic.Snapshot
	fail      map[string]bool
}

func (f *networkFake) Read(_ context.Context, r nettraffic.Request) (nettraffic.Snapshot, error) {
	key := "entry"
	if f.fail[key] {
		return nettraffic.Snapshot{}, errors.New("offline")
	}
	return f.snapshots[key], nil
}
func networkFixture(now time.Time) nettraffic.Snapshot {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := nettraffic.Snapshot{Interface: "ens5", CreatedAt: at, UpdatedAt: now}
	for at.Before(now) {
		seconds := int64(min(24*time.Hour, now.Sub(at)) / time.Hour)
		s.Buckets = append(s.Buckets, nettraffic.Bucket{Start: at.Unix(), Seconds: 86400, RX: seconds * 10, TX: seconds * 20})
		s.RX += seconds * 10
		s.TX += seconds * 20
		at = at.AddDate(0, 0, 1)
	}
	return s
}
func newNetworkTest(t *testing.T, now *time.Time) (*Tracker, *networkFake, string) {
	t.Helper()
	cfg := model.DefaultConfig()
	cfg.VnStatInterface = "ens5"
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 15, Timezone: "UTC"}
	cfg.EgressGateways = []model.EgressGateway{{ID: "exit-1", Enabled: true, Server: "exit-one", Reset: model.ResetConfig{Mode: "monthly", Day: 1, Timezone: "UTC"}}}
	dir := t.TempDir()
	source := &configSource{cfg: cfg}
	tracker, err := OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), source, nil, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	reader := &networkFake{snapshots: map[string]nettraffic.Snapshot{"entry": networkFixture(*now), "exit-one": networkFixture(*now)}, fail: map[string]bool{}}
	tracker.NetworkReader = reader
	// These fixtures represent an installation recording since September 1.
	// Fresh-install behavior is covered separately with an existing source DB.
	current := *now
	*now = reader.snapshots["entry"].CreatedAt
	reader.snapshots["entry"] = networkFixture(*now)
	if err := tracker.SampleNetwork(context.Background(), "entry"); err != nil {
		t.Fatal(err)
	}
	*now = current
	reader.snapshots["entry"] = networkFixture(current)
	return tracker, reader, dir
}
func TestVnStatEntryPeriodAndGatewayRemainIndependent(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tracker, _, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	tracker.state.Egress = map[string]model.GatewayTrafficState{"exit-1": {TX: 100, RX: 200, PeriodStartedAt: now.AddDate(0, 0, -5)}}
	if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err != nil {
		t.Fatal(err)
	}
	a := tracker.NetworkState(EntryNetworkScope)
	if a.RX != 21*240 || a.TX != 21*480 {
		t.Fatalf("A15=%+v", a)
	}
	if err := tracker.ResetGateway(ctx, "exit-1"); err != nil {
		t.Fatal(err)
	}
	if got := tracker.NetworkState(EntryNetworkScope); got.RX != a.RX || got.TX != a.TX {
		t.Fatalf("exit reset changed vnStat: %+v", got)
	}
	if _, err := tracker.ApplySample(ctx, 1<<40, 1<<40); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded {
		t.Fatal("reference counter enforced quota")
	}
	if err := tracker.Persist(); err != nil {
		t.Fatal(err)
	}
	h, err := tracker.NetworkHistory(ctx, "entry", "month", "2026-10", "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Rows) != 1 || h.Rows[0].NetworkTotal != 5*720 || h.Rows[0].ProxyUsedBytes != 2<<40 || !h.Rows[0].ProxyAvailable {
		t.Fatalf("history=%+v", h)
	}
}
func TestVnStatFailureRecoveryAndIdempotentPersistence(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tracker, reader, dir := newNetworkTest(t, &now)
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	old := tracker.NetworkState("entry")
	reader.fail["entry"] = true
	now = now.AddDate(0, 0, 2)
	if err := tracker.SampleNetwork(ctx, "entry"); err == nil {
		t.Fatal("expected unavailable")
	}
	if st := tracker.NetworkState("entry"); st.TX != old.TX || st.Status != "interrupted" {
		t.Fatalf("lost saved total: %+v", st)
	}
	// Roll back the entire raw bucket + checkpoint transaction, then retry.
	if _, err := tracker.history.db.Exec(`CREATE TRIGGER reject_network_checkpoint BEFORE UPDATE ON checkpoint BEGIN SELECT RAISE(ABORT,'fault'); END`); err != nil {
		t.Fatal(err)
	}
	reader.fail["entry"] = false
	reader.snapshots["entry"] = networkFixture(now)
	if err := tracker.SampleNetwork(ctx, "entry"); err == nil {
		t.Fatal("expected persistence fault")
	}
	var count int
	if err := tracker.history.db.QueryRow(`SELECT COUNT(*) FROM network_bucket WHERE start>=?`, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("raw writes survived rollback: %d", count)
	}
	if _, err := tracker.history.db.Exec(`DROP TRIGGER reject_network_checkpoint`); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	state := tracker.State()
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), tracker.config, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.NetworkReader = reader
	if err := reopened.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if got := reopened.NetworkState("entry"); got.RX != state.Network["entry"].RX || got.TX != state.Network["entry"].TX {
		t.Fatalf("replay doubled usage: %+v", got)
	}
}
func TestVnStatQuotaUsesTXOrRXPlusTXWithoutMultiplier(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tracker, _, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	cfg := tracker.config.(*configSource)
	cfg.cfg.TrafficQuota = model.TrafficQuotaConfig{Amount: 0.000012, Unit: "GB", BillingMode: "single", HeadroomPercent: 0}
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded {
		t.Fatal("RX included in TX-only plan")
	}
	cfg.cfg.TrafficQuota.BillingMode = "bidirectional"
	if err := tracker.ReconcileQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if !tracker.State().QuotaExceeded {
		t.Fatal("RX+TX threshold not enforced")
	}
	if err := tracker.resetNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded || tracker.NetworkState("entry").TX != 0 {
		t.Fatal("manual billing reset failed")
	}
	h, err := tracker.NetworkHistory(ctx, "entry", "day", "2026-10-01", "2026-10-05")
	if err != nil || len(h.Rows) != 5 {
		t.Fatalf("reset erased history: %+v %v", h, err)
	}
}

func TestVnStatResetAcrossOutageAndDatabaseRecreation(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	tracker, reader, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	reader.fail["entry"] = true
	now = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if err := tracker.SampleNetwork(ctx, "entry"); err == nil {
		t.Fatal("outage expected")
	}
	reader.fail["entry"] = false
	reader.snapshots["entry"] = networkFixture(now)
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	old := tracker.NetworkState("entry")
	// The external DB was recreated after the last saved snapshot. Preserve the
	// known period total, add new lifetime bytes, and expose the lost interval.
	created := now.Add(time.Hour)
	now = now.Add(2 * time.Hour)
	reader.snapshots["entry"] = nettraffic.Snapshot{Interface: "ens5", CreatedAt: created, UpdatedAt: now, RX: 7, TX: 11, Buckets: []nettraffic.Bucket{{Start: created.Unix(), Seconds: 3600, RX: 7, TX: 11}}}
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState("entry"); st.RX != old.RX+7 || st.TX != old.TX+11 || !st.Partial {
		t.Fatalf("recreation lost usage: %+v", st)
	}
	// A's own next reset starts a fresh period; it must not carry the old total.
	now = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	s := reader.snapshots["entry"]
	s.UpdatedAt = now
	s.RX += 100
	s.TX += 200
	reader.snapshots["entry"] = s
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState("entry"); st.RX != 0 || st.TX != 0 || st.PeriodStartedAt.Day() != 15 {
		t.Fatalf("A reset retained old total: %+v", st)
	}
}
func TestVnStatRetainedBucketsSurviveSourceRetention(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tracker, reader, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	old := tracker.NetworkState("entry")
	now = now.AddDate(0, 0, 1)
	s := networkFixture(now)
	// The source discarded old daily buckets; SBM's history copy is retained.
	s.Buckets = s.Buckets[len(s.Buckets)-1:]
	reader.snapshots["entry"] = s
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState("entry"); st.RX != old.RX+240 || st.TX != old.TX+480 {
		t.Fatalf("retention lost current-period bytes: %+v", st)
	}
	h, err := tracker.NetworkHistory(ctx, "entry", "month", "2026-09", "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Rows) != 1 || h.Rows[0].NetworkTotal != 30*720 {
		t.Fatalf("retention lost history: %+v", h)
	}
}

func TestVnStatMissingDayMarksMonthlyTotalPartial(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tracker, reader, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	missing := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Unix()
	s := reader.snapshots["entry"]
	var retained []nettraffic.Bucket
	for _, b := range s.Buckets {
		if b.Start != missing {
			retained = append(retained, b)
		}
	}
	s.Buckets = retained
	reader.snapshots["entry"] = s
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	h, err := tracker.NetworkHistory(ctx, "entry", "month", "2026-10", "2026-10")
	if err != nil || len(h.Rows) != 1 || h.Rows[0].NetworkTotal != 4*720 || !h.Rows[0].NetworkPartial {
		t.Fatalf("missing day hidden in monthly total: %+v %v", h, err)
	}
	h, err = tracker.NetworkHistory(ctx, "entry", "day", "2026-10-02", "2026-10-02")
	if err != nil || len(h.Rows) != 1 || h.Rows[0].NetworkAvailable || !h.Rows[0].NetworkPartial || h.Rows[0].ProxyAvailable {
		t.Fatalf("missing day looks like recorded zero: %+v %v", h, err)
	}
}

func TestVnStatBackwardBucketCannotEraseSavedHistory(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	tracker, reader, _ := newNetworkTest(t, &now)
	defer tracker.Close()
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	previous := tracker.NetworkState("entry")
	s := reader.snapshots["entry"]
	s.Buckets = append([]nettraffic.Bucket(nil), s.Buckets...)
	s.Buckets[len(s.Buckets)-1].TX = 0
	reader.snapshots["entry"] = s
	if err := tracker.SampleNetwork(ctx, "entry"); err == nil {
		t.Fatal("backward source bucket accepted")
	}
	if st := tracker.NetworkState("entry"); st.TX != previous.TX || st.Status != SampleStatusInterrupted {
		t.Fatalf("previous usage lost: %+v", st)
	}
	h, err := tracker.NetworkHistory(ctx, "entry", "month", "2026-10", "2026-10")
	if err != nil || len(h.Rows) != 1 || h.Rows[0].NetworkTotal != 5*720 || h.Rows[0].ProxyAvailable {
		t.Fatalf("backward source erased history: %+v %v", h, err)
	}
}
