package stability

import (
	"testing"
	"time"
)

func TestStartupHistorySurvivesRestartWithoutRestoringEligibility(t *testing.T) {
	now := time.Now()
	candidates := []Candidate{{"aaaaaaaaaaaaaaaaaaaaaaaa", "111111111111111111111111", "999999999999999999999999"}, {"bbbbbbbbbbbbbbbbbbbbbbbb", "222222222222222222222222", "888888888888888888888888"}}
	e, _ := New(candidates, func() time.Time { return now })
	e.Record(candidates[0].ID, Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: 500 * time.Millisecond}, true)
	e.Record(candidates[1].ID, Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond}, true)
	e.Invalidate()
	if len(e.Attempts()) != 0 {
		t.Fatal("old validation survived restart")
	}
	if e.StartupCandidates()[0] != candidates[1].ID {
		t.Fatal("history was discarded instead of preferring the fast verified node")
	}
	j, err := OpenJournal(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	group := j.ID("IP1")
	if err = j.Store(group, e.Statistics()); err != nil {
		t.Fatal(err)
	}
	j2, err := OpenJournal(j.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := New(candidates, func() time.Time { return now })
	restored.Restore(j2.Statistics(group))
	restored.SwitchNetwork(j.ID("new-network"))
	restored.Invalidate()
	if len(restored.Attempts()) != 0 || restored.StartupCandidates()[0] != candidates[1].ID {
		t.Fatal("hints became permissions or disappeared during network startup")
	}
	now = now.Add(25 * time.Hour)
	if restored.StartupCandidates()[0] != candidates[0].ID {
		t.Fatal("expired history was treated as current")
	}
}

func TestStartupDoesNotPrioritizeFailedOrUnverifiedHistory(t *testing.T) {
	now := time.Now()
	e, _ := New([]Candidate{{"a", "r1", "airport"}, {"b", "r2", "airport"}}, func() time.Time { return now })
	e.Record("b", Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Latency: time.Millisecond}, true)
	e.Invalidate()
	if e.StartupCandidates()[0] != "a" {
		t.Fatal("missing exit verification was saved as good history")
	}
	e.Record("b", Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}, true)
	now = now.Add(4 * time.Second)
	e.Record("b", Sample{Epoch: e.Snapshot().Epoch, Error: "https_timeout"}, true)
	e.Invalidate()
	if e.StartupCandidates()[0] != "a" {
		t.Fatal("last failed route was prioritized")
	}
}
