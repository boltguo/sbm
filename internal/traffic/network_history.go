package traffic

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

func (h *historyStore) loadNetwork(ctx context.Context, scope, generation string) (nettraffic.Snapshot, error) {
	return readNetwork(ctx, h.db, scope, generation)
}

type networkQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readNetwork(ctx context.Context, db networkQuerier, scope, generation string) (nettraffic.Snapshot, error) {
	return readNetworkRows(ctx, db, scope, generation, false)
}
func readNetworkRows(ctx context.Context, db networkQuerier, scope, generation string, baseline bool) (nettraffic.Snapshot, error) {
	result := nettraffic.Snapshot{}
	query := `SELECT start, seconds, rx, tx FROM network_bucket WHERE scope=? AND generation=?`
	if baseline {
		query = `SELECT start, seconds, rx, tx FROM network_baseline WHERE scope=? AND generation=?`
	}
	rows, err := db.QueryContext(ctx, query, scope, generation)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var b nettraffic.Bucket
		if err := rows.Scan(&b.Start, &b.Seconds, &b.RX, &b.TX); err != nil {
			return result, err
		}
		result.Buckets = append(result.Buckets, b)
	}
	return result, rows.Err()
}

func networkSince(snapshot nettraffic.Snapshot, available time.Time, baseline []nettraffic.Bucket) nettraffic.Snapshot {
	if available.After(snapshot.CreatedAt) {
		snapshot.CreatedAt = available
	}
	snapshot.Buckets = append([]nettraffic.Bucket(nil), snapshot.Buckets...)
	prefix := map[[2]int64]nettraffic.Bucket{}
	for _, b := range baseline {
		prefix[[2]int64{b.Start, b.Seconds}] = b
	}
	for i := range snapshot.Buckets {
		b := &snapshot.Buckets[i]
		if old, ok := prefix[[2]int64{b.Start, b.Seconds}]; ok {
			b.RX = max(0, b.RX-old.RX)
			b.TX = max(0, b.TX-old.TX)
		}
	}
	return snapshot
}
func saveNetworks(tx *sql.Tx, pending map[string]networkRecord) error {
	for scope, r := range pending {
		s := r.Snapshot
		// Changing a source closes the previous timeline without deleting it.
		if _, err := tx.Exec(`UPDATE network_source SET ended_at=? WHERE scope=? AND generation<>? AND ended_at=0`, r.AvailableFrom.Unix(), scope, r.Generation); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO network_source(scope,generation,interface,created_at,updated_at,available_from,ended_at) VALUES(?,?,?,?,?,?,0)
    ON CONFLICT(scope,generation) DO UPDATE SET updated_at=excluded.updated_at`, scope, r.Generation, s.Interface, s.CreatedAt.Unix(), s.UpdatedAt.Unix(), r.AvailableFrom.Unix()); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO network_bucket VALUES(?,?,?,?,?,?) ON CONFLICT(scope,generation,start,seconds) DO UPDATE SET rx=excluded.rx,tx=excluded.tx WHERE network_bucket.rx<>excluded.rx OR network_bucket.tx<>excluded.tx`)
		if err != nil {
			return err
		}
		for _, b := range s.Buckets {
			if _, err := stmt.Exec(scope, r.Generation, b.Start, b.Seconds, b.RX, b.TX); err != nil {
				stmt.Close()
				return err
			}
		}
		if err := stmt.Close(); err != nil {
			return err
		}
		for _, b := range r.Baseline {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO network_baseline VALUES(?,?,?,?,?,?)`, scope, r.Generation, b.Start, b.Seconds, b.RX, b.TX); err != nil {
				return err
			}
		}
	}
	return nil
}

// NetworkHistory keeps NIC bytes as the primary columns and attaches sing-box
// totals as a small reference. Legacy records never become invented NIC bytes.
func (t *Tracker) NetworkHistory(ctx context.Context, scope, granularity, from, to string) (HistoryResponse, error) {
	if t.history == nil {
		return HistoryResponse{}, ErrHistoryDisabled
	}
	if scope == "" {
		scope = EntryNetworkScope
	}
	h := t.history
	result := HistoryResponse{Granularity: granularity, Timezone: h.location.String(), StartedAt: h.startedAt, Rows: []Usage{}, Imports: []ImportedUsage{}, Source: "vnstat", Scope: scope}
	if scope == EntryNetworkScope {
		var err error
		result, err = h.query(ctx, granularity, from, to, t.now())
		if err != nil {
			return result, err
		}
		result.Source = "vnstat"
		result.Scope = scope
		result.UpdatedAt = time.Time{}
	}
	start, end, err := historyRange(granularity, from, to, h.location, t.now())
	if err != nil {
		return result, err
	}
	tx, err := h.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	sources, err := tx.QueryContext(ctx, `SELECT generation,interface,created_at,updated_at,available_from,ended_at FROM network_source WHERE scope=? ORDER BY available_from,generation`, scope)
	if err != nil {
		return result, err
	}
	type source struct {
		generation, iface                  string
		created, updated, available, ended int64
	}
	var all []source
	for sources.Next() {
		var s source
		if err := sources.Scan(&s.generation, &s.iface, &s.created, &s.updated, &s.available, &s.ended); err != nil {
			sources.Close()
			return result, err
		}
		all = append(all, s)
	}
	err = sources.Err()
	sources.Close()
	if err != nil {
		return result, err
	}
	if len(all) == 0 {
		result.Rows = []Usage{}
		result.Imports = []ImportedUsage{}
		return result, tx.Commit()
	}
	result.StartedAt = time.Unix(all[0].available, 0).UTC()
	result.Imports = []ImportedUsage{}
	type preparedSource struct {
		snapshot         nettraffic.Snapshot
		available, ended time.Time
	}
	prepared := make([]preparedSource, 0, len(all))
	for _, source := range all {
		snapshot, err := readNetwork(ctx, tx, scope, source.generation)
		if err != nil {
			return result, err
		}
		snapshot.Interface = source.iface
		snapshot.CreatedAt = time.Unix(source.created, 0).UTC()
		snapshot.UpdatedAt = time.Unix(source.updated, 0).UTC()
		baseline, err := readNetworkRows(ctx, tx, scope, source.generation, true)
		if err != nil {
			return result, err
		}
		snapshot = networkSince(snapshot, time.Unix(source.available, 0), baseline.Buckets)
		if snapshot.UpdatedAt.After(result.UpdatedAt) {
			result.UpdatedAt = snapshot.UpdatedAt
		}
		prepared = append(prepared, preparedSource{snapshot, time.Unix(source.available, 0), time.Unix(source.ended, 0)})
	}
	buckets := map[string]Usage{}
	firstKey := result.StartedAt.In(h.location).Format(time.DateOnly)
	if granularity == "month" {
		firstKey = firstKey[:7]
	}
	for _, row := range result.Rows {
		if row.Date < firstKey {
			continue
		}
		row.ProxyAvailable = true
		buckets[row.Date] = row
	}
	// Visit every recorded day, including days outside all source intervals.
	// Checking each source separately would hide gaps between generations.
	for at := start; at.Before(end); at = at.AddDate(0, 0, 1) {
		next := at.AddDate(0, 0, 1)
		left, right := at, next
		if left.Before(result.StartedAt) {
			left = result.StartedAt
		}
		if right.After(t.now()) {
			right = t.now()
		}
		if !right.After(left) {
			continue
		}
		key := at.Format(time.DateOnly)
		if granularity == "month" {
			key = key[:7]
		}
		row := buckets[key]
		row.Date = key
		row.NetworkPartial = row.NetworkPartial || left.After(at) || right.Before(next)
		type interval struct{ left, right time.Time }
		var coverage []interval
		for _, source := range prepared {
			a, z := left, right
			if a.Before(source.available) {
				a = source.available
			}
			if source.ended.Unix() > 0 && z.After(source.ended) {
				z = source.ended
			}
			if z.After(source.snapshot.UpdatedAt) {
				z = source.snapshot.UpdatedAt
			}
			if !z.After(a) || !nettraffic.HasCoverage(source.snapshot, a, z) {
				continue
			}
			rx, txBytes, partial := nettraffic.Sum(source.snapshot, a, z)
			row.NetworkAvailable = true
			row.NetworkRX = addSaturating(row.NetworkRX, rx)
			row.NetworkTX = addSaturating(row.NetworkTX, txBytes)
			row.NetworkPartial = row.NetworkPartial || partial
			coverage = append(coverage, interval{a, z})
		}
		sort.Slice(coverage, func(i, j int) bool { return coverage[i].left.Before(coverage[j].left) })
		covered := left
		for _, interval := range coverage {
			if interval.left.After(covered) {
				row.NetworkPartial = true
			}
			if interval.right.After(covered) {
				covered = interval.right
			}
		}
		if covered.Before(right) {
			row.NetworkPartial = true
		}
		buckets[key] = row
	}
	result.Rows = nil
	for _, row := range buckets {
		row.NetworkTotal = addSaturating(row.NetworkRX, row.NetworkTX)
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Date < result.Rows[j].Date })
	return result, tx.Commit()
}
func (t *Tracker) NetworkState(id string) model.NetworkTrafficState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state.Network[id]
}
