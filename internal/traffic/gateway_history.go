package traffic

import (
	"database/sql"
	"errors"
	"time"

	"github.com/boltguo/sbm/internal/model"
)

type gatewayHistoryKey struct {
	ID, Day string
}

type gatewayHistoryUsage struct {
	TX, RX  int64
	Partial bool
}

// Preserve pre-upgrade period totals separately: their daily breakdown is
// unknown. The marker and imports commit together, so retrying cannot import
// the same counters twice. Removing a gateway never removes these records.
func (h *historyStore) initializeGatewayHistory(state model.State, now time.Time) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var started string
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='gateway_history_started_at'`).Scan(&started)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for id, s := range state.Egress {
		if s.TX == 0 && s.RX == 0 {
			continue
		}
		ended := s.LastSuccessAt
		if ended.IsZero() {
			ended = state.UpdatedAt
		}
		if _, err := tx.Exec(`INSERT INTO gateway_imports VALUES (?, ?, ?, ?, ?)`, id,
			s.PeriodStartedAt.UTC().Format(time.RFC3339Nano), ended.UTC().Format(time.RFC3339Nano), s.TX, s.RX); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO metadata VALUES ('gateway_history_started_at', ?)`, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// Called with t.mu held. This durable record is independent of allowance
// resets and global proxy history. Recovered bytes belong to the recovery
// date; partial marks an uncertain date or lost counter generation.
func (t *Tracker) recordGatewayHistory(id string, now, previous time.Time, tx, rx int64, partial bool) {
	if t.history == nil {
		return
	}
	if t.pendingGatewayHistory == nil {
		t.pendingGatewayHistory = make(map[gatewayHistoryKey]gatewayHistoryUsage)
	}
	day := now.In(t.history.location).Format(time.DateOnly)
	gap := !previous.IsZero() && now.Sub(previous) > 15*time.Second
	key := gatewayHistoryKey{ID: id, Day: day}
	usage := t.pendingGatewayHistory[key]
	usage.TX = addSaturating(usage.TX, tx)
	usage.RX = addSaturating(usage.RX, rx)
	usage.Partial = usage.Partial || partial || gap || previous.IsZero()
	t.pendingGatewayHistory[key] = usage
	if gap {
		previousDay := previous.In(t.history.location).Format(time.DateOnly)
		key := gatewayHistoryKey{ID: id, Day: previousDay}
		usage := t.pendingGatewayHistory[key]
		usage.Partial = true
		t.pendingGatewayHistory[key] = usage
	}
}
