package nettraffic

import (
	"strings"
	"testing"
	"time"
)

func TestVnStat29JSONRequiresUpgradeDespiteAPIVersion2(t *testing.T) {
	// vnStat 2.9 exposes date/time labels but no epoch timestamps in API v2.
	raw := `{"vnstatversion":"2.9","jsonversion":"2","interfaces":[{"name":"ens5","created":{"date":{"year":2026,"month":10,"day":1}},"updated":{"date":{"year":2026,"month":10,"day":6},"time":{"hour":12,"minute":0}},"traffic":{"total":{"rx":100,"tx":200},"day":[],"hour":[],"fiveminute":[]}}]}`
	if _, err := Parse([]byte(raw), "ens5", time.Now()); err == nil || !strings.Contains(err.Error(), "2.10") {
		t.Fatalf("missing explicit version rejection: %v", err)
	}
}
