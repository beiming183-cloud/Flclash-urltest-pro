package outboundgroup

import (
	"context"
	"time"
)

func (s *Stable) startupProbes(ctx context.Context) {
	state := s.engine.Snapshot()
	if len(state.Active) > 0 {
		return
	}
	now := time.Now()
	for _, id := range s.engine.StartupCandidates() {
		if s.pending(id) {
			continue
		}
		recent := false
		for _, node := range state.Nodes {
			if node.ID == id && !node.LastCheck.IsZero() && now.Sub(node.LastCheck) < 5*time.Second {
				recent = true
				break
			}
		}
		if recent {
			continue
		}
		if !s.launchProbe(ctx, id, true) {
			return
		}
	}
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
		s.startupProbes(owner)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-owner.Done():
			return owner.Err()
		case <-changed:
		}
	}
}
