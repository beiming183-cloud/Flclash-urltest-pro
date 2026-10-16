package stability

import (
	"math"
	"sort"
	"time"
)

func resetQuality(s *Stats) {
	s.Samples = nil
	s.SampleTimes = nil
	s.Recent = nil
	s.OutcomeTimes = nil
	s.EWMA = 0
	s.P50 = 0
	s.P90 = 0
	s.P95 = 0
	s.MAD = 0
	s.Jitter = 0
	s.LatencySamples = 0
	s.QualityStreak = 0
	s.FirstQuality = time.Time{}
	s.LastQuality = time.Time{}
	s.FirstResponse = 0
	s.FirstResponseP95 = 0
	s.FirstResponseP90 = 0
	s.FirstResponseMedian = 0
	s.FirstResponseCount = 0
	s.FirstResponseSamples = nil
	s.FirstResponseTimes = nil
}

func resetTransport(s *Stats) {
	s.DialSamples, s.DialTimes = nil, nil
	s.DialP90, s.DialCount, s.LastDial = 0, 0, time.Time{}
	s.ThroughputMbps, s.LoadedLatency = 0, 0
	s.ThroughputReliable, s.ThroughputBytes, s.ThroughputAt = false, 0, time.Time{}
}

func ConnectionTimeout(s Stats, now time.Time) time.Duration {
	if s.DialCount < 3 || s.DialP90 <= 0 || now.Sub(s.LastDial) > 5*time.Minute {
		return 3 * time.Second
	}
	ms := math.Min(math.Max(3*s.DialP90+350, 1000), 6000)
	return time.Duration(ms * float64(time.Millisecond))
}

func (e *Engine) ObserveDial(id string, epoch uint64, elapsed time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.nodes[id]
	if s == nil || epoch != e.epoch || elapsed <= 0 {
		return
	}
	now := e.now()
	s.DialSamples = append(s.DialSamples, float64(elapsed)/float64(time.Millisecond))
	s.DialTimes = append(s.DialTimes, now)
	for len(s.DialSamples) > latencyWindow || len(s.DialTimes) > 1 && now.Sub(s.DialTimes[0]) > 5*time.Minute {
		s.DialSamples, s.DialTimes = s.DialSamples[1:], s.DialTimes[1:]
	}
	sorted := append([]float64(nil), s.DialSamples...)
	sort.Float64s(sorted)
	s.DialP90 = sorted[int(math.Ceil(.9*float64(len(sorted))))-1]
	s.DialCount, s.LastDial = len(sorted), now
}

func (e *Engine) ObserveThroughput(id string, epoch uint64, mbps float64, bytes int64, reliable bool, loaded time.Duration) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.nodes[id]
	if s == nil || epoch != e.epoch || !e.fresh(s, e.now()) || mbps <= 0 || math.IsNaN(mbps) || math.IsInf(mbps, 0) || bytes <= 0 || bytes > 1<<20 {
		return false
	}
	s.ThroughputMbps, s.ThroughputBytes, s.ThroughputReliable = mbps, bytes, reliable
	s.ThroughputAt, s.LoadedLatency = e.now(), float64(loaded)/float64(time.Millisecond)
	e.choose(e.now())
	return true
}

func PreferThroughput(current, candidate Stats, now time.Time) bool {
	if candidate.ID == current.ID || !CompetitionReady(candidate) || !CompetitionReady(current) || !current.ThroughputReliable || !candidate.ThroughputReliable {
		return false
	}
	if now.Sub(current.ThroughputAt) > 15*time.Minute || now.Sub(candidate.ThroughputAt) > 15*time.Minute || current.ThroughputAt.IsZero() || candidate.ThroughputAt.IsZero() {
		return false
	}
	return LatencyCost(candidate) <= LatencyCost(current)*1.1 &&
		candidate.ThroughputMbps >= current.ThroughputMbps*1.5 &&
		current.LoadedLatency > 0 && candidate.LoadedLatency > 0 &&
		candidate.LoadedLatency <= current.LoadedLatency*1.1+25
}

func recordOutcome(s *Stats, ok bool, now time.Time) {
	if len(s.Recent) != len(s.OutcomeTimes) {
		s.Recent = nil
		s.OutcomeTimes = nil
	}
	s.Recent = append(s.Recent, ok)
	s.OutcomeTimes = append(s.OutcomeTimes, now)
	for len(s.Recent) > latencyWindow || len(s.OutcomeTimes) > 1 && now.Sub(s.OutcomeTimes[0]) > 5*time.Minute {
		s.Recent = s.Recent[1:]
		s.OutcomeTimes = s.OutcomeTimes[1:]
	}
}

func decayCooldown(s *Stats, now time.Time) {
	if s.Cooldowns == 0 {
		return
	}
	if s.healthySince.IsZero() {
		s.healthySince = now
		s.cooldownDecayedAt = now
		return
	}
	steps := int(now.Sub(s.cooldownDecayedAt) / (5 * time.Minute))
	if steps > 0 {
		s.Cooldowns -= steps
		if s.Cooldowns < 0 {
			s.Cooldowns = 0
		}
		s.cooldownDecayedAt = now
	}
}
