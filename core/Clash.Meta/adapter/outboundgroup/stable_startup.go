package outboundgroup

import (
	"context"
	"github.com/metacubex/mihomo/component/stability"
	"sort"
	"time"
)

type recoveryNode struct {
	failures int
	next     time.Time
}

type stableSearchKey struct{}

const stableSearchTimeout = 4500 * time.Millisecond

func (s *Stable) startupProbes(ctx context.Context, fault ...bool) {
	active := s.engine.FreshActiveCount()
	now := time.Now()
	s.mu.Lock()
	if active > 0 {
		s.recoveryHadPrimary = true
	} else if s.recoveryHadPrimary {
		s.recoveryHadPrimary, s.recoveryActive = false, false
	}
	if active >= 3 {
		s.recoveryActive = false
	}
	if s.cancel == nil || ctx.Err() != nil || active >= 3 || active > 0 && !s.recoveryActive || s.startupScheduling || now.Before(s.recoveryNext) {
		s.mu.Unlock()
		return
	}
	s.startupScheduling = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.startupScheduling = false; s.mu.Unlock() }()
	if !s.engine.PriorityAvailable(stability.RecoveryProbe, 2) && !s.engine.PriorityAvailable(stability.SearchProbe, 2) {
		return
	}
	s.mu.Lock()
	if s.cancel == nil || ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	requested := s.requested
	initial := !s.recoveryActive
	if initial {
		s.recoveryActive = true
		s.startupTried = map[string]bool{}
	}
	tried := make(map[string]bool, len(s.startupTried))
	for id, value := range s.startupTried {
		tried[id] = value
	}
	batch := 8 - len(s.quickScheduled) + len(s.confirmationScheduled)
	// Reserve the scheduling turn atomically across the background worker and
	// all waiting browser requests. Never scan the entire pool on each wakeup.
	s.recoveryNext = now.Add(100 * time.Millisecond)
	s.mu.Unlock()
	ids := s.engine.StartupCandidates()
	state := s.engine.Snapshot()
	recent := make(map[string]bool)
	for _, node := range state.Nodes {
		if !node.LastCheck.IsZero() && now.Sub(node.LastCheck) < 5*time.Second {
			recent[node.ID] = true
		}
	}
	for _, id := range ids {
		if !tried[id] && !initial || s.pending(id) || containsStable(state.Active, id) || containsStable(state.Warm, id) {
			continue
		}
		if recent[id] {
			continue
		}
		s.mu.Lock()
		retry := s.recoveryNodes[id]
		s.mu.Unlock()
		if now.Before(retry.next) {
			continue
		}
		if s.launchProbeMode(ctx, id, true, true, true) {
			break
		}
	}
	if batch <= 0 {
		return
	}
	// Historical favorites must not consume every recovery credit on a new path.
	sort.SliceStable(ids, func(i, j int) bool { return !tried[ids[i]] && tried[ids[j]] })
	if requested != "" && containsStable(ids, requested) {
		next := []string{requested}
		for _, id := range ids {
			if id != requested {
				next = append(next, id)
			}
		}
		ids = next
	}
	for _, id := range ids {
		if s.engine.FreshActiveCount() >= 3 {
			return
		}
		if tried[id] {
			continue
		}
		if s.pending(id) {
			continue
		}
		s.mu.Lock()
		retry := s.recoveryNodes[id]
		s.mu.Unlock()
		if now.Before(retry.next) {
			continue
		}
		if recent[id] {
			continue
		}
		probeCtx := ctx
		if !tried[id] && s.engine.PriorityAvailable(stability.SearchProbe, 2) {
			probeCtx = context.WithValue(ctx, stableSearchKey{}, true)
		}
		if s.launchProbeMode(probeCtx, id, true, true, true) {
			batch--
		}
		if batch == 0 {
			return
		}
	}
}

func (s *Stable) recoveryResult(id string, sample stability.Sample, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return
	}
	if sample.OK && err == nil {
		delete(s.recoveryNodes, id)
		return
	}
	if sample.Error == "cancelled" {
		return
	}
	if sample.Error == "budget_exhausted" {
		delete(s.startupTried, id)
		return
	}
	if sample.Error != "budget_exhausted" {
		s.recoveryFailures++
	}
	if s.recoveryNodes == nil {
		s.recoveryNodes = map[string]recoveryNode{}
	}
	node := s.recoveryNodes[id]
	if sample.Error != "budget_exhausted" {
		node.failures++
	}
	step := node.failures / 3
	if step > 4 {
		step = 4
	}
	delay := 2 * time.Second * time.Duration(1<<step)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	node.next = time.Now().Add(delay)
	s.recoveryNodes[id] = node
}

func (s *Stable) readyChanged() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready == nil {
		s.ready = make(chan struct{})
	}
	return s.ready
}

func (s *Stable) notifyReadyLocked() {
	if s.ready != nil {
		close(s.ready)
	}
	s.ready = make(chan struct{})
}

func (s *Stable) awaitStartup(ctx, owner context.Context) error {
	for {
		changed := s.readyChanged()
		if len(s.engine.Attempts()) > 0 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if owner.Err() != nil {
			return owner.Err()
		}
		s.startupProbes(owner, true)
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-owner.Done():
			timer.Stop()
			return owner.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
