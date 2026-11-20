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

func refillBudgetAvailable(state stability.Snapshot) bool {
	b, ok := state.ProbeBudgets[stability.RecoveryProbe]
	return ok && b.Tokens >= 2
}

func needsStableValidation(node stability.Stats, now time.Time) bool {
	return node.TLSAt.IsZero() || now.Sub(node.TLSAt) >= 9*time.Minute ||
		node.ExitAt.IsZero() || now.Sub(node.ExitAt) >= 29*time.Minute
}

func schedulerDiagnostics(state stability.Snapshot, now time.Time) map[string]any {
	counts := map[string]int{"total": len(state.Nodes), "active": len(state.Active), "identityRecheck": 0, "qualityWarming": 0, "cooling": 0, "failed": 0, "verified": 0}
	for _, node := range state.Nodes {
		switch {
		case node.Quarantined || now.Before(node.CoolingUntil):
			counts["cooling"]++
		case node.LastError != "":
			counts["failed"]++
		case needsStableValidation(node, now):
			counts["identityRecheck"]++
		case !stability.CompetitionReady(node):
			counts["qualityWarming"]++
		default:
			counts["verified"]++
		}
	}
	return map[string]any{"candidates": counts, "competitionBudgetAvailable": competitionBudgetAvailable(state), "refillNeeded": len(state.Active) < 3, "refillBudgetAvailable": refillBudgetAvailable(state)}
}

func competitionCandidate(state stability.Snapshot, now time.Time, pending func(string) bool) string {
	active := map[string]bool{}
	airports := map[string]int{}
	worstCost := 0.0
	for _, id := range state.Active {
		active[id] = true
		for _, node := range state.Nodes {
			if node.ID == id {
				if node.Airport != "" {
					airports[node.Airport]++
				}
				if cost := stability.LatencyCost(node); cost > worstCost {
					worstCost = cost
				}
			}
		}
	}
	diversityCost := 0.0
	for _, node := range state.Nodes {
		if active[node.ID] && node.ID != state.Primary && airports[node.Airport] > 1 && stability.LatencyCost(node) > diversityCost {
			diversityCost = stability.LatencyCost(node)
		}
	}
	candidates := []stability.Stats{}
	for _, node := range state.Nodes {
		if active[node.ID] || containsStable(state.Warm, node.ID) || pending(node.ID) || node.Quarantined || now.Before(node.CoolingUntil) || now.Sub(node.LastCheck) < 5*time.Second {
			continue
		}
		good := node.LastError == "" && !node.LastSuccess.IsZero() && now.Sub(node.LastSuccess) < 15*time.Minute
		needsValidation := needsStableValidation(node, now)
		if !needsValidation && stability.CompetitionReady(node) && now.Sub(node.LastSuccess) < 90*time.Second {
			continue
		}
		measured := node.EWMA > 0 && node.P95 > 0
		diversity := node.Airport != "" && airports[node.Airport] == 0 && diversityCost > 0 &&
			stability.LatencyCost(node) <= diversityCost*1.25+25
		if len(state.Active) >= 3 && measured && (!good || (!diversity && stability.LatencyCost(node)+15 >= worstCost)) {
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
		if len(state.Active) > 0 && len(state.Active) < 3 && !progressA && !progressB {
			// Refill is an availability search, not a stale latency competition.
			// Try untouched routes before repeating failures, using the currently
			// reachable airport first. Normal full-pool diversity still runs below.
			untriedA, untriedB := a.LastCheck.IsZero(), b.LastCheck.IsZero()
			if untriedA != untriedB {
				return untriedA
			}
			if untriedA {
				reachableA, reachableB := airports[a.Airport] > 0, airports[b.Airport] > 0
				if reachableA != reachableB {
					return reachableA
				}
			} else if !a.LastCheck.Equal(b.LastCheck) {
				return a.LastCheck.Before(b.LastCheck)
			}
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

func stableProbePolicy(state stability.Snapshot, id string, verify, quick, fault bool) (bool, bool, stability.ProbeClass, int) {
	for _, node := range state.Nodes {
		if node.ID == id && node.Health == "suspect" {
			verify, quick = true, true
			break
		}
	}
	var priority stability.ProbeClass
	active := containsStable(state.Active, id)
	warm := containsStable(state.Warm, id)
	if len(state.Active) == 0 || (quick || fault) && !active && !warm {
		priority = stability.RecoveryProbe
	} else if fault || active || warm {
		priority = stability.MaintenanceProbe
	}
	credits := 4
	if quick {
		credits = 2
	}
	if verify && !quick {
		credits = 6
	}
	return verify, quick, priority, credits
}

type stableProbeLeaseKey struct{}

func (s *Stable) launchProbeMode(ctx context.Context, id string, verify, quick bool, fault ...bool) bool {
	emergency := len(fault) > 0 && fault[0]
	var priority stability.ProbeClass
	var credits int
	verify, quick, priority, credits = stableProbePolicy(s.engine.Snapshot(), id, verify, quick, emergency)
	if quality, _ := ctx.Value(stableQualityKey{}).(bool); quality {
		priority = ""
	}
	search, _ := ctx.Value(stableSearchKey{}).(bool)
	if search {
		if !s.optionalTrafficAvailable(time.Now()) {
			return false
		}
		priority = stability.SearchProbe
	}
	var release func()
	if priority != "" {
		consume, refund, ok := s.engine.LeaseProbe(priority, credits)
		if !ok {
			return false
		}
		release = refund
		ctx = context.WithValue(ctx, stableProbeLeaseKey{}, consume)
	}
	s.mu.Lock()
	quickCount := len(s.quickScheduled) - len(s.confirmationScheduled)
	quickLimit, totalLimit := 3, 4
	if len(s.engine.Snapshot().Active) == 0 {
		quickLimit, totalLimit = 8, 8
	}
	classFull := !quick && len(s.scheduled)-len(s.quickScheduled) >= 2 || quick && quickCount >= quickLimit
	confirmation := quick && !search && priority == stability.RecoveryProbe
	if confirmation {
		classFull = len(s.confirmationScheduled) >= 2
		totalLimit = len(s.scheduled) + 1
	}
	if priority == stability.MaintenanceProbe {
		classFull = false
		totalLimit = len(s.scheduled) + 1
	}
	if s.cancel == nil || ctx.Err() != nil || s.scheduled[id] || s.probing[id] || len(s.scheduled)-len(s.confirmationScheduled) >= totalLimit || classFull {
		s.mu.Unlock()
		if release != nil {
			release()
		}
		return false
	}
	s.scheduled[id] = true
	if confirmation {
		if s.confirmationScheduled == nil {
			s.confirmationScheduled = map[string]bool{}
		}
		s.confirmationScheduled[id] = true
	}
	if quick {
		if s.quickScheduled == nil {
			s.quickScheduled = map[string]bool{}
		}
		s.quickScheduled[id] = true
		s.startupTried[id] = true
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		if release != nil {
			defer release()
		}
		defer func() {
			s.mu.Lock()
			delete(s.scheduled, id)
			delete(s.quickScheduled, id)
			delete(s.confirmationScheduled, id)
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
		sample, err := s.probeCandidate(ctx, id, verify, true, emergency, quick)
		if quick {
			s.recoveryResult(id, sample, err)
		}
	}()
	return true
}

func (s *Stable) schedule(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastExplore := time.Time{}
	lastRefill := time.Time{}
	coldIndex := 0
	started := time.Now()
	explored := map[string]bool{}
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
		s.mu.Lock()
		s.recoveryHadPrimary = true
		if len(s.engine.Snapshot().Active) >= 3 {
			s.recoveryActive = false
		}
		finishSearch := s.recoveryActive && len(s.engine.Snapshot().Active) < 3
		s.mu.Unlock()
		if finishSearch {
			// First success restores traffic immediately; finish the same bounded
			// pass for standby routes, instead of restarting at old failed routes.
			s.startupProbes(ctx)
		}
		for slot := 0; slot < 2; slot++ {
			now := time.Now()
			state := s.engine.Snapshot()
			task := stableMaintenanceCandidate(state, now, s.pending)
			exploration := false
			s.mu.Lock()
			requested := s.requested
			s.mu.Unlock()
			if task == "" && requested != "" && !s.pending(requested) {
				for _, node := range state.Nodes {
					if node.ID == requested && !now.Before(node.CoolingUntil) && now.Sub(node.LastCheck) >= 5*time.Second {
						task = requested
					}
				}
			}
			if task == "" {
				s.mu.Lock()
				for id := range s.recheck {
					due := true
					for _, node := range state.Nodes {
						if node.ID == id && now.Sub(node.LastCheck) < 3*time.Second {
							due = false
						}
					}
					if due && !s.scheduled[id] && !s.probing[id] {
						task = id
						delete(s.recheck, id)
						break
					}
				}
				s.mu.Unlock()
			}
			for _, id := range state.Active {
				if task != "" {
					break
				}
				for _, node := range state.Nodes {
					interval := 30 * time.Second
					if node.ID == id && node.Health == "suspect" {
						interval = 3 * time.Second
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
			// Keep a bounded, independently validated reserve outside the three
			// active slots. Refresh it without racing the active maintenance.
			if task == "" {
				for _, id := range state.Warm {
					for _, node := range state.Nodes {
						if node.ID == id && !s.pending(id) && now.Sub(node.LastCheck) >= 60*time.Second {
							task = id
							break
						}
					}
					if task != "" {
						break
					}
				}
			}
			// Restoring redundancy is maintenance, not optional competition.
			// Use the existing fault reserve, with a bounded cadence for cold
			// candidates, so a paused search cannot strand a pool at 1/3 or 2/3.
			refill := false
			warmFill := false
			if task == "" && len(state.Active) < 3 && refillBudgetAvailable(state) && now.Sub(lastRefill) >= 5*time.Second {
				task = competitionCandidate(state, now, s.pending)
				refill = task != ""
			}
			if task == "" && len(state.Active) == 3 && len(state.Warm) < stableWarmTarget() && competitionBudgetAvailable(state) && s.optionalQualityAvailable(now) {
				task = competitionCandidate(state, now, s.pending)
				warmFill = task != ""
			}
			// Fair exploration gets a turn before optional score competition.
			// A once-slow node must not be permanently invisible to the selector.
			if task == "" && competitionBudgetAvailable(state) && s.optionalQualityAvailable(now) && now.Sub(lastExplore) >= 30*time.Second {
				for n := 0; n < len(state.Nodes); n++ {
					node := state.Nodes[coldIndex%len(state.Nodes)]
					coldIndex++
					if !containsStable(state.Active, node.ID) && !node.Quarantined && !s.pending(node.ID) && !now.Before(node.CoolingUntil) {
						task = node.ID
						exploration = true
						break
					}
				}
			}
			if task == "" && competitionBudgetAvailable(state) && s.optionalQualityAvailable(now) {
				task = competitionCandidate(state, now, s.pending)
			}
			if task == "" {
				break
			}
			cold := false
			for _, node := range state.Nodes {
				if node.ID == task {
					cold = node.EWMA <= 0
					break
				}
			}
			if cold && !containsStable(state.Active, task) && !refill {
				burst := now.Sub(started) < time.Minute && len(explored) < 12
				if !burst && now.Sub(lastExplore) < 30*time.Second {
					break
				}
			}
			if task != state.Primary && !containsStable(state.Active, task) && !containsStable(state.Warm, task) && !refill {
				warming := false
				for _, node := range state.Nodes {
					if node.ID == state.Primary {
						warming = !stability.CompetitionReady(node)
						break
					}
				}
				s.mu.Lock()
				busy := len(s.scheduled)
				s.mu.Unlock()
				if warming && busy > 0 {
					break
				}
			}
			verify := false
			for _, node := range state.Nodes {
				if node.ID == task {
					verify = needsStableValidation(node, now)
				}
			}
			maintain := containsStable(state.Active, task) || containsStable(state.Warm, task)
			probeCtx := ctx
			if warmFill {
				probeCtx = context.WithValue(ctx, stableQualityKey{}, true)
			}
			for _, node := range state.Nodes {
				if node.ID == task && maintain && !verify && node.Health != "suspect" && now.Sub(node.LastSuccess) < stableHealthInterval(state.PowerSave) {
					probeCtx = context.WithValue(ctx, stableQualityKey{}, true)
				}
			}
			probeQuick := refill || warmFill
			if probeQuick {
				verify = true
			}
			_, _, priority, credits := stableProbePolicy(state, task, verify, probeQuick, refill || maintain)
			if quality, _ := probeCtx.Value(stableQualityKey{}).(bool); quality {
				if !s.optionalQualityAvailable(now) {
					break
				}
				priority = ""
			}
			if priority != "" && !s.engine.PriorityAvailable(priority, credits) {
				break
			}
			if !s.launchProbeMode(probeCtx, task, verify, probeQuick, refill || maintain) {
				break
			}
			if refill {
				lastRefill = now
			}
			if exploration {
				lastExplore = now
			}
			if cold && !containsStable(state.Active, task) {
				explored[task] = true
				lastExplore = now
			}
		}
		s.checkTargetTLS(ctx)
		s.journal.BusinessTick(s.groupID, s.engine.Snapshot())
	}
}
