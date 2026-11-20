package stability

import (
	"runtime"
	"sort"
	"time"
)

func (e *Engine) warmLocked(now time.Time) []string {
	if runtime.GOOS == "android" {
		return nil
	}
	nodes := []*Stats{}
	for _, id := range e.order {
		s := e.nodes[id]
		if !contains(e.active, id) && e.ready(s, now) && s.LastError == "" && now.Sub(s.LastSuccess) < 5*time.Minute {
			nodes = append(nodes, s)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := ReliabilityTier(*nodes[i]), ReliabilityTier(*nodes[j])
		if a != b {
			return a < b
		}
		return LatencyCost(*nodes[i]) < LatencyCost(*nodes[j])
	})
	ids := []string{}
	for _, s := range nodes {
		ids = append(ids, s.ID)
		if len(ids) == 2 {
			break
		}
	}
	return ids
}

// Reliability is compared before latency. Too few observations are unknown,
// rather than a perfect success rate or zero jitter.
func ReliabilityTier(s Stats) int {
	if len(s.Recent) < 5 {
		return 1
	}
	failed := 0
	for _, ok := range s.Recent {
		if !ok {
			failed++
		}
	}
	ratio := float64(failed) / float64(len(s.Recent))
	if ratio <= .10 {
		return 0
	}
	if ratio <= .30 {
		return 2
	}
	return 3
}

// AcceptDial checks qualification at commit time without making a single
// target's hedge winner the global primary for every other destination.
func (e *Engine) AcceptDial(id string, epoch uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.nodes[id]
	return epoch == e.epoch && s != nil && e.fresh(s, e.now())
}

func HealthState(s Stats, now time.Time) string {
	if s.Quarantined {
		return "isolated"
	}
	if now.Before(s.CoolingUntil) {
		return "cooling"
	}
	if now.Before(s.SuspectUntil) {
		return "suspect"
	}
	if s.LastError != "" {
		return "failed"
	}
	if s.TLSAt.IsZero() || s.ExitAt.IsZero() || now.Sub(s.TLSAt) > 10*time.Minute || now.Sub(s.ExitAt) > 30*time.Minute || now.Sub(s.LastSuccess) > 90*time.Second {
		return "unverified"
	}
	return "healthy"
}
