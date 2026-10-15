package stability

import (
	"math"
	"sort"
	"time"
)

type StartupHint struct {
	Node       string    `json:"nodeId"`
	Score      float64   `json:"score"`
	VerifiedAt time.Time `json:"verifiedAt"`
	CheckedAt  time.Time `json:"checkedAt"`
	Error      string    `json:"error,omitempty"`
}

func (e *Engine) rememberStartup(s *Stats) {
	if s.LastSuccess.IsZero() || s.TLSAt.IsZero() || s.ExitAt.IsZero() {
		return
	}
	score := LatencyCost(*s)
	if math.IsInf(score, 0) || math.IsNaN(score) || score <= 0 {
		return
	}
	verifiedAt := s.ExitAt
	if s.TLSAt.Before(verifiedAt) {
		verifiedAt = s.TLSAt
	}
	e.startup[s.ID] = StartupHint{Node: s.ID, Score: score, VerifiedAt: verifiedAt, CheckedAt: s.LastCheck, Error: s.LastError}
}

func (e *Engine) StartupCandidates() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	ids := []string{}
	for _, id := range e.order {
		s := e.nodes[id]
		if !s.Quarantined && !now.Before(s.CoolingUntil) {
			ids = append(ids, id)
		}
	}
	score := func(id string) float64 {
		hint, ok := e.startup[id]
		age := now.Sub(hint.VerifiedAt)
		if !ok || hint.Error != "" || age < 0 || age > 24*time.Hour {
			return math.Inf(1)
		}
		return hint.Score
	}
	sort.SliceStable(ids, func(i, j int) bool { return score(ids[i]) < score(ids[j]) })
	return ids
}
