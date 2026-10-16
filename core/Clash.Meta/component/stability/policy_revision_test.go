package stability

import (
	"math"
	"testing"
	"time"
)

func TestPolicyRareModerateSpikeDoesNotEraseTypicalSpeed(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}}, func() time.Time { return now })
	for i := 0; i < 12; i++ {
		ms := 200
		if i == 11 {
			ms = 600
		}
		e.Record("a", Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: time.Duration(ms) * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	steady := Stats{EWMA: 350, P50: 350, P95: 350}
	if LatencyCost(e.Snapshot().Nodes[0]) >= LatencyCost(steady) {
		t.Fatal("one moderate spike erased eleven fast observations")
	}
}

func TestPolicyFreshVerificationRestoresAvailabilityAfterInvalidation(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}}, func() time.Time { return now })
	e.Record("a", Sample{Epoch: e.Snapshot().Epoch, Error: "https_timeout"}, true)
	now = now.Add(4 * time.Second)
	e.Record("a", Sample{Epoch: e.Snapshot().Epoch, Error: "https_timeout"}, true)
	e.Invalidate()
	now = now.Add(time.Second)
	e.Record("a", Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true}, true)
	if len(e.Attempts()) != 1 {
		t.Fatal("past ejections blocked a freshly verified sole route")
	}
	if CompetitionReady(e.Snapshot().Nodes[0]) {
		t.Fatal("availability was labeled measured stability")
	}
}

func TestPolicySmoothingUsesElapsedTimeRatherThanProbeFrequency(t *testing.T) {
	measure := func(interval time.Duration) float64 {
		now := time.Unix(1000, 0)
		e, _ := New([]Candidate{{"a", "r1", "x"}}, func() time.Time { return now })
		e.Record("a", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond}, true)
		for elapsed := interval; elapsed <= time.Minute; elapsed += interval {
			now = now.Add(interval)
			e.Record("a", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 600 * time.Millisecond}, true)
		}
		return e.Snapshot().Nodes[0].EWMA
	}
	if a, b := measure(5*time.Second), measure(15*time.Second); math.Abs(a-b) > 1 {
		t.Fatal("the same minute of network behavior depends on sampling frequency", a, b)
	}
}

func TestPolicyDialTimingCannotGrantEligibilityAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}}, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		e.ObserveDial("a", 1, 100*time.Millisecond)
	}
	node := e.Snapshot().Nodes[0]
	if len(e.Attempts()) != 0 || CompetitionReady(node) || ConnectionTimeout(node, now) != time.Second {
		t.Fatal("dial timing granted route eligibility or did not shorten establishment timeout")
	}
	if ConnectionTimeout(node, now.Add(6*time.Minute)) != 3*time.Second {
		t.Fatal("stale timings controlled a changed network")
	}
	e.Invalidate()
	e.ObserveDial("a", 1, 10*time.Second)
	if e.Snapshot().Nodes[0].DialCount != 0 {
		t.Fatal("old-epoch timing contaminated current network")
	}
}

func TestPolicyCooldownHistoryDecaysOnlyDuringContinuousHealth(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}}, func() time.Time { return now })
	e.nodes["a"].Cooldowns = 3
	for i := 0; i <= 20; i++ {
		e.Record("a", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond}, true)
		now = now.Add(15 * time.Second)
	}
	if e.nodes["a"].Cooldowns != 2 {
		t.Fatal("continuous health did not forgive one past cooldown")
	}
	now = now.Add(10 * time.Minute)
	e.Record("a", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond}, true)
	if e.nodes["a"].Cooldowns != 2 {
		t.Fatal("idle time was treated as proven health")
	}
}

func TestPolicyThroughputCannotOverrideLatencyOrIncompleteEvidence(t *testing.T) {
	now := time.Unix(1000, 0)
	base := Stats{Candidate: Candidate{ID: "a"}, EWMA: 200, P50: 200, P95: 200, QualityStreak: 3, FirstQuality: now.Add(-10 * time.Second), LastSuccess: now, ThroughputMbps: 5, ThroughputReliable: true, ThroughputAt: now, LoadedLatency: 200}
	other := base
	other.ID, other.ThroughputMbps = "b", 20
	if !PreferThroughput(base, other, now) {
		t.Fatal("comparable verified paths ignored material throughput gain")
	}
	other.EWMA, other.P50, other.P95 = 450, 450, 450
	if PreferThroughput(base, other, now) {
		t.Fatal("bandwidth overrode a large latency regression")
	}
	other = base
	other.ID, other.ThroughputMbps, other.ThroughputReliable = "b", 1000, false
	if PreferThroughput(base, other, now) {
		t.Fatal("short burst reference was mistaken for reliable bandwidth")
	}
	other.ThroughputReliable, other.LoadedLatency = true, 900
	if PreferThroughput(base, other, now) {
		t.Fatal("loaded latency regression was ignored")
	}
}
