package traffic

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

func TestVnStatFreshInstallIgnoresEarlierSourceUsage(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 15, Timezone: "UTC"}
	dir := t.TempDir()
	source := &configSource{cfg: cfg}
	tracker, err := OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), source, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	reader := &networkFake{snapshots: map[string]nettraffic.Snapshot{"entry": networkFixture(now)}}
	tracker.NetworkReader = reader
	ctx := context.Background()
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState("entry"); st.RX != 0 || st.TX != 0 || !st.RecordedFrom.Equal(now) {
		t.Fatalf("imported earlier usage: %+v", st)
	}
	now = now.AddDate(0, 0, 1)
	reader.snapshots["entry"] = networkFixture(now)
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState("entry"); st.RX != 240 || st.TX != 480 {
		t.Fatalf("new usage lost: %+v", st)
	}
	h, err := tracker.NetworkHistory(ctx, "entry", "day", "2026-10-01", "2026-10-06")
	if err != nil || len(h.Rows) != 1 || h.Rows[0].Date != "2026-10-06" || h.Rows[0].NetworkTotal != 720 {
		t.Fatalf("history imported pre-install days: %+v %v", h, err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	tracker, err = OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), source, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	tracker.NetworkReader = reader
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if tracker.NetworkState("entry").TX != 480 {
		t.Fatal("restart changed baseline")
	}
	// Changing a plan schedule must not import the pre-activation prefix.
	source.cfg.Reset.Day = 1
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState("entry"); st.RX != 240 || st.TX != 480 {
		t.Fatalf("schedule change imported earlier usage: %+v", st)
	}
	source.cfg.Reset.Day = 15
	now = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	reader.snapshots["entry"] = networkFixture(now)
	if err := tracker.SampleNetwork(ctx, "entry"); err != nil {
		t.Fatal(err)
	}
	if st := tracker.NetworkState("entry"); st.RX != 240 || st.TX != 480 || st.PeriodStartedAt.Day() != 15 {
		t.Fatalf("monthly reset incorrect: %+v", st)
	}
}

func TestVnStatFirstBucketBaselineSurvivesFailedWrite(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 2, 0, 0, time.UTC)
	dir := t.TempDir()
	cfg := &configSource{cfg: model.DefaultConfig()}
	tracker, err := OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), cfg, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	s := nettraffic.Snapshot{Interface: "ens5", CreatedAt: day, UpdatedAt: now, RX: 100, TX: 200, Buckets: []nettraffic.Bucket{
		{Start: day.Unix(), Seconds: 86400, RX: 100, TX: 200},
		{Start: day.Add(12 * time.Hour).Unix(), Seconds: 3600, RX: 100, TX: 200},
		{Start: day.Add(12 * time.Hour).Unix(), Seconds: 300, RX: 100, TX: 200},
	}}
	reader := &networkFake{snapshots: map[string]nettraffic.Snapshot{"entry": s}}
	tracker.NetworkReader = reader
	if _, err := tracker.history.db.Exec(`CREATE TRIGGER reject_first BEFORE UPDATE ON checkpoint BEGIN SELECT RAISE(ABORT,'fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(context.Background(), "entry"); err == nil {
		t.Fatal("expected first-write failure")
	}
	if _, err := tracker.history.db.Exec(`DROP TRIGGER reject_first`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	s.UpdatedAt = now
	s.RX += 10
	s.TX += 20
	for i := range s.Buckets {
		s.Buckets[i].RX += 10
		s.Buckets[i].TX += 20
	}
	reader.snapshots["entry"] = s
	if err := tracker.SampleNetwork(context.Background(), "entry"); err != nil {
		t.Fatal(err)
	}
	h, err := tracker.NetworkHistory(context.Background(), "entry", "day", "2026-10-06", "2026-10-06")
	if err != nil || len(h.Rows) != 1 || h.Rows[0].NetworkTotal != 30 {
		t.Fatalf("initial prefix lost on retry: %+v %v", h, err)
	}
}
