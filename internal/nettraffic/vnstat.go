// Package nettraffic reads vnStat's byte counters without modifying its database.
package nettraffic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Bucket struct {
	Start   int64 `json:"start"`
	Seconds int64 `json:"seconds"`
	RX      int64 `json:"rx"`
	TX      int64 `json:"tx"`
}
type Snapshot struct {
	Interface string
	CreatedAt time.Time
	UpdatedAt time.Time
	RX, TX    int64
	Buckets   []Bucket
}
type Request struct {
	Interface string
}
type Reader interface {
	Read(context.Context, Request) (Snapshot, error)
}
type Collector struct{ ConfigPath string }

func (c Collector) Read(ctx context.Context, request Request) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if request.Interface == "" {
		request.Interface = defaultInterface()
	}
	if request.Interface == "" {
		return Snapshot{}, errors.New("select the public network interface")
	}
	if !ValidInterface(request.Interface) {
		return Snapshot{}, errors.New("invalid network interface")
	}
	config := c.ConfigPath
	if config == "" {
		config = "/etc/vnstat.conf"
	}
	args := []string{"--config", config, "--json", "--limit", "0", "-i", request.Interface}
	cmd := exec.CommandContext(ctx, "vnstat", args...)
	var out limitedBuffer
	cmd.Stdout = &out
	// Keep daemon/database diagnostics out of responses sent to the panel.
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Snapshot{}, errors.New("vnStat 2 is not installed")
		}
		return Snapshot{}, errors.New("cannot read vnStat; check its service and database")
	}
	return Parse(out.Bytes(), request.Interface, time.Now())
}

type limitedBuffer struct{ buffer bytes.Buffer }

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 16<<20 {
		return 0, errors.New("vnStat response is too large")
	}
	return b.buffer.Write(p)
}
func ValidInterface(s string) bool {
	if len(s) == 0 || len(s) > 64 || s == "lo" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c)) {
			return false
		}
	}
	return true
}
func defaultInterface() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	metric := int64(math.MaxInt64)
	iface := ""
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, e := strconv.ParseInt(f[3], 16, 64)
		if e != nil || flags&1 == 0 {
			continue
		}
		m, e := strconv.ParseInt(f[6], 10, 64)
		if e == nil && m < metric {
			metric, iface = m, f[0]
		}
	}
	return iface
}

// Parse accepts only JSON API v2 (bytes). Epoch timestamps are used rather than
// the CLI's rendered calendar labels, which depend on its process timezone.
func Parse(raw []byte, iface string, now time.Time) (Snapshot, error) {
	type counter struct {
		Timestamp int64 `json:"timestamp"`
		RX, TX    *int64
	}
	var doc struct {
		Version    string `json:"jsonversion"`
		Interfaces []struct {
			Name             string
			Created, Updated struct{ Timestamp int64 }
			Traffic          struct {
				Total                 counter
				Day, Hour, Fiveminute []counter
			}
		}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Snapshot{}, errors.New("invalid vnStat JSON")
	}
	if doc.Version != "2" {
		return Snapshot{}, errors.New("vnStat JSON API v2 is required")
	}
	var result Snapshot
	found := false
	for _, in := range doc.Interfaces {
		if in.Name != iface {
			continue
		}
		if found {
			return Snapshot{}, errors.New("duplicate vnStat interface")
		}
		found = true
		if in.Created.Timestamp == 0 || in.Updated.Timestamp == 0 {
			return Snapshot{}, errors.New("vnStat >= 2.10 with JSON timestamps is required")
		}
		if in.Created.Timestamp <= 0 || in.Updated.Timestamp < in.Created.Timestamp || in.Updated.Timestamp > now.Add(time.Minute).Unix() {
			return Snapshot{}, errors.New("invalid vnStat timestamps")
		}
		result = Snapshot{Interface: iface, CreatedAt: time.Unix(in.Created.Timestamp, 0).UTC(), UpdatedAt: time.Unix(in.Updated.Timestamp, 0).UTC()}
		if in.Traffic.Total.RX == nil || in.Traffic.Total.TX == nil {
			return Snapshot{}, errors.New("missing vnStat total")
		}
		result.RX, result.TX = *in.Traffic.Total.RX, *in.Traffic.Total.TX
		if !validCounters(result.RX, result.TX) {
			return Snapshot{}, errors.New("invalid vnStat total")
		}
		for _, series := range []struct {
			rows    []counter
			seconds int64
		}{{in.Traffic.Day, 86400}, {in.Traffic.Hour, 3600}, {in.Traffic.Fiveminute, 300}} {
			seen := map[int64]bool{}
			var seriesRX, seriesTX int64
			for _, row := range series.rows {
				if row.RX == nil || row.TX == nil || !validCounters(*row.RX, *row.TX) || row.Timestamp <= 0 || row.Timestamp > result.UpdatedAt.Unix() || seen[row.Timestamp] {
					return Snapshot{}, errors.New("invalid vnStat bucket")
				}
				if *row.RX > result.RX-seriesRX || *row.TX > result.TX-seriesTX {
					return Snapshot{}, errors.New("vnStat buckets exceed lifetime totals")
				}
				seriesRX += *row.RX
				seriesTX += *row.TX
				seen[row.Timestamp] = true
				// Old local-time daily buckets cannot be silently relabelled as UTC.
				// Fine-grained records can still supply the affected boundary hours.
				if row.Timestamp%series.seconds != 0 {
					continue
				}
				result.Buckets = append(result.Buckets, Bucket{row.Timestamp, series.seconds, *row.RX, *row.TX})
			}
		}
	}
	if !found {
		return Snapshot{}, fmt.Errorf("public interface %s is not monitored by vnStat", iface)
	}
	return result, nil
}
func validCounters(rx, tx int64) bool { return rx >= 0 && tx >= 0 && rx <= math.MaxInt64-tx }

// Sum returns available bytes and a coverage flag. A coarse bucket is included
// once when wholly covered; boundary fragments use hourly / five-minute data.
// Missing or unresolvable fragments are flagged, never prorated or invented.
func Sum(snapshot Snapshot, start, end time.Time) (rx, tx int64, partial bool) {
	return NewIndex(snapshot).Sum(start, end)
}

// Index reuses the bucket lookup across all days in a history query.
type Index struct {
	created, updated time.Time
	lookup           map[[2]int64]Bucket
	coverage         [][2]int64
}

func NewIndex(snapshot Snapshot) *Index {
	i := &Index{created: snapshot.CreatedAt, updated: snapshot.UpdatedAt, lookup: make(map[[2]int64]Bucket, len(snapshot.Buckets))}
	for _, b := range snapshot.Buckets {
		i.lookup[[2]int64{b.Seconds, b.Start}] = b
		left, right := max(b.Start, snapshot.CreatedAt.Unix()), min(b.Start+b.Seconds, snapshot.UpdatedAt.Unix())
		if right > left {
			i.coverage = append(i.coverage, [2]int64{left, right})
		}
	}
	sort.Slice(i.coverage, func(a, b int) bool { return i.coverage[a][0] < i.coverage[b][0] })
	return i
}

func (i *Index) HasCoverage(start, end time.Time) bool {
	at := sort.Search(len(i.coverage), func(n int) bool { return i.coverage[n][0] >= start.Unix() })
	for ; at < len(i.coverage) && i.coverage[at][0] < end.Unix(); at++ {
		if i.coverage[at][1] <= end.Unix() {
			return true
		}
	}
	return false
}

func (i *Index) Sum(start, end time.Time) (rx, tx int64, partial bool) {
	if !end.After(start) {
		return 0, 0, false
	}
	requested := start
	if start.Before(i.created) {
		start = i.created
		partial = true
	}
	if end.After(i.updated) {
		end = i.updated
	}
	if !end.After(start) {
		return 0, 0, partial || requested.Before(i.created) || i.updated.Before(requested)
	}
	var sum func(int64, int64, int) (int64, int64, bool)
	widths := []int64{86400, 3600, 300}
	sum = func(a, z int64, level int) (int64, int64, bool) {
		width := widths[level]
		var r, t int64
		p := false
		for at := a / width * width; at < z; at += width {
			left, right := max(at, a, i.created.Unix()), min(at+width, z, i.updated.Unix())
			if right <= left {
				continue
			}
			b, ok := i.lookup[[2]int64{width, at}]
			if ok && left == max(at, i.created.Unix()) && right == min(at+width, i.updated.Unix()) {
				r = saturate(r, b.RX)
				t = saturate(t, b.TX)
				continue
			}
			if level+1 < len(widths) {
				rr, tt, pp := sum(left, right, level+1)
				r = saturate(r, rr)
				t = saturate(t, tt)
				p = p || pp
			} else {
				p = true
			}
		}
		return r, t, p
	}
	r, t, p := sum(start.Unix(), end.Unix(), 0)
	return r, t, partial || p
}
func saturate(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// HasCoverage distinguishes a recorded zero-byte bucket from missing records.
func HasCoverage(snapshot Snapshot, start, end time.Time) bool {
	for _, b := range snapshot.Buckets {
		left := max(b.Start, snapshot.CreatedAt.Unix())
		right := min(b.Start+b.Seconds, snapshot.UpdatedAt.Unix())
		if right > left && left >= start.Unix() && right <= end.Unix() {
			return true
		}
	}
	return false
}
