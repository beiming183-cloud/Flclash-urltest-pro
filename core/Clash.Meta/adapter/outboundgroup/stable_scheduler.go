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

func competitionBudgetAvailable(state stability.Snapshot) bool {
	remaining := state.BudgetResetIn
	if remaining <= 0 || remaining > 3600 {
		remaining = 3600
	}
	maintenance := (remaining+5)/6 + 12
	return state.BudgetUsed < 780 && state.BudgetUsed+maintenance < 840
}

func competitionCandidate(state stability.Snapshot, now time.Time, pending func(string) bool) string {
	active := map[string]bool{}
	worstCost := 0.0
	for _, id := range state.Active {
		active[id] = true
		for _, node := range state.Nodes {
			if node.ID == id {
				if cost := stability.LatencyCost(node); cost > worstCost {
					worstCost = cost
				}
			}
		}
	}
	candidates := []stability.Stats{}
	for _, node := range state.Nodes {
		if active[node.ID] || pending(node.ID) || node.Quarantined || now.Before(node.CoolingUntil) || now.Sub(node.LastCheck) < 5*time.Second {
			continue
		}
		good := node.LastError == "" && !node.LastSuccess.IsZero() && now.Sub(node.LastSuccess) < 15*time.Minute
		needsValidation := node.TLSAt.IsZero() || node.ExitAt.IsZero()
		if !needsValidation && stability.CompetitionReady(node) && now.Sub(node.LastSuccess) < 90*time.Second {
			continue
		}
		if len(state.Active) >= 3 && (!good || stability.LatencyCost(node)+15 >= worstCost) {
			continue
		}
		candidates = append(candidates, node)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		progressA := !a.LastSuccess.IsZero() && a.LastError == "" && now.Sub(a.LastSuccess) < 90*time.Second
		progressB := !b.LastSuccess.IsZero() && b.LastError == "" && now.Sub(b.LastSuccess) < 90*time.Second
		if progressA != progressB {
			return progressA
		}
		if stability.LatencyCost(a) != stability.LatencyCost(b) {
			return stability.LatencyCost(a) < stability.LatencyCost(b)
		}
		return a.LastCheck.Before(b.LastCheck)
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].ID
}

func (s *Stable) launchProbe(ctx context.Context, id string, verify bool, fault ...bool) bool {
	return s.launchProbeMode(ctx, id, verify, false, fault...)
}

func (s *Stable) launchProbeMode(ctx context.Context, id string, verify, quick bool, fault ...bool) bool {
	s.mu.Lock()
	limit := 2
	if quick {
		limit = 4
	}
	if s.cancel == nil || ctx.Err() != nil || s.scheduled[id] || s.probing[id] || len(s.scheduled) >= limit {
		s.mu.Unlock()
		return false
	}
	s.scheduled[id] = true
	if quick {
		s.startupTried[id] = true
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.scheduled, id)
			s.notifyReadyLocked()
			s.mu.Unlock()
			s.wg.Done()
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}()
		if len(s.engine.Snapshot().Active) == 0 {
			reason := "startup_revalidation"
			if quick {
				reason = "startup_availability"
			}
			s.log("startup_probe", reason, id, false, 0)
		}
		emergency := len(fault) > 0 && fault[0]
		_, _ = s.probeCandidate(ctx, id, verify, true, emergency, quick)
	}()
	return true
}

func (s *Stable) schedule(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastExplore := time.Time{}
	coldIndex := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
		if len(s.engine.Snapshot().Active) == 0 {
			s.startupProbes(ctx)
			s.journal.BusinessTick(s.groupID, s.engine.Snapshot())
			continue
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
					if !stability.CompetitionReady(node) {
						interval = 5 * time.Second
						if node.EWMA <= 0 {
							interval = 0
						}
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
			if task == "" && competitionBudgetAvailable(state) {
				task = competitionCandidate(state, now, s.pending)
			}
			if task == "" && competitionBudgetAvailable(state) && now.Sub(lastExplore) >= time.Minute {
				for n := 0; n < len(state.Nodes); n++ {
					node := state.Nodes[coldIndex%len(state.Nodes)]
					coldIndex++
					if !containsStable(state.Active, node.ID) && !node.Quarantined && !s.pending(node.ID) && !now.Before(node.CoolingUntil) {
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
		s.journal.BusinessTick(s.groupID, s.engine.Snapshot())
	}
}
