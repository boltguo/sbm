package traffic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

const EntryNetworkScope = "entry"

type networkRecord struct {
	Snapshot      nettraffic.Snapshot
	Generation    string
	AvailableFrom time.Time
	Revision      uint64
	Baseline      []nettraffic.Bucket
}

func (t *Tracker) UsesVnStat() bool { return t.NetworkReader != nil }
func networkRequest(cfg model.Config, id string) (nettraffic.Request, model.ResetConfig, error) {
	if id != EntryNetworkScope {
		return nettraffic.Request{}, model.ResetConfig{}, errors.New("vnStat covers the entry host only")
	}
	return nettraffic.Request{Interface: cfg.VnStatInterface}, cfg.Reset, nil
}
func periodStart(now time.Time, reset model.ResetConfig) (time.Time, time.Time, error) {
	if reset.Mode != "monthly" {
		return time.Time{}, time.Time{}, nil
	}
	next, err := NextMonthlyReset(now, reset)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return next.AddDate(0, -1, 0), next, nil
}
func networkOrigin(_ nettraffic.Request, s nettraffic.Snapshot) string { return "local/" + s.Interface }
func networkGeneration(req nettraffic.Request, s nettraffic.Snapshot) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", networkOrigin(req, s), s.CreatedAt.Unix())))
	return hex.EncodeToString(hash[:])
}

// SampleNetwork is serialized with manual resets and does not enter the
// existing core/gateway accounting locks.
func (t *Tracker) SampleNetwork(ctx context.Context, id string) error {
	if t.NetworkReader == nil {
		return nil
	}
	lock := t.networkLock(id)
	lock.Lock()
	defer lock.Unlock()
	cfg := t.config.Get()
	req, reset, err := networkRequest(cfg, id)
	var snapshot nettraffic.Snapshot
	if err == nil {
		snapshot, err = t.NetworkReader.Read(ctx, req)
	}
	if err != nil {
		t.networkFailure(id, err)
		return err
	}
	// An interface change while the vnStat query was in flight must not publish old-host data.
	currentReq, currentReset, currentErr := networkRequest(t.config.Get(), id)
	if currentErr != nil || !sameNetworkRequest(req, currentReq) || reset != currentReset {
		return nil
	}
	generation := networkGeneration(req, snapshot)
	t.mu.RLock()
	previous := t.state.Network[id]
	pending := t.pendingNetwork[id]
	t.mu.RUnlock()
	if previous.Generation != "" {
		if previous.Origin == networkOrigin(req, snapshot) && previous.CreatedAt.Equal(snapshot.CreatedAt) {
			generation = previous.Generation
		} else {
			generation = fmt.Sprintf("%s-%d", generation, t.now().UnixNano())
		}
	}
	if generation == previous.Generation && (snapshot.UpdatedAt.Before(previous.UpdatedAt) || snapshot.RX < previous.LastRX || snapshot.TX < previous.LastTX) {
		err = errors.New("vnStat counters moved backwards; last saved usage retained")
		t.networkFailure(id, err)
		return err
	}
	if generation != previous.Generation && t.now().Sub(snapshot.UpdatedAt) > 3*time.Minute {
		err := errors.New("vnStat database is stale; waiting for a fresh source baseline")
		t.networkFailure(id, err)
		return err
	}
	st := previous
	now := t.now()
	start, next, err := periodStart(now, reset)
	if err != nil {
		return err
	}
	resetChanged := st.Reset != reset
	periodChanged := st.Generation != "" && reset.Mode == "monthly" && (!now.Before(st.NextResetAt) && !st.NextResetAt.IsZero())
	if st.Generation == "" || resetChanged || periodChanged {
		st.Manual = false
		st.CarryRX = 0
		st.CarryTX = 0
		if reset.Mode == "monthly" {
			st.PeriodStartedAt = start
			if st.RecordedFrom.After(start) {
				st.PeriodStartedAt = st.RecordedFrom
			}
		} else if st.PeriodStartedAt.IsZero() {
			st.PeriodStartedAt = snapshot.CreatedAt
			st.Manual = true
			st.BaselineRX = 0
			st.BaselineTX = 0
		}
	}
	available := st.SourceStartedAt
	if available.IsZero() {
		available = snapshot.CreatedAt
	}
	if previous.Generation == "" {
		// Start at the first saved sample, even when the host already has vnStat
		// history. Installing SBM does not import earlier NIC usage.
		available = snapshot.UpdatedAt
		st.RecordedFrom = available
		st.PeriodStartedAt = available
		st.Manual = true
		st.BaselineRX, st.BaselineTX = snapshot.RX, snapshot.TX
		st.CarryRX, st.CarryTX = 0, 0
	}
	if previous.Generation != "" && previous.Generation != generation {
		// Recreated database: keep already-recorded usage and collect new bytes.
		// A different interface/host may have overlapping history, so establish a
		// fresh total baseline rather than adding its old traffic to this machine.
		if (periodChanged || resetChanged) && t.history != nil {
			old, err := t.history.loadNetwork(ctx, id, previous.Generation)
			if err != nil {
				return err
			}
			old.CreatedAt = previous.CreatedAt
			old.UpdatedAt = previous.UpdatedAt
			prefix, err := readNetworkRows(ctx, t.history.db, id, previous.Generation, true)
			if err != nil {
				return err
			}
			old = networkSince(old, previous.SourceStartedAt, prefix.Buckets)
			from := st.PeriodStartedAt
			if previous.SourceStartedAt.After(from) {
				from = previous.SourceStartedAt
			}
			st.RX, st.TX, _ = nettraffic.Sum(old, from, now)
		}
		st.Manual = true
		st.CarryRX = st.RX
		st.CarryTX = st.TX
		st.BaselineRX = snapshot.RX
		st.BaselineTX = snapshot.TX
		available = now
		if previous.Origin == networkOrigin(req, snapshot) && snapshot.CreatedAt.After(previous.UpdatedAt) {
			available = snapshot.CreatedAt
			from := st.PeriodStartedAt
			if st.RecordedFrom.After(from) {
				from = st.RecordedFrom
			}
			// Lifetime totals may include bytes before the new billing period.
			rx, tx, _ := nettraffic.Sum(snapshot, from, snapshot.UpdatedAt)
			st.BaselineRX = snapshot.RX - rx
			st.BaselineTX = snapshot.TX - tx
		}
		st.Partial = true
	}
	record := networkRecord{Snapshot: snapshot, Generation: generation, AvailableFrom: available}
	if pending.Generation == generation {
		record.Baseline = pending.Baseline
	}
	if generation != previous.Generation && available.After(snapshot.CreatedAt) {
		// Keep the prefix of the buckets containing the first sample. History
		// can subtract it exactly without importing earlier bytes in that day.
		for _, b := range snapshot.Buckets {
			if b.Start <= available.Unix() && b.Start+b.Seconds > available.Unix() {
				record.Baseline = append(record.Baseline, b)
			}
		}
	}
	// Merge retained SBM buckets with the source's complete snapshot. Replacing
	// a bucket, rather than adding every response, makes retries idempotent.
	merged := snapshot
	if t.history != nil {
		saved, loadErr := t.history.loadNetwork(ctx, id, generation)
		if loadErr != nil {
			t.networkFailure(id, loadErr)
			return loadErr
		}
		merged.Buckets, loadErr = mergeNetworkBuckets(saved.Buckets, snapshot.Buckets)
		if loadErr != nil {
			t.networkFailure(id, loadErr)
			return loadErr
		}
	}
	if st.Manual {
		st.RX = addSaturating(st.CarryRX, max(0, snapshot.RX-st.BaselineRX))
		st.TX = addSaturating(st.CarryTX, max(0, snapshot.TX-st.BaselineTX))
	} else {
		prefix := record.Baseline
		if t.history != nil {
			baseline, err := readNetworkRows(ctx, t.history.db, id, generation, true)
			if err != nil {
				t.networkFailure(id, err)
				return err
			}
			prefix = append(prefix, baseline.Buckets...)
		}
		merged = networkSince(merged, available, prefix)
		st.RX, st.TX, st.Partial = nettraffic.Sum(merged, st.PeriodStartedAt, snapshot.UpdatedAt)
		if generation == previous.Generation && !resetChanged && !periodChanged && (st.RX < previous.RX || st.TX < previous.TX) {
			// A missing or inconsistent source bucket cannot erase bytes already
			// counted in the same billing period or release an existing quota stop.
			st.RX = max(st.RX, previous.RX)
			st.TX = max(st.TX, previous.TX)
			st.Partial = true
		}
	}
	st.Origin = networkOrigin(req, snapshot)
	st.Available = st.Manual || snapshot.UpdatedAt.Equal(st.PeriodStartedAt) || nettraffic.HasCoverage(merged, st.PeriodStartedAt, snapshot.UpdatedAt)
	st.Interface = snapshot.Interface
	st.Generation = generation
	st.CreatedAt = snapshot.CreatedAt
	st.SourceStartedAt = available
	st.UpdatedAt = snapshot.UpdatedAt
	st.LastRX = snapshot.RX
	st.LastTX = snapshot.TX
	st.NextResetAt = next
	st.Reset = reset
	st.Status = SampleStatusHealthy
	st.Reason = ""
	if !st.Available {
		st.Status = SampleStatusInterrupted
		st.Reason = "no vnStat records cover the current billing period"
	}
	if now.Sub(snapshot.UpdatedAt) > 3*time.Minute {
		st.Status = SampleStatusInterrupted
		st.Reason = "vnStat database is stale; showing the last saved counters"
	}
	t.mu.Lock()
	if t.state.Network == nil {
		t.state.Network = map[string]model.NetworkTrafficState{}
	}
	if t.pendingNetwork == nil {
		t.pendingNetwork = map[string]networkRecord{}
	}
	t.networkRevision++
	record.Revision = t.networkRevision
	t.state.Network[id] = st
	t.pendingNetwork[id] = record
	t.state.UpdatedAt = now
	t.mu.Unlock()
	err = t.Persist()
	if id == EntryNetworkScope {
		err = errors.Join(err, t.ReconcileQuota(ctx))
	}
	return err
}
func sameNetworkRequest(a, b nettraffic.Request) bool { return a.Interface == b.Interface }
func (t *Tracker) networkFailure(id string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.Network == nil {
		t.state.Network = map[string]model.NetworkTrafficState{}
	}
	st := t.state.Network[id]
	st.Status = SampleStatusInterrupted
	st.Reason = err.Error()
	t.state.Network[id] = st
}
func (t *Tracker) networkLock(id string) *sync.Mutex {
	t.networkLocksMu.Lock()
	defer t.networkLocksMu.Unlock()
	if t.networkLocks == nil {
		t.networkLocks = map[string]*sync.Mutex{}
	}
	lock := t.networkLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		t.networkLocks[id] = lock
	}
	return lock
}
func mergeNetworkBuckets(old, next []nettraffic.Bucket) ([]nettraffic.Bucket, error) {
	items := map[[2]int64]nettraffic.Bucket{}
	for _, b := range old {
		items[[2]int64{b.Seconds, b.Start}] = b
	}
	for _, b := range next {
		key := [2]int64{b.Seconds, b.Start}
		if saved, ok := items[key]; ok && (b.RX < saved.RX || b.TX < saved.TX) {
			return nil, errors.New("vnStat bucket moved backwards; last saved records retained")
		}
		items[key] = b
	}
	result := make([]nettraffic.Bucket, 0, len(items))
	for _, b := range items {
		result = append(result, b)
	}
	return result, nil
}
func (t *Tracker) resetNetwork(ctx context.Context, id string) error {
	// A manual reset needs a fresh source total, otherwise bytes between the old
	// sample and the button press would leak into the new period.
	if err := t.SampleNetwork(ctx, id); err != nil {
		return err
	}
	lock := t.networkLock(id)
	lock.Lock()
	defer lock.Unlock()
	t.mu.Lock()
	st := t.state.Network[id]
	if st.Status != SampleStatusHealthy {
		t.mu.Unlock()
		return errors.New("vnStat must be available before a manual reset")
	}
	st.Manual = true
	st.BaselineRX = st.LastRX
	st.BaselineTX = st.LastTX
	st.CarryRX = 0
	st.CarryTX = 0
	st.RX = 0
	st.TX = 0
	st.Partial = false
	st.PeriodStartedAt = st.UpdatedAt
	t.state.Network[id] = st
	if id == EntryNetworkScope {
		t.state.QuotaExceeded = false
	}
	t.mu.Unlock()
	if err := t.Persist(); err != nil {
		return err
	}
	if id == EntryNetworkScope {
		return t.ReconcileQuota(ctx)
	}
	return nil
}
func (t *Tracker) runNetworkSource(ctx context.Context, id string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	failed := false
	for {
		err := t.SampleNetwork(ctx, id)
		if err != nil && !failed && ctx.Err() == nil {
			log.Printf("vnstat (%s): %v", id, err)
		}
		failed = err != nil
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (t *Tracker) reconcileNetworkQuota(ctx context.Context) error {
	cfg := t.config.Get()
	limit, err := cfg.TrafficQuota.NetworkStopBytes()
	if err != nil {
		return err
	}
	t.mu.Lock()
	st := t.state.Network[EntryNetworkScope]
	// Missing source data must not clear a persisted safety stop on startup.
	if limit > 0 && (st.Generation == "" || (!st.Available) || st.UpdatedAt.Before(st.PeriodStartedAt)) {
		t.mu.Unlock()
		return t.reconcileCore(ctx)
	}
	exceeded := limit > 0 && cfg.TrafficQuota.NetworkUsage(st.RX, st.TX) >= limit
	changed := t.state.QuotaExceeded != exceeded
	t.state.QuotaExceeded = exceeded
	t.mu.Unlock()
	var persistErr error
	if changed {
		persistErr = t.Persist()
	}
	return errors.Join(persistErr, t.reconcileCore(ctx))
}
