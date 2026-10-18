package traffic

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

// vnStatSnapshot builds a UTC vnStat source with perSlot TX bytes in every
// five-minute slot from created to updated, keeping five-minute rows for 48
// hours only, as the installer configures.
func vnStatSnapshot(created, updated time.Time, perSlot int64) nettraffic.Snapshot {
	s := nettraffic.Snapshot{Interface: "ens5", CreatedAt: created, UpdatedAt: updated}
	days, hours := map[int64]int64{}, map[int64]int64{}
	for at := created; at.Before(updated); at = at.Add(5 * time.Minute) {
		s.TX += perSlot
		days[at.Truncate(24*time.Hour).Unix()] += perSlot
		hours[at.Truncate(time.Hour).Unix()] += perSlot
		if updated.Sub(at) <= 48*time.Hour {
			s.Buckets = append(s.Buckets, nettraffic.Bucket{Start: at.Unix(), Seconds: 300, TX: perSlot})
		}
	}
	for start, tx := range days {
		s.Buckets = append(s.Buckets, nettraffic.Bucket{Start: start, Seconds: 86400, TX: tx})
	}
	for start, tx := range hours {
		s.Buckets = append(s.Buckets, nettraffic.Bucket{Start: start, Seconds: 3600, TX: tx})
	}
	return s
}

func TestHalfHourResetBoundarySurvivesFiveMinuteRetention(t *testing.T) {
	// 00:00 in Asia/Kolkata is 18:30 UTC: the period starts mid-way through
	// an hourly vnStat row.
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cfg := model.DefaultConfig()
	cfg.VnStatInterface = "ens5"
	cfg.Reset = model.ResetConfig{Mode: "monthly", Day: 17, Timezone: "Asia/Kolkata"}
	dir := t.TempDir()
	tracker, err := OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), &configSource{cfg: cfg}, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	reader := &networkFake{snapshots: map[string]nettraffic.Snapshot{}, fail: map[string]bool{}}
	tracker.NetworkReader = reader
	ctx := context.Background()
	periodStart := time.Date(2026, 10, 16, 18, 30, 0, 0, time.UTC)
	// SBM samples every few hours, more than 48 hours past the boundary.
	for ; !now.After(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)); now = now.Add(5 * time.Hour) {
		reader.snapshots["entry"] = vnStatSnapshot(created, now, 10)
		if err := tracker.SampleNetwork(ctx, EntryNetworkScope); err != nil {
			t.Fatal(err)
		}
	}
	st := tracker.NetworkState(EntryNetworkScope)
	last := reader.snapshots["entry"].UpdatedAt
	want := int64(last.Sub(periodStart)/(5*time.Minute)) * 10
	if !st.PeriodStartedAt.Equal(periodStart) || st.TX != want || st.Partial {
		t.Fatalf("period %s TX=%d partial=%v, want %s TX=%d", st.PeriodStartedAt, st.TX, st.Partial, periodStart, want)
	}
}
