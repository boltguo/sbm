package nettraffic

import (
	"context"
	"os"
	"testing"
)

// Opt-in integration uses real vnstatd/veth counters in a disposable Linux
// namespace, without changing the host's interfaces or source database.
func TestNativeVnStat(t *testing.T) {
	config := os.Getenv("SBM_VNSTAT_TEST_CONFIG")
	if config == "" {
		t.Skip("requires an isolated Linux vnStat fixture")
	}
	reader := Collector{ConfigPath: config}
	var snapshots []Snapshot
	for _, iface := range []string{"stat0", "stat1", "stat2"} {
		s, err := reader.Read(context.Background(), Request{Interface: iface})
		if err != nil {
			t.Fatal(err)
		}
		if s.Interface != iface || s.RX <= 0 || s.TX <= 0 {
			t.Fatalf("missing real interface counters: %+v", s)
		}
		snapshots = append(snapshots, s)
	}
	difference := func(a, b int64) int64 {
		if a > b {
			return a - b
		}
		return b - a
	}
	if difference(snapshots[0].TX, snapshots[1].RX) > 2048 || difference(snapshots[0].RX, snapshots[1].TX) > 2048 {
		t.Fatalf("veth peer counters differ: %+v", snapshots)
	}
	if snapshots[2].RX <= snapshots[0].RX {
		t.Fatal("interfaces were merged")
	}
	t.Logf("real vnStat A RX=%d TX=%d, peer RX=%d TX=%d; second pair RX=%d TX=%d", snapshots[0].RX, snapshots[0].TX, snapshots[1].RX, snapshots[1].TX, snapshots[2].RX, snapshots[2].TX)
}
