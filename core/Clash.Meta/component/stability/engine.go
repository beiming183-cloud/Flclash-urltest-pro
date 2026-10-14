//go:build go1.21

package stability

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

const MaxCandidates = 512
const latencyWindow = 12

type Candidate struct {
	ID      string `json:"id"`
	Relay   string `json:"relayId"`
	Airport string `json:"airportId"`
}

type Sample struct {
	Epoch   uint64
	OK      bool
	TLS     bool
	Exit    bool
	Hard    bool
	Latency time.Duration
	Error   string
	Phase   string
	Service string
	TLSInfo []TLSInfo
}

type TLSInfo struct {
	Service     string    `json:"serviceTag"`
	Certificate string    `json:"certSha256"`
	SPKI        string    `json:"spkiSha256"`
	SANCount    int       `json:"sanCount"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	Version     uint16    `json:"version"`
	Verified    bool      `json:"verified"`
	CheckedAt   time.Time `json:"checkedAt"`
	Epoch       uint64    `json:"networkEpoch"`
}

type Stats struct {
	Candidate
	Success      float64   `json:"success"`
	Failure      float64   `json:"failure"`
	Samples      []float64 `json:"-"`
	EWMA         float64   `json:"ewmaMs"`
	P95          float64   `json:"p95Ms"`
	P50          float64   `json:"p50Ms"`
	Jitter       float64   `json:"jitterMs"`
	Recent       []bool    `json:"recentResults,omitempty"`
	Lower        float64   `json:"reliabilityLower"`
	Count        int       `json:"sampleCount"`
	LastSuccess  time.Time `json:"lastSuccess"`
	LastCheck    time.Time `json:"lastCheck"`
	FirstSuccess time.Time `json:"-"`
	TLSAt        time.Time `json:"tlsAt"`
	ExitAt       time.Time `json:"exitAt"`
	CoolingUntil time.Time `json:"coolingUntil"`
	Streak       int       `json:"successStreak"`
	LastPhase    string    `json:"phase,omitempty"`
	LastService  string    `json:"serviceTag,omitempty"`
	Failures     int       `json:"-"`
	Cooldowns    int       `json:"-"`
	LastError    string    `json:"error"`
	Quarantined  bool      `json:"quarantined"`
	decayedAt    time.Time
	lastFailure  time.Time
}

type Snapshot struct {
	Epoch       uint64             `json:"networkEpoch"`
	Generation  uint64             `json:"generation"`
	Primary     string             `json:"primary"`
	Active      []string           `json:"active"`
	Nodes       []Stats            `json:"nodes"`
	Reason      string             `json:"lastSwitchReason"`
	BudgetUsed  int                `json:"budgetUsed"`
	BudgetLimit int                `json:"budgetLimit"`
	Paused      bool               `json:"paused"`
	PowerSave   bool               `json:"powerSave"`
	Running     bool               `json:"running"`
	NetworkKey  string             `json:"networkKey,omitempty"`
	Networks    map[string][]Stats `json:"networks,omitempty"`
}

type Engine struct {
	mu          sync.Mutex
	now         func() time.Time
	nodes       map[string]*Stats
	order       []string
	epoch       uint64
	generation  uint64
	primary     string
	active      []string
	reason      string
	paused      bool
	powerSave   bool
	budgetStart time.Time
	budget      int
	network     string
	networks    map[string][]Stats
	networkLRU  []string
	lastSwitch  time.Time
	manual      string
}

func New(candidates []Candidate, now func() time.Time) (*Engine, error) {
	if len(candidates) == 0 || len(candidates) > MaxCandidates {
		return nil, errors.New("invalid candidate count")
	}
	if now == nil {
		now = time.Now
	}
	e := &Engine{now: now, nodes: make(map[string]*Stats), epoch: 1, generation: 1, budgetStart: now(), network: "startup", networks: map[string][]Stats{}}
	for _, c := range candidates {
		if c.ID == "" || c.Relay == "" || e.nodes[c.ID] != nil {
			return nil, errors.New("invalid candidate identity")
		}
		e.nodes[c.ID] = &Stats{Candidate: c, decayedAt: now()}
		e.order = append(e.order, c.ID)
	}
	return e, nil
}

func (e *Engine) decay(s *Stats, now time.Time) {
	f := math.Exp2(-now.Sub(s.decayedAt).Hours() / 24)
	s.Success *= f
	s.Failure *= f
	s.decayedAt = now
	n := s.Success + s.Failure
	if n == 0 {
		s.Lower = 0
		return
	}
	p := s.Success / n
	z := 1.96
	s.Lower = (p + z*z/(2*n) - z*math.Sqrt((p*(1-p)+z*z/(4*n))/n)) / (1 + z*z/n)
}

func (e *Engine) ready(s *Stats, now time.Time) bool {
	confirmed := s.Streak >= 3 && s.LastSuccess.Sub(s.FirstSuccess) >= 10*time.Second
	return !s.Quarantined && !now.Before(s.CoolingUntil) && s.Streak >= 1 &&
		(confirmed || s.Cooldowns == 0) && s.LastError == "" &&
		now.Sub(s.TLSAt) <= 10*time.Minute && !s.TLSAt.IsZero() &&
		now.Sub(s.ExitAt) <= 30*time.Minute && !s.ExitAt.IsZero()
}

func (e *Engine) fresh(s *Stats, now time.Time) bool {
	age := 90 * time.Second
	if e.powerSave {
		age = 180 * time.Second
	}
	return e.ready(s, now) && now.Sub(s.LastSuccess) <= age
}

func CompetitionReady(s Stats) bool {
	return s.Streak >= 3 && s.LastSuccess.Sub(s.FirstSuccess) >= 10*time.Second
}

func LatencyCost(s Stats) float64 {
	if s.EWMA <= 0 || s.P95 <= 0 {
		return math.Inf(1)
	}
	p50 := s.P50
	if p50 <= 0 {
		p50 = s.EWMA
	}
	jitter := math.Max(s.P95-p50, 0)
	cost := .5*p50 + .3*s.P95 + .2*s.EWMA + .5*jitter
	if len(s.Recent) > 0 {
		failures := 0
		for _, ok := range s.Recent {
			if !ok {
				failures++
			}
		}
		cost *= 1 + 4*float64(failures)/float64(len(s.Recent))
	}
	return cost
}

func (e *Engine) choose(now time.Time) {
	pool := []*Stats{}
	for _, id := range e.order {
		s := e.nodes[id]
		e.decay(s, now)
		if e.fresh(s, now) {
			pool = append(pool, s)
		}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		qualifiedI, qualifiedJ := CompetitionReady(*pool[i]), CompetitionReady(*pool[j])
		if qualifiedI != qualifiedJ {
			return qualifiedI
		}
		return LatencyCost(*pool[i]) < LatencyCost(*pool[j])
	})
	old := e.primary
	if old != "" && !e.fresh(e.nodes[old], now) {
		e.primary = ""
	}
	if e.primary == "" && !e.paused && len(pool) > 0 {
		e.primary = pool[0].ID
	}
	if e.manual != "" && e.fresh(e.nodes[e.manual], now) {
		e.primary = e.manual
	}
	if e.primary != "" && e.manual == "" && !e.paused && len(pool) > 0 && now.Sub(e.lastSwitch) >= 30*time.Second {
		current, better := e.nodes[e.primary], pool[0]
		currentCost, betterCost := LatencyCost(*current), LatencyCost(*better)
		if better.ID != current.ID && CompetitionReady(*better) &&
			betterCost < currentCost*.88 && betterCost+25 < currentCost {
			e.primary = better.ID
			e.reason = "latency_quality_improved"
		}
	}
	e.active = nil
	if e.primary != "" && e.fresh(e.nodes[e.primary], now) {
		e.active = append(e.active, e.primary)
	}
	relays, airports := map[string]bool{}, map[string]bool{}
	if len(e.active) != 0 {
		s := e.nodes[e.primary]
		relays[s.Relay] = true
		airports[s.Airport] = true
	}
	for pass := 0; pass < 3 && len(e.active) < 3; pass++ {
		for _, s := range pool {
			if pass == 0 && len(pool) > 0 && LatencyCost(*s) > LatencyCost(*pool[0])*1.25+25 {
				continue
			}
			if contains(e.active, s.ID) || (pass < 2 && relays[s.Relay]) || (pass == 0 && airports[s.Airport]) {
				continue
			}
			e.active = append(e.active, s.ID)
			relays[s.Relay] = true
			airports[s.Airport] = true
			if len(e.active) == 3 {
				break
			}
		}
	}
	if old == "" && e.primary != "" {
		e.reason = "promote"
	}
	if old != e.primary {
		e.generation++
		e.lastSwitch = now
		if e.reason == "" {
			e.reason = "promote"
		}
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (e *Engine) Record(id string, sample Sample, elect bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.nodes[id]
	if s == nil || sample.Epoch != e.epoch {
		return false
	}
	now := e.now()
	if !sample.OK && !sample.Hard && !s.lastFailure.IsZero() && now.Sub(s.lastFailure) < 3*time.Second {
		return false
	}
	e.decay(s, now)
	freshWindow := 90 * time.Second
	if e.powerSave {
		freshWindow = 180 * time.Second
	}
	if sample.OK && now.Sub(s.LastSuccess) > freshWindow {
		s.Streak = 0
		s.Samples = nil
		s.Recent = nil
		s.EWMA = 0
		s.P95 = 0
		s.P50 = 0
		s.Jitter = 0
	}
	s.Recent = append(s.Recent, sample.OK)
	if len(s.Recent) > latencyWindow {
		s.Recent = s.Recent[len(s.Recent)-latencyWindow:]
	}
	s.LastCheck = now
	s.LastPhase = sample.Phase
	s.LastService = sample.Service
	if sample.OK {
		if sample.TLS && sample.Exit {
			s.Quarantined = false
			s.CoolingUntil = time.Time{}
		}
		s.Success++
		s.Count++
		s.LastSuccess = now
		s.LastError = ""
		s.Failures = 0
		if s.Streak == 0 {
			s.FirstSuccess = now
		}
		s.Streak++
		if sample.TLS {
			s.TLSAt = now
		}
		if sample.Exit {
			s.ExitAt = now
		}
		ms := float64(sample.Latency) / float64(time.Millisecond)
		if ms > 0 {
			if s.EWMA == 0 {
				s.EWMA = ms
			} else {
				s.EWMA = .2*ms + .8*s.EWMA
			}
			s.Samples = append(s.Samples, ms)
			if len(s.Samples) > latencyWindow {
				s.Samples = s.Samples[1:]
			}
			sorted := append([]float64(nil), s.Samples...)
			sort.Float64s(sorted)
			s.P95 = sorted[int(math.Ceil(.95*float64(len(sorted))))-1]
			s.P50 = sorted[(len(sorted)-1)/2]
			s.Jitter = math.Max(s.P95-s.P50, 0)
		}
	} else {
		s.Failure++
		s.Count++
		s.Streak = 0
		s.LastError = sample.Error
		s.Failures++
		s.lastFailure = now
		if sample.Hard {
			s.Quarantined = true
		}
		if sample.Hard || s.Failures >= 2 {
			s.Cooldowns++
			cooldown := time.Minute * time.Duration(1<<min(s.Cooldowns-1, 5))
			if cooldown > 30*time.Minute {
				cooldown = 30 * time.Minute
			}
			s.CoolingUntil = now.Add(cooldown)
		}
		if e.primary == id {
			e.reason = sample.Error
		}
	}
	if elect {
		e.choose(now)
	}
	return true
}

func (e *Engine) Reserve(fault bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if now.Sub(e.budgetStart) >= time.Hour {
		e.budget = 0
		e.budgetStart = now
	}
	limit := 840
	if fault {
		limit = 900
	}
	if e.budget >= limit {
		return false
	}
	e.budget++
	return true
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.now().Sub(e.budgetStart) >= time.Hour {
		e.budget = 0
		e.budgetStart = e.now()
	}
	now := e.now()
	result := Snapshot{Epoch: e.epoch, Generation: e.generation, Primary: e.primary, Active: append([]string{}, e.active...), Reason: e.reason, BudgetUsed: e.budget, BudgetLimit: 900, Paused: e.paused, PowerSave: e.powerSave, Nodes: []Stats{}, NetworkKey: e.network}
	for _, id := range e.order {
		s := *e.nodes[id]
		e.decay(&s, now)
		s.Samples = nil
		result.Nodes = append(result.Nodes, s)
	}
	return result
}

func (e *Engine) Attempts() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.choose(e.now())
	if e.paused {
		if e.primary != "" && e.fresh(e.nodes[e.primary], e.now()) {
			return []string{e.primary}
		}
		return nil
	}
	return append([]string{}, e.active...)
}

func (e *Engine) SetPrimary(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.nodes[id]
	if s == nil || !e.fresh(s, e.now()) {
		return false
	}
	e.manual = id
	if e.primary != id {
		e.primary = id
		e.generation++
		e.reason = "manual"
	}
	e.choose(e.now())
	return true
}

func (e *Engine) ClearManual() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.manual = ""
}

func (e *Engine) PromoteDial(id string, epoch uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.nodes[id]
	if epoch != e.epoch || s == nil || !e.fresh(s, e.now()) {
		return false
	}
	if !e.paused && e.primary != id {
		e.primary = id
		e.generation++
		e.reason = "dial_failover"
	}
	e.choose(e.now())
	return true
}

func (e *Engine) Configure(paused, powerSave bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = paused
	e.powerSave = powerSave
	e.choose(e.now())
}

func (e *Engine) Invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.epoch++
	e.generation++
	e.primary = ""
	e.active = nil
	e.reason = "network_changed"
	for _, s := range e.nodes {
		s.Streak = 0
		s.FirstSuccess = time.Time{}
		s.TLSAt = time.Time{}
		s.ExitAt = time.Time{}
		s.LastSuccess = time.Time{}
		s.LastCheck = time.Time{}
		s.LastError = ""
		s.Recent = nil
		s.Samples = nil
		s.P50 = 0
		s.Jitter = 0
		s.Quarantined = false
		s.CoolingUntil = time.Time{}
		s.Failures = 0
	}
}

func (e *Engine) Restore(snapshot Snapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, nodes := range snapshot.Networks {
		if len(e.networks) >= 8 || !validID(key) {
			continue
		}
		e.networks[key] = nodes
		e.networkLRU = append(e.networkLRU, key)
	}
	for _, saved := range snapshot.Nodes {
		if s := e.nodes[saved.ID]; s != nil {
			s.Success = saved.Success
			s.Failure = saved.Failure
			s.Count = saved.Count
			s.EWMA = saved.EWMA
			s.P95 = saved.P95
			if !saved.LastCheck.IsZero() {
				s.decayedAt = saved.LastCheck
				e.decay(s, e.now())
			}
		}
	}
}

func (e *Engine) SwitchNetwork(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if key == e.network {
		return
	}
	if e.network != "startup" {
		saved := []Stats{}
		for _, id := range e.order {
			s := *e.nodes[id]
			s.Samples = nil
			if s.LastCheck.IsZero() {
				s.LastCheck = s.decayedAt
			}
			saved = append(saved, s)
		}
		e.networks[e.network] = saved
		e.networkLRU = removeNetwork(e.networkLRU, e.network)
		e.networkLRU = append(e.networkLRU, e.network)
	}
	for _, id := range e.order {
		candidate := e.nodes[id].Candidate
		e.nodes[id] = &Stats{Candidate: candidate, decayedAt: e.now()}
	}
	for _, saved := range e.networks[key] {
		if s := e.nodes[saved.ID]; s != nil {
			s.Success = saved.Success
			s.Failure = saved.Failure
			s.Count = saved.Count
			s.EWMA = saved.EWMA
			s.P95 = saved.P95
			if !saved.LastCheck.IsZero() {
				s.decayedAt = saved.LastCheck
				e.decay(s, e.now())
			}
		}
	}
	e.network = key
	e.networkLRU = removeNetwork(e.networkLRU, key)
	for {
		data, _ := json.Marshal(e.networks)
		if len(e.networkLRU) <= 7 && len(data) <= ExportLimit-256*1024 {
			break
		}
		if len(e.networkLRU) == 0 {
			break
		}
		old := e.networkLRU[0]
		e.networkLRU = e.networkLRU[1:]
		delete(e.networks, old)
	}
	e.epoch++
	e.generation++
	e.primary = ""
	e.active = nil
	e.reason = "network_changed"
}

func removeNetwork(keys []string, key string) []string {
	result := []string{}
	for _, item := range keys {
		if item != key {
			result = append(result, item)
		}
	}
	return result
}

func (e *Engine) Statistics() Snapshot {
	state := e.Snapshot()
	e.mu.Lock()
	defer e.mu.Unlock()
	state.Networks = map[string][]Stats{}
	for key, nodes := range e.networks {
		state.Networks[key] = append([]Stats(nil), nodes...)
	}
	if validID(e.network) {
		state.Networks[e.network] = state.Nodes
	}
	return state
}
