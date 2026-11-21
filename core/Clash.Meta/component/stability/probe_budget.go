package stability

import (
	"sync"
	"time"
)

// Reserve the complete bounded operation before issuing its first request.
// Only consumed charge units count toward usage; unused credits are returned.
func (e *Engine) LeaseProbe(class ProbeClass, credits int) (func() bool, func(), bool) {
	e.mu.Lock()
	b := e.probeBucketLocked(class)
	if b == nil || credits < 1 || b.tokens < float64(credits) {
		e.mu.Unlock()
		return nil, nil, false
	}
	b.tokens -= float64(credits)
	e.mu.Unlock()
	left := credits
	closed := false
	var once sync.Once
	consume := func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		if closed || left == 0 {
			return false
		}
		left--
		e.probeBucketLocked(class).used++
		return true
	}
	release := func() {
		once.Do(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			closed = true
			b := e.probeBucketLocked(class)
			capacity, _ := probeRate(class)
			b.tokens += float64(left)
			if b.tokens > float64(capacity) {
				b.tokens = float64(capacity)
			}
			left = 0
		})
	}
	return consume, release, true
}

// Exploration may stop for the hour; health and recovery must remain possible.
// Token buckets bound actual outbound requests independently of that quota.
type ProbeClass string

const (
	MaintenanceProbe ProbeClass = "maintenance"
	RecoveryProbe    ProbeClass = "recovery"
	StandbyProbe     ProbeClass = "first_standby"
	SearchProbe      ProbeClass = "availability_search"
)

type ProbeBudgetState struct {
	Tokens        float64 `json:"tokens"`
	Capacity      int     `json:"capacity"`
	RefillEveryMs int64   `json:"refillEveryMs"`
	HourRequests  int     `json:"hourRequests"`
}

type probeBucket struct {
	tokens  float64
	updated time.Time
	hour    time.Time
	used    int
}

func probeRate(class ProbeClass) (int, time.Duration) {
	switch class {
	case MaintenanceProbe:
		return 12, 3 * time.Second
	case RecoveryProbe:
		return 6, 5 * time.Second
	case StandbyProbe:
		return 2, 30 * time.Second
	case SearchProbe:
		// A finite, one-pass outage search. Network changes do not replenish it.
		// Enough for two 185-route passes with two exit services; afterwards
		// the existing recovery bucket keeps slow retries possible.
		return 768, 5 * time.Second
	default:
		return 0, 0
	}
}

func (e *Engine) probeBucketLocked(class ProbeClass) *probeBucket {
	capacity, interval := probeRate(class)
	if capacity == 0 {
		return nil
	}
	if e.probeBuckets == nil {
		e.probeBuckets = map[ProbeClass]*probeBucket{}
	}
	now := e.now()
	b := e.probeBuckets[class]
	if b == nil {
		b = &probeBucket{tokens: float64(capacity), updated: now, hour: now}
		e.probeBuckets[class] = b
	}
	if now.After(b.updated) {
		b.tokens += float64(now.Sub(b.updated)) / float64(interval)
		if b.tokens > float64(capacity) {
			b.tokens = float64(capacity)
		}
		b.updated = now
	}
	if now.Sub(b.hour) >= time.Hour {
		b.hour = now
		b.used = 0
	}
	return b
}

func (e *Engine) ReservePriority(class ProbeClass) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.probeBucketLocked(class)
	if b == nil || b.tokens < 1 {
		return false
	}
	b.tokens--
	b.used++
	return true
}

func (e *Engine) PriorityAvailable(class ProbeClass, requests int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.probeBucketLocked(class)
	return b != nil && requests > 0 && b.tokens >= float64(requests)
}

func (e *Engine) probeBudgetsLocked() map[ProbeClass]ProbeBudgetState {
	result := map[ProbeClass]ProbeBudgetState{}
	for _, class := range []ProbeClass{MaintenanceProbe, RecoveryProbe, SearchProbe, StandbyProbe} {
		b := e.probeBucketLocked(class)
		capacity, interval := probeRate(class)
		result[class] = ProbeBudgetState{Tokens: b.tokens, Capacity: capacity, RefillEveryMs: interval.Milliseconds(), HourRequests: b.used}
	}
	return result
}
