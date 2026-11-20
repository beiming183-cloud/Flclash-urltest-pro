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
const latencyWindow = 64

type Candidate struct {
	ID      string `json:"id"`
	Relay   string `json:"relayId"`
	Airport string `json:"airportId"`
}

type Sample struct {
	ProbeDial, ProbeTLS, ExitCheck time.Duration
	Epoch                          uint64
	OK                             bool
	TLS                            bool
	Exit                           bool
	Hard                           bool
	Latency                        time.Duration
	FirstResponse                  time.Duration
	Error                          string
	Phase                          string
	Service                        string
	TLSInfo                        []TLSInfo
	FailureWait                    time.Duration
}

type TLSInfo struct {
	Service           string    `json:"serviceTag"`
	Certificate       string    `json:"certSha256"`
	SPKI              string    `json:"spkiSha256"`
	SANCount          int       `json:"sanCount"`
	NotBefore         time.Time `json:"notBefore"`
	NotAfter          time.Time `json:"notAfter"`
	Version           uint16    `json:"version"`
	Verified          bool      `json:"verified"`
	HandshakeComplete bool      `json:"handshakeComplete"`
	CheckedAt         time.Time `json:"checkedAt"`
	Epoch             uint64    `json:"networkEpoch"`
}

type Stats struct {
	Health string `json:"healthState,omitempty"`
	Candidate
	Success              float64     `json:"success"`
	Failure              float64     `json:"failure"`
	Samples              []float64   `json:"-"`
	EWMA                 float64     `json:"ewmaMs"`
	P95                  float64     `json:"p95Ms"`
	P50                  float64     `json:"p50Ms"`
	P90                  float64     `json:"p90Ms"`
	MAD                  float64     `json:"madMs"`
	LatencySamples       int         `json:"latencySampleCount"`
	QualityStreak        int         `json:"qualityStreak"`
	QualityReady         bool        `json:"qualityReady"`
	FirstQuality         time.Time   `json:"-"`
	LastQuality          time.Time   `json:"-"`
	SampleTimes          []time.Time `json:"-"`
	OutcomeTimes         []time.Time `json:"-"`
	FailureWaitMs        float64     `json:"failureWaitMs"`
	Jitter               float64     `json:"jitterMs"`
	Recent               []bool      `json:"recentResults,omitempty"`
	FirstResponse        float64     `json:"firstResponseMs"`
	FirstResponseP95     float64     `json:"firstResponseP95Ms"`
	FirstResponseMedian  float64     `json:"firstResponseMedianMs"`
	FirstResponseP90     float64     `json:"firstResponseP90Ms"`
	FirstResponseCount   int         `json:"firstResponseSampleCount"`
	FirstResponseSamples []float64   `json:"-"`
	FirstResponseTimes   []time.Time `json:"-"`
	DialSamples          []float64   `json:"-"`
	DialTimes            []time.Time `json:"-"`
	DialP90              float64     `json:"dialP90Ms"`
	DialCount            int         `json:"dialSampleCount"`
	LastDial             time.Time   `json:"lastDial"`
	ThroughputMbps       float64     `json:"throughputMbps"`
	ThroughputReliable   bool        `json:"throughputReliable"`
	ThroughputAt         time.Time   `json:"throughputAt"`
	ThroughputBytes      int64       `json:"throughputBytes"`
	LoadedLatency        float64     `json:"loadedLatencyMs"`
	Lower                float64     `json:"reliabilityLower"`
	Count                int         `json:"sampleCount"`
	LastSuccess          time.Time   `json:"lastSuccess"`
	LastCheck            time.Time   `json:"lastCheck"`
	FirstSuccess         time.Time   `json:"-"`
	TLSAt                time.Time   `json:"tlsAt"`
	ExitAt               time.Time   `json:"exitAt"`
	CoolingUntil         time.Time   `json:"coolingUntil"`
	Streak               int         `json:"successStreak"`
	LastPhase            string      `json:"phase,omitempty"`
	LastService          string      `json:"serviceTag,omitempty"`
	Failures             int         `json:"-"`
	Cooldowns            int         `json:"-"`
	LastError            string      `json:"error"`
	SuspectUntil         time.Time   `json:"suspectUntil,omitempty"`
	Quarantined          bool        `json:"quarantined"`
	decayedAt            time.Time
	lastFailure          time.Time
	healthySince         time.Time
	cooldownDecayedAt    time.Time
}

type Snapshot struct {
	Warm                []string                        `json:"warmStandbys,omitempty"`
	StartupHints        []StartupHint                   `json:"startupHints,omitempty"`
	Epoch               uint64                          `json:"networkEpoch"`
	Generation          uint64                          `json:"generation"`
	Primary             string                          `json:"primary"`
	Active              []string                        `json:"active"`
	Nodes               []Stats                         `json:"nodes"`
	Reason              string                          `json:"lastSwitchReason"`
	BudgetUsed          int                             `json:"budgetUsed"`
	BudgetLimit         int                             `json:"budgetLimit"`
	BudgetResetIn       int                             `json:"budgetResetInSec"`
	ProbeBudgets        map[ProbeClass]ProbeBudgetState `json:"priorityProbeBudgets,omitempty"`
	Paused              bool                            `json:"paused"`
	PowerSave           bool                            `json:"powerSave"`
	Running             bool                            `json:"running"`
	NetworkKey          string                          `json:"networkKey,omitempty"`
	Networks            map[string][]Stats              `json:"networks,omitempty"`
	NetworkStartupHints map[string][]StartupHint        `json:"networkStartupHints,omitempty"`
	Selections          []SelectionEvent                `json:"selections,omitempty"`
	Scheduler           map[string]any                  `json:"scheduler,omitempty"`
}

type SelectionEvent struct {
	Time            time.Time `json:"time"`
	Kind            string    `json:"kind"`
	Node            string    `json:"node"`
	Previous        string    `json:"previous,omitempty"`
	Reason          string    `json:"reason"`
	Latency         float64   `json:"requestLatencyMs"`
	Jitter          float64   `json:"jitterMs"`
	FirstResponse   float64   `json:"firstResponseMs,omitempty"`
	PreviousLatency float64   `json:"previousLatencyMs,omitempty"`
	Cost            float64   `json:"qualityCostMs,omitempty"`
	PreviousCost    float64   `json:"previousQualityCostMs,omitempty"`
	Samples         int       `json:"latencySampleCount"`
	Qualified       bool      `json:"qualityReady"`
	HealthAgeMs     int64     `json:"healthAgeMs"`
}

type Engine struct {
	challenger        string
	challengerSince   time.Time
	startup           map[string]StartupHint
	mu                sync.Mutex
	now               func() time.Time
	nodes             map[string]*Stats
	order             []string
	epoch             uint64
	generation        uint64
	primary           string
	active            []string
	reason            string
	paused            bool
	powerSave         bool
	budgetStart       time.Time
	budget            int
	probeBuckets      map[ProbeClass]*probeBucket
	network           string
	networks          map[string][]Stats
	networkLRU        []string
	networkHints      map[string]map[string]StartupHint
	lastSwitch        time.Time
	lastPoolChange    time.Time
	manual            string
	startupPrimary    bool
	selections        []SelectionEvent
	selectionObserver func(SelectionEvent, uint64, uint64)
}

func New(candidates []Candidate, now func() time.Time) (*Engine, error) {
	if len(candidates) == 0 || len(candidates) > MaxCandidates {
		return nil, errors.New("invalid candidate count")
	}
	if now == nil {
		now = time.Now
	}
	e := &Engine{now: now, nodes: make(map[string]*Stats), startup: map[string]StartupHint{}, epoch: 1, generation: 1, budgetStart: now(), network: "startup", networks: map[string][]Stats{}}
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
	return !s.Quarantined && !now.Before(s.CoolingUntil) && s.Streak >= 1 &&
		(s.LastError == "" || now.Before(s.SuspectUntil)) &&
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
	return s.QualityStreak >= 3 && !s.FirstQuality.IsZero() && s.LastSuccess.Sub(s.FirstQuality) >= 10*time.Second
}

func PrimaryCompetitionReady(s Stats) bool {
	return s.QualityStreak >= 5 && s.LatencySamples >= 5 && !s.FirstQuality.IsZero() && s.LastQuality.Sub(s.FirstQuality) >= 15*time.Second
}

func LatencyCost(s Stats) float64 {
	if s.EWMA <= 0 || s.P95 <= 0 {
		return math.Inf(1)
	}
	p50 := s.P50
	if p50 <= 0 {
		p50 = s.EWMA
	}
	p90 := s.P90
	if p90 <= 0 {
		p90 = s.P95
	}
	cost := .55*p50 + .25*s.EWMA + .2*p90 + .25*s.MAD
	if s.DialCount >= 3 && s.DialP90 > 0 && !s.LastDial.IsZero() && s.LastSuccess.Sub(s.LastDial) <= 5*time.Minute {
		cost += .25 * s.DialP90
	}
	if s.FirstResponseCount > 0 {
		first := s.FirstResponseMedian
		if first <= 0 {
			first = s.FirstResponseP95
		}
		cost += .15 * first
	}
	if len(s.Recent) > 0 {
		failures := 0
		for _, ok := range s.Recent {
			if !ok {
				failures++
			}
		}
		cost += float64(failures) / float64(len(s.Recent)) * math.Max(s.FailureWaitMs, 3000)
	}
	return cost
}

func (e *Engine) choose(now time.Time) {
	previousActive := append([]string(nil), e.active...)
	pool := []*Stats{}
	for _, id := range e.order {
		s := e.nodes[id]
		e.decay(s, now)
		if e.fresh(s, now) {
			pool = append(pool, s)
		}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].LastError != pool[j].LastError && (pool[i].LastError == "" || pool[j].LastError == "") {
			return pool[i].LastError == ""
		}
		if a, b := ReliabilityTier(*pool[i]), ReliabilityTier(*pool[j]); a != b {
			return a < b
		}
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
		e.startupPrimary = pool[0].EWMA <= 0
	}
	if e.manual != "" && e.fresh(e.nodes[e.manual], now) {
		e.primary = e.manual
	}
	if e.primary != "" && e.manual == "" && !e.paused && len(pool) > 0 && (e.startupPrimary || now.Sub(e.lastSwitch) >= time.Minute) {
		current, better := e.nodes[e.primary], pool[0]
		currentCost, betterCost := LatencyCost(*current), LatencyCost(*better)
		if better.ID != current.ID && PrimaryCompetitionReady(*better) &&
			better.LastError == "" && ReliabilityTier(*better) <= ReliabilityTier(*current) &&
			betterCost <= currentCost*.8 && betterCost+100 <= currentCost {
			if e.challenger != better.ID {
				e.challenger, e.challengerSince = better.ID, now
			}
			if e.startupPrimary || !CompetitionReady(*current) || now.Sub(e.challengerSince) >= 15*time.Second {
				e.primary = better.ID
				e.reason = "latency_quality_improved"
				e.startupPrimary = false
			}
		} else {
			e.challenger = ""
			e.challengerSince = time.Time{}
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
	for _, id := range previousActive {
		if len(e.active) == 3 {
			break
		}
		if !contains(e.active, id) && e.fresh(e.nodes[id], now) {
			e.active = append(e.active, id)
			s := e.nodes[id]
			relays[s.Relay], airports[s.Airport] = true, true
		}
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
	replaced := map[string]string{}
	// A healthy pool can otherwise remain trapped in one airport forever:
	// retaining incumbents is useful, but not at the expense of failure domains.
	if len(e.active) == 3 && !e.paused && now.Sub(e.lastPoolChange) >= 30*time.Second {
		for _, challenger := range pool {
			if contains(e.active, challenger.ID) || !CompetitionReady(*challenger) {
				continue
			}
			airportUsed := false
			for _, id := range e.active {
				airportUsed = airportUsed || e.nodes[id].Airport == challenger.Airport
			}
			if airportUsed {
				continue
			}
			worst := -1
			for i := 1; i < len(e.active); i++ {
				incumbent := e.nodes[e.active[i]]
				duplicate := false
				for j, id := range e.active {
					if i != j && e.nodes[id].Airport == incumbent.Airport {
						duplicate = true
					}
				}
				if duplicate && LatencyCost(*challenger) <= LatencyCost(*incumbent)*1.25+25 &&
					(worst < 0 || LatencyCost(*incumbent) > LatencyCost(*e.nodes[e.active[worst]])) {
					worst = i
				}
			}
			if worst >= 0 {
				replaced[challenger.ID] = e.active[worst]
				e.active[worst] = challenger.ID
				break
			}
		}
	}
	if len(e.active) == 3 && len(replaced) == 0 && !e.paused {
		for _, challenger := range pool {
			if contains(e.active, challenger.ID) || !CompetitionReady(*challenger) {
				continue
			}
			for index := 1; index < len(e.active); index++ {
				incumbent := e.nodes[e.active[index]]
				duplicate, unused := false, true
				for i, id := range e.active {
					if i == index {
						continue
					}
					duplicate = duplicate || e.nodes[id].Relay == incumbent.Relay
					unused = unused && e.nodes[id].Relay != challenger.Relay
				}
				if duplicate && unused && LatencyCost(*challenger) <= LatencyCost(*incumbent)*1.25+25 {
					replaced[challenger.ID] = incumbent.ID
					e.active[index] = challenger.ID
					break
				}
			}
			if len(replaced) > 0 {
				break
			}
		}
	}
	if len(e.active) == 3 && len(replaced) == 0 && !e.paused && now.Sub(e.lastPoolChange) >= 30*time.Second {
		for _, challenger := range pool {
			if contains(e.active, challenger.ID) || !CompetitionReady(*challenger) {
				continue
			}
			worst := 1
			if LatencyCost(*e.nodes[e.active[2]]) > LatencyCost(*e.nodes[e.active[1]]) {
				worst = 2
			}
			incumbent := e.nodes[e.active[worst]]
			cost, oldCost := LatencyCost(*challenger), LatencyCost(*incumbent)
			// Do not immediately undo a useful independent-airport standby just
			// because a correlated challenger is a little faster.
			uniqueAirport, challengerAirportUsed := true, false
			for i, id := range e.active {
				if i != worst && e.nodes[id].Airport == incumbent.Airport {
					uniqueAirport = false
				}
				if i != worst && e.nodes[id].Airport == challenger.Airport {
					challengerAirportUsed = true
				}
			}
			if uniqueAirport && challengerAirportUsed && oldCost <= LatencyCost(*pool[0])*1.25+25 {
				continue
			}
			if cost < oldCost*.88 && cost+25 < oldCost {
				replaced[challenger.ID] = incumbent.ID
				e.active[worst] = challenger.ID
				break
			}
		}
	}
	changed := len(e.active) != len(previousActive)
	for _, id := range e.active {
		if !contains(previousActive, id) {
			changed = true
		}
	}
	if changed {
		e.lastPoolChange = now
	}
	if old == "" && e.primary != "" {
		e.reason = "promote"
		e.recordSelection("empty_exit", e.primary, "", "route_recovered")
	}
	if old != e.primary {
		e.generation++
		e.lastSwitch = now
		if e.reason == "" {
			e.reason = "promote"
		}
		if e.primary != "" {
			e.recordSelection("primary", e.primary, old, e.reason)
		} else if old != "" {
			e.recordSelection("empty_enter", old, "", "no_eligible_route")
		}
	}
	for _, id := range e.active {
		if id != e.primary && !contains(previousActive, id) {
			e.recordSelection("backup", id, replaced[id], "pool_selected")
		}
	}
	for _, id := range previousActive {
		if !contains(e.active, id) && (id != old || e.primary == "") {
			reason := "pool_replaced"
			if e.nodes[id].LastError != "" {
				reason = e.nodes[id].LastError
			} else if !e.fresh(e.nodes[id], now) {
				reason = "health_check_expired"
			}
			e.recordSelection("removed", id, "", reason)
		}
	}
}

func (e *Engine) recordSelection(kind, id, previous, reason string) {
	node := e.nodes[id]
	if node == nil {
		return
	}
	event := SelectionEvent{Time: e.now(), Kind: kind, Node: id, Previous: previous, Reason: reason, Latency: node.EWMA, Jitter: node.Jitter, FirstResponse: node.FirstResponse}
	if kind == "empty_enter" || kind == "empty_exit" {
		if e.selectionObserver != nil {
			e.selectionObserver(event, e.epoch, e.generation)
		}
		return
	}
	event.Samples, event.Qualified = node.LatencySamples, CompetitionReady(*node)
	if !node.LastSuccess.IsZero() {
		event.HealthAgeMs = max(0, e.now().Sub(node.LastSuccess).Milliseconds())
	}
	if cost := LatencyCost(*node); !math.IsInf(cost, 0) && !math.IsNaN(cost) {
		event.Cost = cost
	}
	if previousNode := e.nodes[previous]; previousNode != nil {
		event.PreviousLatency = previousNode.EWMA
		if cost := LatencyCost(*previousNode); !math.IsInf(cost, 0) && !math.IsNaN(cost) {
			event.PreviousCost = cost
		}
	}
	e.selections = append(e.selections, event)
	if e.selectionObserver != nil {
		e.selectionObserver(event, e.epoch, e.generation)
	}
	if len(e.selections) > 128 {
		e.selections = e.selections[len(e.selections)-128:]
	}
}

// The observer runs under the engine lock and must not call back into Engine.
func (e *Engine) SetSelectionObserver(fn func(SelectionEvent, uint64, uint64)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.selectionObserver = fn
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
		resetQuality(s)
		s.healthySince = time.Time{}
	}
	if !sample.OK && (sample.Error == "cancelled" || sample.Error == "budget_exhausted") {
		return false
	}
	previousStreak := s.Streak
	recordOutcome(s, sample.OK, now)
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
		s.SuspectUntil = time.Time{}
		s.Failures = 0
		decayCooldown(s, now)
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
		if sample.FirstResponse > 0 {
			s.FirstResponse = float64(sample.FirstResponse) / float64(time.Millisecond)
			s.FirstResponseSamples = append(s.FirstResponseSamples, s.FirstResponse)
			s.FirstResponseTimes = append(s.FirstResponseTimes, now)
			for len(s.FirstResponseSamples) > latencyWindow || len(s.FirstResponseTimes) > 1 && now.Sub(s.FirstResponseTimes[0]) > 5*time.Minute {
				s.FirstResponseSamples = s.FirstResponseSamples[1:]
				s.FirstResponseTimes = s.FirstResponseTimes[1:]
			}
			s.FirstResponseCount = len(s.FirstResponseSamples)
			firstResponses := append([]float64(nil), s.FirstResponseSamples...)
			sort.Float64s(firstResponses)
			s.FirstResponseP95 = firstResponses[int(math.Ceil(.95*float64(len(firstResponses))))-1]
			s.FirstResponseMedian = firstResponses[(len(firstResponses)-1)/2]
			s.FirstResponseP90 = firstResponses[int(math.Ceil(.9*float64(len(firstResponses))))-1]
		}
		if ms > 0 {
			if s.QualityStreak == 0 {
				s.FirstQuality = now
			}
			s.QualityStreak++
			if s.EWMA == 0 {
				s.EWMA = ms
			} else {
				alpha := 1 - math.Exp(-math.Ln2*math.Max(now.Sub(s.LastQuality).Seconds(), .001)/30)
				s.EWMA = alpha*ms + (1-alpha)*s.EWMA
			}
			s.LastQuality = now
			s.Samples = append(s.Samples, ms)
			s.SampleTimes = append(s.SampleTimes, now)
			for len(s.Samples) > latencyWindow || len(s.SampleTimes) > 1 && now.Sub(s.SampleTimes[0]) > 5*time.Minute {
				s.Samples = s.Samples[1:]
				s.SampleTimes = s.SampleTimes[1:]
			}
			s.LatencySamples = len(s.Samples)
			sorted := append([]float64(nil), s.Samples...)
			sort.Float64s(sorted)
			s.P95 = sorted[int(math.Ceil(.95*float64(len(sorted))))-1]
			s.P50 = sorted[(len(sorted)-1)/2]
			s.P90 = sorted[int(math.Ceil(.9*float64(len(sorted))))-1]
			deviations := make([]float64, len(sorted))
			for i, value := range sorted {
				deviations[i] = math.Abs(value - s.P50)
			}
			sort.Float64s(deviations)
			s.MAD = deviations[(len(deviations)-1)/2]
			s.Jitter = math.Max(s.P95-s.P50, 0)
		}
	} else {
		s.Failure++
		s.Count++
		s.Streak = 0
		s.QualityStreak = 0
		s.FirstQuality = time.Time{}
		s.healthySince = time.Time{}
		s.cooldownDecayedAt = time.Time{}
		if sample.FailureWait > 0 {
			wait := math.Min(float64(sample.FailureWait)/float64(time.Millisecond), 15000)
			if s.FailureWaitMs == 0 {
				s.FailureWaitMs = wait
			} else {
				s.FailureWaitMs = .25*wait + .75*s.FailureWaitMs
			}
		}
		s.LastError = sample.Error
		s.Failures++
		s.lastFailure = now
		// Only an explicit background availability probe gets bounded grace.
		// CONNECT errors and security failures never gain health from this path.
		s.SuspectUntil = time.Time{}
		if !sample.Hard && (sample.Phase == "delay" || sample.Phase == "exit") &&
			s.Failures == 1 && previousStreak > 0 && now.Sub(s.LastSuccess) < 90*time.Second &&
			!s.TLSAt.IsZero() && !s.ExitAt.IsZero() {
			s.Streak = previousStreak
			s.SuspectUntil = now.Add(30 * time.Second)
		}
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
			if sample.Hard || s.Failures == 2 {
				e.recordSelection("rejected", id, "", sample.Error)
			}
		}
		if e.primary == id {
			e.reason = sample.Error
		}
	}
	e.rememberStartup(s)
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
	result.Active = make([]string, 0, 3)
	for _, id := range e.active {
		if e.fresh(e.nodes[id], now) {
			result.Active = append(result.Active, id)
		}
	}
	if result.Primary != "" && !e.fresh(e.nodes[result.Primary], now) {
		result.Primary = ""
	}
	result.Selections = append([]SelectionEvent(nil), e.selections...)
	result.ProbeBudgets = e.probeBudgetsLocked()
	result.Warm = e.warmLocked(now)
	result.BudgetResetIn = int(math.Ceil(time.Hour.Seconds() - now.Sub(e.budgetStart).Seconds()))
	for _, id := range e.order {
		s := *e.nodes[id]
		if hint, ok := e.startup[id]; ok {
			result.StartupHints = append(result.StartupHints, hint)
		}
		e.decay(&s, now)
		s.QualityReady = CompetitionReady(s)
		s.Health = HealthState(s, now)
		if e.fresh(&s, now) && s.LastError == "" {
			s.Health = "healthy"
		}
		s.Samples = nil
		s.FirstResponseSamples = nil
		s.SampleTimes, s.OutcomeTimes, s.FirstResponseTimes = nil, nil, nil
		s.DialSamples, s.DialTimes = nil, nil
		s.Recent = append([]bool(nil), s.Recent...)
		result.Nodes = append(result.Nodes, s)
	}
	return result
}

func (e *Engine) FreshActiveCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	n := 0
	for _, id := range e.active {
		if e.fresh(e.nodes[id], now) {
			n++
		}
	}
	return n
}

func (e *Engine) FreshPrimary() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if node := e.nodes[e.primary]; node != nil && e.fresh(node, e.now()) {
		return e.primary
	}
	return ""
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
		previous := e.primary
		e.primary = id
		e.generation++
		e.reason = "manual"
		e.recordSelection("primary", id, previous, "manual")
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
		previous := e.primary
		e.primary = id
		e.generation++
		e.reason = "dial_failover"
		e.lastSwitch = e.now()
		e.recordSelection("primary", id, previous, "dial_failover")
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
		e.rememberStartup(s)
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
		s.EWMA = 0
		s.P95 = 0
		s.FirstResponse = 0
		s.FirstResponseP95 = 0
		s.FirstResponseCount = 0
		s.FirstResponseSamples = nil
		s.Quarantined = false
		resetQuality(s)
		resetTransport(s)
		s.healthySince = time.Time{}
		s.CoolingUntil = time.Time{}
		s.Failures = 0
	}
}

func (e *Engine) Restore(snapshot Snapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.restoreNetworkHints(snapshot.NetworkStartupHints)
	for _, hint := range snapshot.StartupHints {
		if e.nodes[hint.Node] != nil && hint.Score > 0 && !math.IsInf(hint.Score, 0) && !math.IsNaN(hint.Score) && reasons[hint.Error] {
			e.startup[hint.Node] = hint
		}
	}
	for _, event := range snapshot.Selections {
		if validSelection(event) && e.nodes[event.Node] != nil && (event.Previous == "" || e.nodes[event.Previous] != nil) {
			e.selections = append(e.selections, event)
		}
	}
	if len(e.selections) > 128 {
		e.selections = e.selections[len(e.selections)-128:]
	}
	for key, nodes := range snapshot.Networks {
		if len(e.networks) >= 8 || !validID(key) {
			continue
		}
		e.networks[key] = nodes
		e.networkLRU = append(e.networkLRU, key)
	}
	for _, saved := range snapshot.Nodes {
		if e.nodes[saved.ID] != nil {
			e.rememberStartup(&saved)
		}
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
			s.FirstResponseSamples = nil
			if s.LastCheck.IsZero() {
				s.LastCheck = s.decayedAt
			}
			saved = append(saved, s)
		}
		e.networks[e.network] = saved
		e.networkLRU = removeNetwork(e.networkLRU, e.network)
		e.networkLRU = append(e.networkLRU, e.network)
	}
	// Stop has invalidated live qualifications, but per-network probe ordering
	// remains separate. A hotspot success must not overwrite a Flymodem hint.
	e.selectNetworkHints(key)
	e.network = key
	for _, id := range e.order {
		candidate := e.nodes[id].Candidate
		e.nodes[id] = &Stats{Candidate: candidate, decayedAt: e.now()}
	}
	for _, saved := range e.networks[key] {
		if e.nodes[saved.ID] != nil {
			e.rememberStartup(&saved)
		}
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
	e.networkLRU = removeNetwork(e.networkLRU, key)
	for {
		data, _ := json.Marshal([]any{e.networks, e.networkHints})
		if len(e.networkLRU) <= 7 && len(data) <= ExportLimit/4 {
			break
		}
		if len(e.networkLRU) == 0 {
			break
		}
		old := e.networkLRU[0]
		e.networkLRU = e.networkLRU[1:]
		delete(e.networks, old)
		delete(e.networkHints, old)
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
	state.NetworkStartupHints = map[string][]StartupHint{}
	for key, hints := range e.networkHints {
		for _, hint := range hints {
			state.NetworkStartupHints[key] = append(state.NetworkStartupHints[key], hint)
		}
	}
	for key, nodes := range e.networks {
		state.Networks[key] = append([]Stats(nil), nodes...)
	}
	if validID(e.network) {
		state.Networks[e.network] = state.Nodes
	}
	return state
}
