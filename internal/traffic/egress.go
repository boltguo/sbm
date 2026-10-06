package traffic

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/boltguo/sbm/internal/model"
)

func cloneState(state model.State) model.State {
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	counters, err := t.Gateways.Sample(ctx, gateways)
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
			s.Initialized = false
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
		// Following a missed reset boundary, baseline the first available
		// sample: cumulative counters cannot split the offline interval.
		if s.Initialized && s.Peer == gatewayPeer(g) && !due {
			s.TX = addSaturating(s.TX, counterDelta(c.TX, s.LastTX, c.TXGeneration, s.TXGeneration))
			s.RX = addSaturating(s.RX, counterDelta(c.RX, s.LastRX, c.RXGeneration, s.RXGeneration))
		}
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
func (t *Tracker) SampleGateways(ctx context.Context) error {
	t.gatewayMu.Lock()
	defer t.gatewayMu.Unlock()
	gateways := t.config.Get().EgressGateways
	if len(gateways) == 0 && !t.State().EgressAccountingPending {
		return nil
	}
	return t.sampleGateways(ctx, gateways)
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
	pending := t.state.EgressAccountingPending || len(t.state.Egress) > 0
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
	if pending || len(old.EgressGateways) > 0 || len(next.EgressGateways) > 0 {
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
	if s.Status == SampleStatusInterrupted {
		s.Initialized = false
	}
	t.state.Egress[id] = s
	t.mu.Unlock()
	return t.Persist()
}

func (t *Tracker) ReconcileGateways(ctx context.Context) error {
	t.gatewayMu.Lock()
	defer t.gatewayMu.Unlock()
	cfg := t.config.Get()
	return t.CommitEgressChange(ctx, cfg, cfg)
}
