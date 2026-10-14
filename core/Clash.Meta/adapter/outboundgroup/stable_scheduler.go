package outboundgroup

import (
	"context"
	"sort"
	"time"

	"github.com/metacubex/mihomo/component/stability"
)

func (s *Stable) ObserveDelay(name, url string, delay uint16) {
	id := s.byName[name]
	if id == "" || url != "https://www.gstatic.com/generate_204" {
		return
	}
	s.mu.Lock()
	busy := s.probing[id]
	s.mu.Unlock()
	if busy {
		return
	}
	state := s.engine.Snapshot()
	sample := stability.Sample{Epoch: state.Epoch, OK: delay > 0, Latency: time.Duration(delay) * time.Millisecond, Phase: "delay", Service: "control"}
	if delay == 0 {
		sample.Error = "https_timeout"
	}
	s.engine.Record(id, sample, false)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Stable) pending(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scheduled[id] || s.probing[id]
}

func (s *Stable) launchProbe(ctx context.Context, id string, verify bool) bool {
	s.mu.Lock()
	if s.cancel == nil || s.scheduled[id] || s.probing[id] || len(s.scheduled) >= 2 {
		s.mu.Unlock()
		return false
	}
	s.scheduled[id] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.scheduled, id)
			s.mu.Unlock()
			s.wg.Done()
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}()
		_, _ = s.Probe(ctx, id, verify, true)
	}()
	return true
}

func (s *Stable) schedule(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastExplore := time.Time{}
	warmIndex, coldIndex := 0, 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
		for slot := 0; slot < 2; slot++ {
			now := time.Now()
			state := s.engine.Snapshot()
			if state.BudgetUsed >= 840 {
				break
			}
			task := ""
			s.mu.Lock()
			requested := s.requested
			s.mu.Unlock()
			if requested != "" && !s.pending(requested) {
				for _, node := range state.Nodes {
					if node.ID == requested && !now.Before(node.CoolingUntil) && now.Sub(node.LastCheck) >= 5*time.Second {
						task = requested
					}
				}
			}
			for _, id := range state.Active {
				if task != "" {
					break
				}
				for _, node := range state.Nodes {
					interval := 30 * time.Second
					if id == state.Primary {
						interval = 15 * time.Second
					}
					if state.PowerSave {
						interval *= 3
					}
					if node.ID == id && !s.pending(id) && now.Sub(node.LastCheck) >= interval {
						task = id
						break
					}
				}
				if task != "" {
					break
				}
			}
			ordered := append([]stability.Stats(nil), state.Nodes...)
			sort.SliceStable(ordered, func(i, j int) bool {
				a, b := ordered[i], ordered[j]
				goodA := a.LastError == "" && !a.LastSuccess.IsZero() && now.Sub(a.LastSuccess) < 90*time.Second
				goodB := b.LastError == "" && !b.LastSuccess.IsZero() && now.Sub(b.LastSuccess) < 90*time.Second
				if goodA != goodB {
					return goodA
				}
				return goodA && a.EWMA < b.EWMA
			})
			if task == "" && len(state.Active) < 3 && state.BudgetUsed < 600 {
				for n := 0; n < len(ordered); n++ {
					i := warmIndex % len(ordered)
					warmIndex++
					node := ordered[i]
					if i >= 20 {
						continue
					}
					needsValidation := node.TLSAt.IsZero() || node.ExitAt.IsZero()
					warming := node.Streak < 3 && now.Sub(node.LastCheck) >= 5*time.Second
					if !s.pending(node.ID) && !node.Quarantined && !now.Before(node.CoolingUntil) && (needsValidation || warming) && now.Sub(node.LastCheck) >= 5*time.Second {
						task = node.ID
						break
					}
				}
			}
			if task == "" && state.BudgetUsed < 600 && now.Sub(lastExplore) >= time.Minute {
				for n := 0; n < len(state.Nodes); n++ {
					node := state.Nodes[coldIndex%len(state.Nodes)]
					coldIndex++
					if !s.pending(node.ID) && !now.Before(node.CoolingUntil) {
						task = node.ID
						lastExplore = now
						break
					}
				}
			}
			if task == "" {
				break
			}
			verify := false
			for _, node := range state.Nodes {
				if node.ID == task {
					verify = node.TLSAt.IsZero() || now.Sub(node.TLSAt) >= 9*time.Minute || node.ExitAt.IsZero() || now.Sub(node.ExitAt) >= 29*time.Minute
				}
			}
			if !s.launchProbe(ctx, task, verify) {
				break
			}
		}
		s.checkTargetTLS(ctx)
	}
}
