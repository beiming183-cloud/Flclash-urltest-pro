//go:build go1.21

package outboundgroup

import (
	"context"
	"errors"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

type stableEstablishment struct {
	id      string
	route   stability.BusinessRoute
	attempt int
	started time.Time
	elapsed time.Duration
	conn    C.Conn
	err     error
	cancel  context.CancelFunc
}

var stableSharedHedgeSlots = make(chan struct{}, 2)

// establish races CONNECT handshakes only: no application bytes are sent or
// replayed. At most two handshakes per request, three distinct verified routes,
// and two extra handshakes per group may overlap. The caller owns the one winner.
func (s *Stable) establish(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	target := metadata.RemoteAddress()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	epoch := s.engine.Snapshot().Epoch
	flow := stability.BusinessFromContext(ctx)
	results := make(chan stableEstablishment)
	pending := map[string]stableEstablishment{}
	tried := map[string]bool{}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	var tick <-chan time.Time
	arm := func(delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
		tick = timer.C
	}
	defer func() {
		for _, p := range pending {
			p.cancel()
			err := error(context.Canceled)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				err = context.DeadlineExceeded
			}
			elapsed := time.Since(p.started)
			flow.DialOutcome(p.route, s.byID[p.id].Name(), p.attempt, err, elapsed, false)
			reason, hard := stableError(err)
			s.targetResult(target, p.id, epoch, false, reason, hard, elapsed)
			_ = s.journal.Record(stability.Event{Event: "dial_attempt", Group: s.groupID, Node: p.id, Epoch: epoch, Target: s.journal.ID(target), Attempts: p.attempt, Reason: reason, DialMs: elapsed.Milliseconds()})
		}
	}()
	launch := func(extra bool) bool {
		if len(tried) >= 3 || len(pending) >= 2 || ctx.Err() != nil {
			return false
		}
		state := s.engine.Snapshot()
		if state.Epoch != epoch {
			return false
		}
		id := ""
		for _, candidate := range s.engine.Attempts() {
			if !tried[candidate] && !s.targetCooling(target, candidate) {
				id = candidate
				break
			}
		}
		// Target-specific backoff orders attempts; if every eligible route is
		// cooling, half-open one rather than declaring the entire IP unavailable.
		if id == "" && len(pending) == 0 {
			for _, candidate := range s.engine.Attempts() {
				if !tried[candidate] {
					id = candidate
					break
				}
			}
		}
		if id == "" {
			return false
		}
		if extra {
			select {
			case stableSharedHedgeSlots <- struct{}{}:
			default:
				return false
			}
			select {
			case s.hedgeSlots <- struct{}{}:
			default:
				<-stableSharedHedgeSlots
				return false
			}
		}
		var node stability.Stats
		for _, n := range state.Nodes {
			if n.ID == id {
				node = n
				break
			}
		}
		attemptCtx, release := context.WithTimeout(ctx, stability.ConnectionTimeout(node, time.Now()))
		p := stableEstablishment{id: id, route: stability.BusinessRoute{Group: s.groupID, Node: id,
			Protocol: s.byID[id].Type().String(), Epoch: epoch, Generation: state.Generation}, started: time.Now(), cancel: release}
		p.attempt = flow.DialAttempt(p.route, s.byID[id].Name())
		pending[id], tried[id] = p, true
		copyMeta := *metadata
		go func() {
			defer release()
			if extra {
				defer func() { <-s.hedgeSlots }()
				defer func() { <-stableSharedHedgeSlots }()
			}
			p.conn, p.err = s.byID[id].DialContext(attemptCtx, &copyMeta)
			p.elapsed = time.Since(p.started)
			if p.conn == nil && p.err == nil {
				p.err = errors.New("empty connection")
			}
			select {
			case results <- p:
			case <-ctx.Done():
				if p.conn != nil {
					p.conn.Close()
				}
			}
		}()
		delay := stability.EstablishmentHedgeDelay(node, time.Now())
		if deadline, ok := ctx.Deadline(); ok {
			delay = min(delay, max(10*time.Millisecond, time.Until(deadline)/3))
		}
		arm(delay)
		if extra {
			s.log("dial_hedge", "slow_establishment", id, true, 0)
		}
		return true
	}
	refill := func() {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	if !launch(false) {
		return nil, errors.New("no validated residential chain")
	}
	for {
		if len(pending) == 0 {
			if len(tried) >= 3 {
				return nil, stability.ErrEstablishmentExhausted
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			if s.engine.Snapshot().Epoch != epoch {
				return nil, stability.ErrEstablishmentExhausted
			}
			refill()
			if !launch(false) {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick:
			tick = nil
			if s.engine.Snapshot().Epoch != epoch {
				return nil, errors.New("residential network changed during establishment")
			}
			if !launch(true) && len(tried) < 3 && len(pending) < 2 {
				arm(200 * time.Millisecond)
			}
		case p := <-results:
			delete(pending, p.id)
			p.cancel()
			if ctx.Err() != nil {
				if p.conn != nil {
					p.conn.Close()
				}
				return nil, ctx.Err()
			}
			if p.err == nil {
				previousPrimary := s.engine.Snapshot().Primary
				if s.engine.AcceptDial(p.id, epoch) {
					s.targetResult(target, p.id, epoch, true, "", false, p.elapsed)
					s.engine.ObserveDial(p.id, epoch, p.elapsed)
					flow.DialOutcome(p.route, s.byID[p.id].Name(), p.attempt, nil, p.elapsed, true)
					_ = s.journal.Record(stability.Event{Event: "dial_result", Group: s.groupID, Node: p.id, Epoch: epoch, OK: true, DialMs: p.elapsed.Milliseconds(), Target: s.journal.ID(target), Attempts: p.attempt})
					p.conn.AppendToChains(s)
					if p.attempt > 1 || len(tried) > 1 {
						reason := "hedge_backup_won"
						if previousPrimary == p.id {
							reason = "hedge_primary_retained"
						}
						s.log("dial_selected", reason, p.id, true, p.elapsed)
						s.notifyRouteChange()
					}
					return p.conn, nil
				}
				p.conn.Close()
				p.err = errors.New("invalidated residential establishment")
				// Preserve the newer failed health/security verdict. A completed
				// CONNECT is not evidence that TLS or the fixed exit is valid.
				s.log("dial_discarded", "qualification_changed", p.id, false, 0)
			} else {
				if p.conn != nil {
					p.conn.Close()
				}
				reason, hard := stableError(p.err)
				s.targetResult(target, p.id, epoch, false, reason, hard, p.elapsed)
				s.log("dial_failed", reason, p.id, false, p.elapsed)
				s.notifyRouteChange()
			}
			flow.DialOutcome(p.route, s.byID[p.id].Name(), p.attempt, p.err, p.elapsed, false)
			reason, _ := stableError(p.err)
			_ = s.journal.Record(stability.Event{Event: "dial_attempt", Reason: reason, Group: s.groupID, Node: p.id, Epoch: epoch, Target: s.journal.ID(target), Attempts: p.attempt, DialMs: p.elapsed.Milliseconds()})
			refill()
			if s.engine.Snapshot().Epoch != epoch {
				return nil, errors.New("residential network changed during establishment")
			}
			launch(len(pending) > 0)
		}
	}
}
