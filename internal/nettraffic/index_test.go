package nettraffic

import (
	"testing"
	"time"
)

func yearSnapshot() Snapshot {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	s := Snapshot{CreatedAt: start, UpdatedAt: start.AddDate(1, 0, 0)}
	for _, seconds := range []int64{86400, 3600, 300} {
		for at := start.Unix(); at < s.UpdatedAt.Unix(); at += seconds {
			s.Buckets = append(s.Buckets, Bucket{Start: at, Seconds: seconds, RX: seconds, TX: seconds * 2})
		}
	}
	return s
}

func TestIndexYearAndBoundaryFragments(t *testing.T) {
	s := yearSnapshot()
	index := NewIndex(s)
	var rx, tx int64
	for at := s.CreatedAt; at.Before(s.UpdatedAt); at = at.AddDate(0, 0, 1) {
		end := at.AddDate(0, 0, 1)
		r, tBytes, partial := index.Sum(at, end)
		if partial || !index.HasCoverage(at, end) {
			t.Fatal("complete day lost")
		}
		rx += r
		tx += tBytes
	}
	seconds := int64(s.UpdatedAt.Sub(s.CreatedAt) / time.Second)
	if rx != seconds || tx != 2*seconds {
		t.Fatalf("overlapping resolutions counted twice: %d/%d", rx, tx)
	}
	start, end := s.CreatedAt.Add(5*time.Minute), s.CreatedAt.Add(65*time.Minute)
	r, tBytes, partial := index.Sum(start, end)
	if partial || r != 3600 || tBytes != 7200 {
		t.Fatalf("boundary fragments: %d/%d partial=%v", r, tBytes, partial)
	}
}

func BenchmarkIndexedYearHistory(b *testing.B) {
	s := yearSnapshot()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index := NewIndex(s)
		for at := s.CreatedAt; at.Before(s.UpdatedAt); at = at.AddDate(0, 0, 1) {
			index.Sum(at, at.AddDate(0, 0, 1))
		}
	}
}
