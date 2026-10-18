package traffic

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/boltguo/sbm/internal/model"
)

func cloneState(state model.State) model.State {
	if state.Network != nil {
		result := make(map[string]model.NetworkTrafficState, len(state.Network))
		for k, v := range state.Network {
			result[k] = v
		}
		state.Network = result
	}
	if state.Egress != nil {
		result := make(map[string]model.GatewayTrafficState, len(state.Egress))
		for id, s := range state.Egress {
			result[id] = s
		}
		state.Egress = result
	}
	return state
}
func addSaturating(a, b int64) int64 {
	if b > 0 && a > (1<<63-1)-b {
		return 1<<63 - 1
	}
	return a + b
}
func counterDelta(current, last int64, generation, previous string) int64 {
	if current < last || generation != previous {
		return current
	}
	return current - last
}
func gatewayPeer(g model.EgressGateway) string { return fmt.Sprintf("%s:%d", g.Server, g.ServerPort) }
func nextGatewayReset(now time.Time, g model.EgressGateway) time.Time {
	if g.Reset.Mode != "monthly" {
		return time.Time{}
	}
	next, _ := NextMonthlyReset(now, g.Reset)
	return next
}
func (t *Tracker) sampleGateways(ctx context.Context, gateways []model.EgressGateway) error {
	if t.Gateways == nil {
		return nil
	}
	var counters map[string]GatewayCounters
	var err error
	if hasEnabledGateway(gateways) || t.State().EgressAccountingPending {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		counters, err = t.Gateways.Sample(ctx, gateways)
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.EgressAccountingPending = err != nil
	if t.state.Egress == nil {
		t.state.Egress = map[string]model.GatewayTrafficState{}
	}
	for _, g := range gateways {
		s, exists := t.state.Egress[g.ID]
		if !exists {
			s = model.GatewayTrafficState{PeriodStartedAt: now, NextResetAt: nextGatewayReset(now, g), Reset: g.Reset, Status: SampleStatusWaiting}
		}

		due := !s.NextResetAt.IsZero() && !now.Before(s.NextResetAt)
		if due {
			s.TX = 0
			s.RX = 0
			s.PeriodStartedAt = now
			s.NextResetAt = nextGatewayReset(now, g)
			s.Partial = false
			s.Adjustment = model.UsageAdjustment{}
		}
		if !g.Enabled {
			s.Status = "disabled"
			t.state.Egress[g.ID] = s
			continue
		}
		c, ok := counters[g.ID]
		if err != nil || !ok || c.TX < 0 || c.RX < 0 {
			s.Status = SampleStatusInterrupted
			if s.FailureSince.IsZero() {
				s.FailureSince = now
			}
			t.state.Egress[g.ID] = s
			continue
		}
		var tx, rx int64
		partial := !s.Initialized || s.Peer != gatewayPeer(g)
		if !partial {
			tx = counterDelta(c.TX, s.LastTX, c.TXGeneration, s.TXGeneration)
			rx = counterDelta(c.RX, s.LastRX, c.RXGeneration, s.RXGeneration)
			// Keep recoverable bytes even across an allowance reset. The exact
			// split is unavailable, so assign them when observed and flag it.
			partial = c.TXGeneration != s.TXGeneration || c.RXGeneration != s.RXGeneration || c.TX < s.LastTX || c.RX < s.LastRX
			crossedReset := !s.LastSuccessAt.IsZero() && s.LastSuccessAt.Before(s.PeriodStartedAt) && (tx > 0 || rx > 0)
			missedReset := crossedReset && (!s.FailureSince.IsZero() || now.Sub(s.LastSuccessAt) > 15*time.Second)
			s.Partial = s.Partial || partial || missedReset
			partial = partial || crossedReset
			s.TX = addSaturating(s.TX, tx)
			s.RX = addSaturating(s.RX, rx)
		}
		t.recordGatewayHistory(g.ID, now, s.LastSuccessAt, tx, rx, partial)
		s.Initialized = true
		s.Peer = gatewayPeer(g)
		s.LastTX = c.TX
		s.LastRX = c.RX
		s.TXGeneration = c.TXGeneration
		s.RXGeneration = c.RXGeneration
		s.Status = SampleStatusHealthy
		s.LastSuccessAt = now
		s.FailureSince = time.Time{}
		t.state.Egress[g.ID] = s
	}
	return err
}

// GatewaysOverHardLimit returns the enabled gateways with a hard stop whose
// estimated provider usage has reached the stop point.
func GatewaysOverHardLimit(cfg model.Config, state model.State) map[string]bool {
	paused := map[string]bool{}
	for _, g := range cfg.EgressGateways {
		if !g.Enabled || !g.StopOnQuota {
			continue
		}
		stop, err := g.TrafficQuota.NetworkStopBytes()
		if err == nil && stop > 0 && state.Egress[g.ID].ProviderUsage(g.TrafficQuota) >= stop {
			paused[g.ID] = true
		}
	}
	return paused
}

// DesiredGatewayPauses is the set of gateways cfg should render as paused.
func (t *Tracker) DesiredGatewayPauses(cfg model.Config) map[string]bool {
	return GatewaysOverHardLimit(cfg, t.State())
}

// AppliedGatewayPauses is the set of gateways the running core rejects.
func (t *Tracker) AppliedGatewayPauses() map[string]bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	applied := make(map[string]bool, len(t.state.AppliedGatewayPauses))
	for _, id := range t.state.AppliedGatewayPauses {
		applied[id] = true
	}
	return applied
}

// SetAppliedGatewayPauses records the paused set the core now runs with.
func (t *Tracker) SetAppliedGatewayPauses(paused map[string]bool) error {
	ids := make([]string, 0, len(paused))
	for id, on := range paused {
		if on {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	t.mu.Lock()
	t.state.AppliedGatewayPauses = ids
	t.mu.Unlock()
	return t.Persist()
}

// GatewayPauseChanges signals when a gateway crosses its hard limit or comes
// back under it, so the core can be re-rendered.
func (t *Tracker) GatewayPauseChanges() <-chan struct{} {
	t.pauseOnce.Do(func() { t.pauseSignal = make(chan struct{}, 1) })
	return t.pauseSignal
}

func (t *Tracker) signalGatewayPauses() {
	t.pauseOnce.Do(func() { t.pauseSignal = make(chan struct{}, 1) })
	if maps.Equal(t.DesiredGatewayPauses(t.config.Get()), t.AppliedGatewayPauses()) {
		return
	}
	select {
	case t.pauseSignal <- struct{}{}:
	default:
	}
}

func hasEnabledGateway(gateways []model.EgressGateway) bool {
	for _, g := range gateways {
		if g.Enabled {
			return true
		}
	}
	return false
}

func (t *Tracker) SampleGateways(ctx context.Context) error {
	t.gatewayMu.Lock()
	defer t.gatewayMu.Unlock()
	gateways := t.config.Get().EgressGateways
	if len(gateways) == 0 && !t.State().EgressAccountingPending {
		return nil
	}
	// Commit each read with its baseline. A panel crash can then resume from
	// the same kernel counters without losing or repeating the delta.
	err := errors.Join(t.sampleGateways(ctx, gateways), t.Persist())
	t.signalGatewayPauses()
	return err
}

// Hold the accounting lock across the core transaction. A periodic sampler
// must never reconcile rules against a candidate config that later rolls back.
func (t *Tracker) BeginEgressChange(ctx context.Context) func() {
	t.gatewayMu.Lock()
	gateways := t.config.Get().EgressGateways
	if len(gateways) > 0 {
		_ = t.sampleGateways(ctx, gateways)
	}
	return t.gatewayMu.Unlock
}
func (t *Tracker) CommitEgressChange(ctx context.Context, old, next model.Config) error {
	now := t.now()
	t.mu.Lock()
	if t.state.Egress == nil {
		t.state.Egress = map[string]model.GatewayTrafficState{}
	}
	// Once inactive rules are cleaned up, disabled drafts need no kernel
	// accounting dependency. Preserve failed cleanup for periodic retries.
	t.state.EgressAccountingPending = t.state.EgressAccountingPending || hasEnabledGateway(old.EgressGateways)
	keep := map[string]bool{}
	for _, g := range next.EgressGateways {
		keep[g.ID] = true
		s, exists := t.state.Egress[g.ID]
		if !exists {
			s = model.GatewayTrafficState{PeriodStartedAt: now, Status: SampleStatusWaiting}
		}
		if s.Reset != g.Reset || !exists {
			s.Reset = g.Reset
			s.NextResetAt = nextGatewayReset(now, g)
		}
		if s.Peer != gatewayPeer(g) {
			s.Initialized = false
			s.Peer = gatewayPeer(g)
		}
		if !g.Enabled {
			s.Status = "disabled"
		}
		t.state.Egress[g.ID] = s
	}
	for id := range t.state.Egress {
		if !keep[id] {
			delete(t.state.Egress, id)
		}
	}
	t.mu.Unlock()
	var err error
	if t.State().EgressAccountingPending || len(next.EgressGateways) > 0 {
		err = t.sampleGateways(ctx, next.EgressGateways)
	}
	return errors.Join(err, t.Persist())
}

func (t *Tracker) ResetGateway(ctx context.Context, id string) error {
	t.gatewayMu.Lock()
	defer t.gatewayMu.Unlock()
	gateways := t.config.Get().EgressGateways
	var gateway *model.EgressGateway
	for i := range gateways {
		if gateways[i].ID == id {
			gateway = &gateways[i]
			break
		}
	}
	if gateway == nil {
		return errors.New("中继出口不存在")
	}
	// A reset never clears iptables counters. Keep the most recent baseline.
	_ = t.sampleGateways(ctx, gateways)
	now := t.now()
	t.mu.Lock()
	if t.state.Egress == nil {
		t.state.Egress = map[string]model.GatewayTrafficState{}
	}
	s := t.state.Egress[id]
	s.TX = 0
	s.RX = 0
	s.PeriodStartedAt = now
	s.NextResetAt = nextGatewayReset(now, *gateway)
	s.Partial = false
	s.Adjustment = model.UsageAdjustment{}
	t.state.Egress[id] = s
	t.mu.Unlock()
	defer t.signalGatewayPauses()
	return t.Persist()
}

// CalibrateGateway makes the gateway's estimated provider usage equal
// usedBytes until its next reset. The tunnel counters cannot see traffic the
// gateway host sends on its own, or usage from before the gateway was added.
// clear removes the calibration instead.
func (t *Tracker) CalibrateGateway(ctx context.Context, id string, usedBytes int64, clear bool) error {
	if usedBytes < 0 {
		return errors.New("used traffic cannot be negative")
	}
	t.gatewayMu.Lock()
	defer t.gatewayMu.Unlock()
	gateways := t.config.Get().EgressGateways
	var gateway *model.EgressGateway
	for i := range gateways {
		if gateways[i].ID == id {
			gateway = &gateways[i]
			break
		}
	}
	if gateway == nil {
		return errors.New("中继出口不存在")
	}
	_ = t.sampleGateways(ctx, gateways)
	t.mu.Lock()
	if t.state.Egress == nil {
		t.state.Egress = map[string]model.GatewayTrafficState{}
	}
	s := t.state.Egress[id]
	s.Adjustment = model.UsageAdjustment{}
	if !clear {
		measured := s.ProviderUsage(gateway.TrafficQuota)
		s.Adjustment = model.UsageAdjustment{Bytes: usedBytes - measured, BillingMode: gateway.TrafficQuota.BillingMode}
	}
	t.state.Egress[id] = s
	t.mu.Unlock()
	defer t.signalGatewayPauses()
	return t.Persist()
}

func (t *Tracker) ReconcileGateways(ctx context.Context) error {
	t.gatewayMu.Lock()
	defer t.gatewayMu.Unlock()
	cfg := t.config.Get()
	// On startup, reconcile once even if every gateway is disabled: a crash
	// may have left jumps behind before their cleanup was persisted.
	t.mu.Lock()
	t.state.EgressAccountingPending = t.state.EgressAccountingPending || len(cfg.EgressGateways) > 0 || len(t.state.Egress) > 0
	t.mu.Unlock()
	return t.CommitEgressChange(ctx, cfg, cfg)
}
