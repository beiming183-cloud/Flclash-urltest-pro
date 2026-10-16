package stability

import (
	"math"
	"testing"
	"time"
)

func TestStandbySlowNewAirportCannotEvictFreshFasterBackup(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"primary", "r1", "night"}, {"fast", "r2", "night"}, {"other", "r3", "night"}, {"new", "r4", "other"}}, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		e.Record("primary", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 400 * time.Millisecond, FirstResponse: 3500 * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	e.Record("fast", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 336 * time.Millisecond, FirstResponse: 1822 * time.Millisecond}, true)
	e.Record("other", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 348 * time.Millisecond, FirstResponse: 2388 * time.Millisecond}, true)
	e.Record("new", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 855 * time.Millisecond, FirstResponse: 9162 * time.Millisecond}, true)
	if got := e.Snapshot().Active; contains(got, "new") || !contains(got, "fast") || !contains(got, "other") {
		t.Fatalf("single slow observation displaced a fresh faster standby: %v", got)
	}
}

func TestStandbySimilarChallengerDoesNotChurnHealthyPool(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}, {"b", "r2", "y"}, {"c", "r3", "z"}, {"d", "r4", "z"}}, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		for _, id := range []string{"a", "b", "c"} {
			e.Record(id, Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
		}
		now = now.Add(5 * time.Second)
	}
	for i := 0; i < 3; i++ {
		e.Record("d", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 295 * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	if got := e.Snapshot().Active; contains(got, "d") || !contains(got, "c") {
		t.Fatalf("negligible advantage replaced a healthy standby: %v", got)
	}
	e.Record("c", Sample{Epoch: 1, Error: "https_timeout"}, true)
	if !contains(e.Snapshot().Active, "d") {
		t.Fatal("retention prevented immediate failure replacement")
	}
}

func TestStandbyColdResponseCostHasNoThirdSampleCliff(t *testing.T) {
	s := Stats{EWMA: 300, P50: 300, P90: 300, P95: 300, FirstResponseCount: 2, FirstResponseMedian: 2500, FirstResponseP95: 2500}
	before := LatencyCost(s)
	s.FirstResponseCount = 3
	if after := LatencyCost(s); math.Abs(after-before) > 1 || before <= 300 {
		t.Fatalf("unchanged cold response changed cost at sample three: %.0f -> %.0f", before, after)
	}
}

func TestStandbyQualifiedMaterialImprovementStillReplacesSlowBackup(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}, {"b", "r2", "y"}, {"c", "r3", "z"}, {"d", "r4", "z"}}, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		for index, id := range []string{"a", "b", "c"} {
			e.Record(id, Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: time.Duration(200+index*150) * time.Millisecond}, true)
		}
		now = now.Add(5 * time.Second)
	}
	for i := 0; i < 3; i++ {
		e.Record("d", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 250 * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	if got := e.Attempts(); !contains(got, "d") || contains(got, "c") || got[0] != "a" {
		t.Fatalf("qualified faster standby could not replace slow backup after hold: %v", got)
	}
	for _, event := range e.Snapshot().Selections {
		if event.Kind == "backup" && event.Node == "d" {
			if event.Previous != "c" || event.Cost != 250 || event.PreviousCost != 500 || !event.Qualified || event.Samples != 3 {
				t.Fatalf("replacement decision evidence missing: %+v", event)
			}
			return
		}
	}
	t.Fatal("missing qualified standby replacement event")
}

func TestStandbyExpiryIsNotReportedAsQualityReplacement(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}, {"b", "r2", "y"}}, func() time.Time { return now })
	for _, id := range []string{"a", "b"} {
		e.Record(id, Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
	}
	now = now.Add(91 * time.Second)
	e.Record("a", Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
	for _, event := range e.Snapshot().Selections {
		if event.Kind == "removed" && event.Node == "b" {
			if event.Reason != "health_check_expired" {
				t.Fatalf("expired observation mislabeled: %s", event.Reason)
			}
			return
		}
	}
	t.Fatal("missing standby expiry event")
}
