package nettraffic

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseOfficialJSONAndRejectInvalidCounters(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	raw := `{"jsonversion":"2","interfaces":[{"name":"ens5","created":{"timestamp":1790812800},"updated":{"timestamp":1791288000},"traffic":{"total":{"rx":100,"tx":200},"day":[{"timestamp":1791244800,"rx":10,"tx":20}],"hour":[],"fiveminute":[]}}]}`
	s, err := Parse([]byte(raw), "ens5", now)
	if err != nil {
		t.Fatal(err)
	}
	if s.RX != 100 || s.TX != 200 || len(s.Buckets) != 1 {
		t.Fatalf("snapshot=%+v", s)
	}
	for _, broken := range []string{strings.Replace(raw, `"2"`, `"1"`, 1), strings.Replace(raw, `"rx":100`, `"rx":-1`, 1), strings.Replace(raw, `"rx":100`, `"rx":9223372036854775807`, 1), strings.Replace(raw, `"rx":100`, `"absent":100`, 1)} {
		if _, err := Parse([]byte(broken), "ens5", now); err == nil {
			t.Fatalf("accepted invalid JSON: %s", broken)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(raw), "wg0", now); err == nil {
		t.Fatal("wrong interface selected")
	}
}
func TestSumIndependentBillingDatesAndBoundaryHours(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := Snapshot{CreatedAt: start, UpdatedAt: start.AddDate(0, 1, 5)}
	for d := 0; d < 35; d++ {
		at := start.AddDate(0, 0, d)
		s.Buckets = append(s.Buckets, Bucket{at.Unix(), 86400, 24, 48})
		for h := 0; h < 24; h++ {
			s.Buckets = append(s.Buckets, Bucket{at.Add(time.Duration(h) * time.Hour).Unix(), 3600, 1, 2})
		}
	}
	aStart := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	aRX, aTX, p := Sum(s, aStart, s.UpdatedAt)
	if p || aRX != 21*24 || aTX != 21*48 {
		t.Fatalf("A15=%d/%d partial=%v", aRX, aTX, p)
	}
	bStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	bRX, bTX, p := Sum(s, bStart, s.UpdatedAt)
	if p || bRX != 5*24 || bTX != 5*48 {
		t.Fatalf("B1=%d/%d partial=%v", bRX, bTX, p)
	}
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	boundary := time.Date(2026, 10, 1, 0, 0, 0, 0, shanghai)
	rx, tx, p := Sum(s, boundary, s.UpdatedAt)
	if p || rx != 5*24+8 || tx != (5*24+8)*2 {
		t.Fatalf("timezone boundary=%d/%d partial=%v", rx, tx, p)
	}
	// Remove boundary hours: do not invent a fraction of a UTC day.
	onlyDays := s
	onlyDays.Buckets = nil
	for _, b := range s.Buckets {
		if b.Seconds == 86400 {
			onlyDays.Buckets = append(onlyDays.Buckets, b)
		}
	}
	rx, _, p = Sum(onlyDays, boundary, s.UpdatedAt)
	if !p || rx != 5*24 {
		t.Fatalf("missing boundary=%d partial=%v", rx, p)
	}
}
func TestSumFiveMinuteBoundaryAndCreation(t *testing.T) {
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	s := Snapshot{CreatedAt: day, UpdatedAt: day.Add(time.Hour), Buckets: []Bucket{{day.Unix(), 3600, 12, 24}}}
	for i := 0; i < 12; i++ {
		s.Buckets = append(s.Buckets, Bucket{day.Add(time.Duration(i) * 5 * time.Minute).Unix(), 300, 1, 2})
	}
	rx, tx, p := Sum(s, day.Add(15*time.Minute), day.Add(time.Hour))
	if p || rx != 9 || tx != 18 {
		t.Fatalf("five minute=%d/%d %v", rx, tx, p)
	}
	_, _, p = Sum(s, day.Add(time.Minute), day.Add(time.Hour))
	if !p {
		t.Fatal("sub-bucket split must be marked partial")
	}
	rx, tx, p = Sum(s, day.Add(-time.Hour), day.Add(time.Hour))
	if !p || rx != 12 || tx != 24 {
		t.Fatalf("creation=%d/%d %v", rx, tx, p)
	}
	if !validCounters(0, math.MaxInt64) || validCounters(1, math.MaxInt64) {
		t.Fatal("counter overflow validation")
	}
}
