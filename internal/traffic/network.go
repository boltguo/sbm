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

// networkStaleAfter is how old vnStat's last update may be before the
// source is reported as interrupted.
const networkStaleAfter = 3 * time.Minute

// proxyMarkWindow bounds the per-second core totals kept to find the proxy
// total at the moment of vnStat's last update.
const proxyMarkWindow = 15 * time.Minute

type proxyMark struct {
	at    time.Time
	total int64
}

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
	if generation != previous.Generation && t.now().Sub(snapshot.UpdatedAt) > networkStaleAfter {
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
		st.Adjustment = model.UsageAdjustment{}
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
			sources, _, err := t.history.networkSources(ctx, id, "", st.PeriodStartedAt, previous.UpdatedAt)
			if err != nil {
				t.networkFailure(id, err)
				return err
			}
			st.RX, st.TX, _, st.Partial = sumNetworkSources(sources, st.PeriodStartedAt, previous.UpdatedAt)
			if st.Partial && !st.PeriodStartedAt.After(previous.PeriodStartedAt) {
				st.RX, st.TX = max(st.RX, previous.RX), max(st.TX, previous.TX)
			}
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
	periodAvailable := false
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
		var sources []networkSource
		if t.history != nil && available.After(st.PeriodStartedAt) {
			var err error
			sources, _, err = t.history.networkSources(ctx, id, generation, st.PeriodStartedAt, available)
			if err != nil {
				t.networkFailure(id, err)
				return err
			}
			// A source switch may not have reached SQLite yet.
			for n := range sources {
				if sources[n].ended.Unix() <= 0 || sources[n].ended.After(available) {
					sources[n].ended = available
				}
			}
		}
		sources = append(sources, networkSource{snapshot: merged, available: available, index: nettraffic.NewIndex(merged)})
		st.RX, st.TX, periodAvailable, st.Partial = sumNetworkSources(sources, st.PeriodStartedAt, snapshot.UpdatedAt)
		if st.Partial && resetChanged && !st.PeriodStartedAt.After(previous.PeriodStartedAt) {
			st.RX, st.TX = max(st.RX, previous.RX), max(st.TX, previous.TX)
		}
		if generation == previous.Generation && !resetChanged && !periodChanged && (st.RX < previous.RX || st.TX < previous.TX) {
			// A missing or inconsistent source bucket cannot erase bytes already
			// counted in the same billing period or release an existing quota stop.
			st.RX = max(st.RX, previous.RX)
			st.TX = max(st.TX, previous.TX)
			st.Partial = true
		}
	}
	st.Origin = networkOrigin(req, snapshot)
	st.Available = st.Manual || snapshot.UpdatedAt.Equal(st.PeriodStartedAt) || periodAvailable
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
	if now.Sub(snapshot.UpdatedAt) > networkStaleAfter {
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
	t.state.Network[id] = t.anchorProxyLocked(st)
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
	st.Adjustment = model.UsageAdjustment{}
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
	usage, _ := t.entryUsageLocked(cfg.TrafficQuota)
	exceeded := limit > 0 && usage >= limit
	// Missing source data must not clear a persisted safety stop on startup,
	// but proxy bytes counted since vnStat went quiet can still raise one.
	if limit > 0 && (st.Generation == "" || (!st.Available) || st.UpdatedAt.Before(st.PeriodStartedAt)) {
		exceeded = exceeded || t.state.QuotaExceeded
	}
	changed := t.state.QuotaExceeded != exceeded
	t.state.QuotaExceeded = exceeded
	t.mu.Unlock()
	var persistErr error
	if changed {
		persistErr = t.Persist()
	}
	return errors.Join(persistErr, t.reconcileCore(ctx))
}

// Called with t.mu held.
func (t *Tracker) recordProxyMarkLocked(now time.Time) {
	total := t.state.Total()
	t.proxyMarks = append(t.proxyMarks, proxyMark{at: now, total: total})
	cut := 0
	for cut < len(t.proxyMarks)-1 && now.Sub(t.proxyMarks[cut].at) > proxyMarkWindow {
		cut++
	}
	if cut > 0 {
		t.proxyMarks = append(t.proxyMarks[:0], t.proxyMarks[cut:]...)
	}
	if st, ok := t.state.Network[EntryNetworkScope]; ok && st.Generation != "" && !st.ProxyAnchorKnown {
		// The anchor was taken before any core sample (panel start). Bytes the
		// first sample adds for the downtime may already be in vnStat, so the
		// estimate starts here instead.
		st.ProxyAnchor = total
		st.ProxyAnchorKnown = true
		t.state.Network[EntryNetworkScope] = st
	}
}

// anchorProxyLocked aligns the proxy counters with vnStat: the anchor is the
// proxy total when vnStat last updated, or when the period began if that is
// later. Called with t.mu held.
func (t *Tracker) anchorProxyLocked(st model.NetworkTrafficState) model.NetworkTrafficState {
	at := st.UpdatedAt
	if st.PeriodStartedAt.After(at) {
		at = st.PeriodStartedAt
	}
	// A core sample may have resolved the anchor while vnStat was being read.
	if current := t.state.Network[EntryNetworkScope]; current.ProxyAnchorKnown && current.ProxyAnchorAt.Equal(at) {
		st.ProxyAnchor, st.ProxyAnchorAt, st.ProxyAnchorKnown = current.ProxyAnchor, current.ProxyAnchorAt, true
		return st
	}
	st.ProxyAnchorAt, st.ProxyAnchor, st.ProxyAnchorKnown = at, 0, false
	for i := len(t.proxyMarks) - 1; i >= 0; i-- {
		if !t.proxyMarks[i].at.After(at) {
			st.ProxyAnchor, st.ProxyAnchorKnown = t.proxyMarks[i].total, true
			return st
		}
	}
	if len(t.proxyMarks) > 0 {
		// Counters only cover a later moment; skip the gap rather than count
		// bytes vnStat may already include.
		st.ProxyAnchor, st.ProxyAnchorKnown = t.proxyMarks[0].total, true
	}
	return st
}

// entryUsageLocked returns the billed entry usage SBM enforces and the part of
// it estimated from sing-box counters because vnStat has not recorded it yet:
// vnStat saves once a minute and is read every 30 seconds, and a stale or
// unreadable source would otherwise freeze the usage. Called with t.mu held.
func (t *Tracker) entryUsageLocked(q model.TrafficQuotaConfig) (usage, estimated int64) {
	st := t.state.Network[EntryNetworkScope]
	usage = q.NetworkUsage(st.RX, st.TX)
	if st.Generation != "" && st.ProxyAnchorKnown {
		estimated = scaleSaturating(max(0, t.state.Total()-st.ProxyAnchor), q.ProviderUsageFactor())
	}
	return st.Adjustment.Apply(addSaturating(usage, estimated), q.BillingMode), estimated
}

// CalibrateEntryUsage makes the enforced entry usage equal usedBytes, the
// provider-reported usage for the current period, until the next reset. It
// covers usage from before SBM was installed or from other interfaces. clear
// removes the calibration instead.
func (t *Tracker) CalibrateEntryUsage(ctx context.Context, usedBytes int64, clear bool) error {
	if !t.UsesVnStat() {
		return errors.New("vnStat is required to calibrate usage")
	}
	if usedBytes < 0 {
		return errors.New("used traffic cannot be negative")
	}
	if err := t.SampleNetwork(ctx, EntryNetworkScope); err != nil {
		return err
	}
	lock := t.networkLock(EntryNetworkScope)
	lock.Lock()
	defer lock.Unlock()
	q := t.config.Get().TrafficQuota
	t.mu.Lock()
	st := t.state.Network[EntryNetworkScope]
	if st.Status != SampleStatusHealthy {
		t.mu.Unlock()
		return errors.New("vnStat must be available before calibrating usage")
	}
	st.Adjustment = model.UsageAdjustment{}
	if !clear {
		t.state.Network[EntryNetworkScope] = st
		measured, _ := t.entryUsageLocked(q)
		st.Adjustment = model.UsageAdjustment{Bytes: usedBytes - measured, BillingMode: q.BillingMode}
	}
	t.state.Network[EntryNetworkScope] = st
	t.state.UpdatedAt = t.now()
	t.mu.Unlock()
	if err := t.Persist(); err != nil {
		return err
	}
	return t.ReconcileQuota(ctx)
}

// EntryUsage is the billed entry usage used for the quota stop, including the
// proxy bytes vnStat has not recorded yet.
func (t *Tracker) EntryUsage(q model.TrafficQuotaConfig) (usage, estimated int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.entryUsageLocked(q)
}

// networkOverLimitLocked lets every core sample raise the stop as soon as the
// estimate crosses the threshold. Only vnStat samples clear it. Called with
// t.mu held.
func (t *Tracker) networkOverLimitLocked(q model.TrafficQuotaConfig) bool {
	limit, err := q.NetworkStopBytes()
	if err != nil || limit <= 0 || t.state.Network[EntryNetworkScope].Generation == "" {
		return false
	}
	usage, _ := t.entryUsageLocked(q)
	return usage >= limit
}

func scaleSaturating(value, factor int64) int64 {
	if value <= 0 || factor <= 0 {
		return 0
	}
	if value > (1<<63-1)/factor {
		return 1<<63 - 1
	}
	return value * factor
}
