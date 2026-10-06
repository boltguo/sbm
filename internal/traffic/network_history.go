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
	return readNetworkWindow(ctx, db, scope, generation, baseline, time.Time{}, time.Time{})
}
func readNetworkWindow(ctx context.Context, db networkQuerier, scope, generation string, baseline bool, start, end time.Time) (nettraffic.Snapshot, error) {
	result := nettraffic.Snapshot{}
	query := `SELECT start, seconds, rx, tx FROM network_bucket WHERE scope=? AND generation=?`
	if baseline {
		query = `SELECT start, seconds, rx, tx FROM network_baseline WHERE scope=? AND generation=?`
	}
	args := []any{scope, generation}
	if !start.IsZero() && !end.IsZero() {
		// vnStat's largest retained bucket is one day. Bound both sides of the
		// indexed start column instead of scanning every older row.
		query += ` AND start>=? AND start<? AND start+seconds>?`
		args = append(args, start.Unix()-86400, end.Unix(), start.Unix())
	}
	rows, err := db.QueryContext(ctx, query, args...)
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

type networkSource struct {
	generation       string
	snapshot         nettraffic.Snapshot
	available, ended time.Time
	index            *nettraffic.Index
}

// Release the read transaction before indexing and aggregating the buckets, so
// a long history query does not hold the only SQLite connection during its work.
func (h *historyStore) networkSources(ctx context.Context, scope, exclude string, start, end time.Time) ([]networkSource, time.Time, error) {
	tx, err := h.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, time.Time{}, err
	}
	defer tx.Rollback()
	var first sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MIN(available_from) FROM network_source WHERE scope=?`, scope).Scan(&first); err != nil {
		return nil, time.Time{}, err
	}
	started := time.Time{}
	if first.Valid {
		started = time.Unix(first.Int64, 0).UTC()
	}
	rows, err := tx.QueryContext(ctx, `SELECT generation,interface,created_at,updated_at,available_from,ended_at FROM network_source WHERE scope=? AND generation<>? AND available_from<? AND (ended_at=0 OR ended_at>?) ORDER BY available_from,generation`, scope, exclude, end.Unix(), start.Unix())
	if err != nil {
		return nil, started, err
	}
	var all []networkSource
	for rows.Next() {
		var s networkSource
		var created, updated, available, ended int64
		if err := rows.Scan(&s.generation, &s.snapshot.Interface, &created, &updated, &available, &ended); err != nil {
			rows.Close()
			return nil, started, err
		}
		s.snapshot.CreatedAt, s.snapshot.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		s.available, s.ended = time.Unix(available, 0).UTC(), time.Unix(ended, 0).UTC()
		all = append(all, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, started, err
	}
	for n := range all {
		s := &all[n]
		buckets, err := readNetworkWindow(ctx, tx, scope, s.generation, false, start, end)
		if err != nil {
			return nil, started, err
		}
		prefix, err := readNetworkWindow(ctx, tx, scope, s.generation, true, start, end)
		if err != nil {
			return nil, started, err
		}
		s.snapshot.Buckets = buckets.Buckets
		s.snapshot = networkSince(s.snapshot, s.available, prefix.Buckets)
	}
	if err := tx.Commit(); err != nil {
		return nil, started, err
	}
	for n := range all {
		all[n].index = nettraffic.NewIndex(all[n].snapshot)
	}
	return all, started, nil
}

// Both current billing usage and history use the same source intervals.
func sumNetworkSources(sources []networkSource, start, end time.Time) (rx, tx int64, available, partial bool) {
	type interval struct{ left, right time.Time }
	var coverage []interval
	for _, source := range sources {
		a, z := start, end
		if a.Before(source.available) {
			a = source.available
		}
		if source.ended.Unix() > 0 && z.After(source.ended) {
			z = source.ended
		}
		if z.After(source.snapshot.UpdatedAt) {
			z = source.snapshot.UpdatedAt
		}
		if !z.After(a) || !source.index.HasCoverage(a, z) {
			continue
		}
		r, t, p := source.index.Sum(a, z)
		rx, tx = addSaturating(rx, r), addSaturating(tx, t)
		available, partial = true, partial || p
		coverage = append(coverage, interval{a, z})
	}
	sort.Slice(coverage, func(i, j int) bool { return coverage[i].left.Before(coverage[j].left) })
	covered := start
	for _, c := range coverage {
		if c.left.After(covered) {
			partial = true
		}
		if c.right.After(covered) {
			covered = c.right
		}
	}
	return rx, tx, available, partial || covered.Before(end)
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
	prepared, recordedFrom, err := h.networkSources(ctx, scope, "", start, end)
	if err != nil {
		return result, err
	}
	if recordedFrom.IsZero() {
		result.Rows, result.Imports = []Usage{}, []ImportedUsage{}
		return result, nil
	}
	result.StartedAt = recordedFrom
	result.Imports = []ImportedUsage{}
	for _, source := range prepared {
		if source.snapshot.UpdatedAt.After(result.UpdatedAt) {
			result.UpdatedAt = source.snapshot.UpdatedAt
		}
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
		rx, txBytes, available, partial := sumNetworkSources(prepared, left, right)
		row.NetworkRX = addSaturating(row.NetworkRX, rx)
		row.NetworkTX = addSaturating(row.NetworkTX, txBytes)
		row.NetworkAvailable = row.NetworkAvailable || available
		row.NetworkPartial = row.NetworkPartial || partial
		buckets[key] = row
	}
	result.Rows = nil
	for _, row := range buckets {
		row.NetworkTotal = addSaturating(row.NetworkRX, row.NetworkTX)
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Date < result.Rows[j].Date })
	return result, nil
}
func (t *Tracker) NetworkState(id string) model.NetworkTrafficState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state.Network[id]
}
