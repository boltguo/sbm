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
	result := nettraffic.Snapshot{}
	rows, err := db.QueryContext(ctx, `SELECT start, seconds, rx, tx FROM network_bucket WHERE scope=? AND generation=?`, scope, generation)
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
	sources, err := tx.QueryContext(ctx, `SELECT generation,interface,created_at,updated_at,available_from,ended_at FROM network_source WHERE scope=?`, scope)
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
	buckets := map[string]Usage{}
	for _, row := range result.Rows {
		row.ProxyAvailable = true
		buckets[row.Date] = row
	}
	if len(all) > 0 {
		result.StartedAt = time.Unix(all[0].available, 0).UTC()
	}
	for _, source := range all {
		snapshot, err := readNetwork(ctx, tx, scope, source.generation)
		if err != nil {
			return result, err
		}
		snapshot.Interface = source.iface
		snapshot.CreatedAt = time.Unix(source.created, 0).UTC()
		snapshot.UpdatedAt = time.Unix(source.updated, 0).UTC()
		if snapshot.UpdatedAt.After(result.UpdatedAt) {
			result.UpdatedAt = snapshot.UpdatedAt
		}
		available := time.Unix(source.available, 0)
		if available.Before(result.StartedAt) {
			result.StartedAt = available
		}
		for at := start; at.Before(end); at = at.AddDate(0, 0, 1) {
			next := at.AddDate(0, 0, 1)
			left, right := at, next
			if left.Before(available) {
				left = available
			}
			if source.ended > 0 && right.After(time.Unix(source.ended, 0)) {
				right = time.Unix(source.ended, 0)
			}
			if right.After(snapshot.UpdatedAt) {
				right = snapshot.UpdatedAt
			}
			if !right.After(left) {
				continue
			}
			key := at.Format(time.DateOnly)
			if granularity == "month" {
				key = key[:7]
			}
			if !nettraffic.HasCoverage(snapshot, left, right) {
				// Keep missing dates visible and propagate gaps to a monthly total.
				// A month with other valid days must not look fully recorded.
				row := buckets[key]
				row.Date = key
				row.NetworkPartial = true
				buckets[key] = row
				continue
			}
			rx, txBytes, partial := nettraffic.Sum(snapshot, left, right)
			row := buckets[key]
			row.Date = key
			row.NetworkAvailable = true
			row.NetworkRX = addSaturating(row.NetworkRX, rx)
			row.NetworkTX = addSaturating(row.NetworkTX, txBytes)
			row.NetworkPartial = row.NetworkPartial || partial || left.After(at) || right.Before(next)
			buckets[key] = row
		}
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
