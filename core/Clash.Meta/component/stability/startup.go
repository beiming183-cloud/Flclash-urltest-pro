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
	if validID(e.network) {
		if e.networkHints == nil {
			e.networkHints = map[string]map[string]StartupHint{}
		}
		if e.networkHints[e.network] == nil {
			e.networkHints[e.network] = map[string]StartupHint{}
		}
		e.networkHints[e.network][s.ID] = e.startup[s.ID]
	}
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
	// During a common outage every soft-failed route can be cooling. Permit
	// one bounded half-open validation rather than waiting up to 30 minutes.
	// This is a probe candidate only: no eligibility is restored here, and
	// hard certificate/exit quarantines are never included.
	if len(ids) == 0 && len(e.active) == 0 {
		oldest := ""
		for _, id := range e.order {
			s := e.nodes[id]
			if !s.Quarantined && (oldest == "" || s.CoolingUntil.Before(e.nodes[oldest].CoolingUntil)) {
				oldest = id
			}
		}
		if oldest != "" {
			ids = append(ids, oldest)
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
	// Prefer independent failure domains in the first recovery wave, keeping
	// the complete list available for later fair attempts.
	ordered := []string{}
	relays, airports := map[string]bool{}, map[string]bool{}
	for pass := 0; pass < 3; pass++ {
		for _, id := range ids {
			s := e.nodes[id]
			if contains(ordered, id) || pass < 2 && relays[s.Relay] || pass == 0 && airports[s.Airport] {
				continue
			}
			ordered = append(ordered, id)
			relays[s.Relay], airports[s.Airport] = true, true
		}
	}
	// Preserve per-airport ranking and relay diversity, but keep rotating failure
	// domains after the first wave. A large unreachable airport must not consume
	// every recovery credit while another airport has untried healthy routes.
	airportsInOrder := []string{}
	queues := map[string][]string{}
	for _, id := range ordered {
		airport := e.nodes[id].Airport
		if _, exists := queues[airport]; !exists {
			airportsInOrder = append(airportsInOrder, airport)
		}
		queues[airport] = append(queues[airport], id)
	}
	fair := make([]string, 0, len(ordered))
	for len(fair) < len(ordered) {
		for _, airport := range airportsInOrder {
			if queue := queues[airport]; len(queue) > 0 {
				fair = append(fair, queue[0])
				queues[airport] = queue[1:]
			}
		}
	}
	return fair
}
