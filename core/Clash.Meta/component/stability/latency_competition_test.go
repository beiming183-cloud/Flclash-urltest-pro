package stability

import (
	"testing"
	"time"
)

func TestStableLowLatencyChallengerBeatsFrequentlyMeasuredSlowPrimary(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"slow", "r1", "a1"}, {"fast", "r2", "a2"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 34; i++ {
		e.Record("slow", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Duration(420+(i%3)*40) * time.Millisecond}, true)
		now = now.Add(2 * time.Second)
	}
	for _, ms := range []int{215, 205, 210, 208} {
		e.Record("fast", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Duration(ms) * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	if got := e.Snapshot().Primary; got != "fast" {
		t.Fatalf("Stable 200ms challenger lost to 400-500ms incumbent purely due to sampling advantage: %s", got)
	}
}

func TestStableLowJitterBeatsOccasionallyFastSpikes(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"spiky", "r1", "a1"}, {"steady", "r2", "a2"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 34; i++ {
		ms := 180
		if i%4 == 0 {
			ms = 850
		}
		e.Record("spiky", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Duration(ms) * time.Millisecond}, true)
		now = now.Add(2 * time.Second)
	}
	for _, ms := range []int{250, 255, 248, 252} {
		e.Record("steady", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Duration(ms) * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	if got := e.Snapshot().Primary; got != "steady" {
		t.Fatalf("Consistent 250ms challenger lost to frequently spiking 180ms node: %s", got)
	}
}

func TestSingleFastObservationAndSmallImprovementKeepHealthyPrimary(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"current", "r1", "a1"}, {"one", "r2", "a2"}, {"similar", "r3", "a3"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 34; i++ {
		e.Record("current", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 250 * time.Millisecond}, true)
		now = now.Add(2 * time.Second)
	}
	e.Record("one", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 20 * time.Millisecond}, true)
	for i := 0; i < 4; i++ {
		e.Record("similar", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 235 * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	if got := e.Snapshot().Primary; got != "current" {
		t.Fatalf("Insufficient evidence or negligible gain changed healthy primary: %s", got)
	}
}

func TestAirportDiversityDoesNotForceVerySlowBackup(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"primary", "r1", "a1"}, {"fast1", "r2", "a1"}, {"fast2", "r3", "a1"}, {"slow", "r4", "a2"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 3; i++ {
		for _, id := range []string{"primary", "fast1", "fast2", "slow"} {
			ms := 210
			if id == "primary" {
				ms = 200
			}
			if id == "slow" {
				ms = 800
			}
			e.Record(id, Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Duration(ms) * time.Millisecond}, true)
		}
		now = now.Add(5 * time.Second)
	}
	if contains(e.Snapshot().Active, "slow") {
		t.Fatal("800ms alternative airport displaced verified 210ms backups")
	}
}

func TestRecentFailuresStillPenalizeLatencyCost(t *testing.T) {
	good := Stats{P50: 220, P95: 230, EWMA: 220, Recent: []bool{true, true, true, true}}
	bad := good
	bad.Recent = []bool{false, true, false, true}
	if LatencyCost(bad) <= LatencyCost(good)*2 {
		t.Fatal("Recent instability was ignored by latency-quality ranking")
	}
}

func TestRequestLatencyAndFirstResponseRemainSeparate(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "a1"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 3; i++ {
		e.Record("a", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond, FirstResponse: 3500 * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	node := e.Snapshot().Nodes[0]
	if node.EWMA != 200 || node.P95 != 200 || node.FirstResponse != 3500 || node.FirstResponseP95 != 3500 || node.FirstResponseCount != 3 {
		t.Fatalf("First-response time polluted request statistics: %+v", node)
	}
	good := Stats{P50: 300, P95: 300, EWMA: 300, FirstResponseCount: 3, FirstResponseP95: 1000}
	if LatencyCost(node) <= LatencyCost(good) {
		t.Fatal("Repeated slow establishment did not penalize the otherwise fast node")
	}
	now = now.Add(2 * time.Minute)
	e.Record("a", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 210 * time.Millisecond, FirstResponse: 900 * time.Millisecond}, true)
	node = e.Snapshot().Nodes[0]
	if node.EWMA != 210 || node.FirstResponseP95 != 900 || node.FirstResponseCount != 1 {
		t.Fatal("Expired establishment statistics contaminated the new competition")
	}
}

func TestNormalEstablishmentDoesNotDisplaceFastStableNode(t *testing.T) {
	fast := Stats{P50: 200, P95: 220, EWMA: 205, FirstResponseCount: 3, FirstResponseP95: 1900}
	slow := Stats{P50: 450, P95: 500, EWMA: 460, FirstResponseCount: 3, FirstResponseP95: 600}
	if LatencyCost(fast) >= LatencyCost(slow) {
		t.Fatal("Normal first-response cost outweighed low, stable request latency")
	}
}

func TestIdleChallengerMustRebuildRecentSuccessStreak(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"current", "r1", "a1"}, {"idle", "r2", "a2"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 3; i++ {
		e.Record("current", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 450 * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	now = now.Add(20 * time.Second)
	idle := e.nodes["idle"]
	idle.Streak = 20
	idle.LastSuccess = now.Add(-3 * time.Minute)
	idle.FirstSuccess = now.Add(-5 * time.Minute)
	idle.TLSAt, idle.ExitAt = idle.LastSuccess, idle.LastSuccess
	e.Record("idle", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond}, true)
	if idle.Streak != 1 || e.Snapshot().Primary != "current" {
		t.Fatal("Old idle samples were treated as current stable evidence")
	}
}
