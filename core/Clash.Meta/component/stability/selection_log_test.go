package stability

import (
	"testing"
	"time"
)

func TestSelectionLogRecordsRealChangesWithoutRoutineNoise(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"slow", "r1", "a1"}, {"fast", "r2", "a2"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 34; i++ {
		e.Record("slow", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 450 * time.Millisecond, FirstResponse: time.Second}, true)
		now = now.Add(2 * time.Second)
	}
	if len(e.Snapshot().Selections) != 1 {
		t.Fatal("Routine healthy checks filled the selection log")
	}
	for i := 0; i < 3; i++ {
		e.Record("fast", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond, FirstResponse: 1500 * time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	found := false
	for _, event := range e.Snapshot().Selections {
		if event.Kind == "primary" && event.Node == "fast" && event.Previous == "slow" {
			found = event.Reason == "latency_quality_improved" && event.Latency == 200 && event.PreviousLatency == 450 && event.FirstResponse == 1500
		}
	}
	if !found {
		t.Fatalf("Real switch or its request measurements missing: %+v", e.Snapshot().Selections)
	}
	e.Record("fast", Sample{Epoch: epoch, Error: "https_timeout"}, true)
	last := e.Snapshot().Selections
	if last[len(last)-1].Node != "slow" || last[len(last)-1].Reason != "https_timeout" {
		t.Fatalf("Timeout selection reason missing: %+v", last)
	}
}

func TestSelectionLogBoundedCopiedAndRestored(t *testing.T) {
	now := time.Unix(1000, 0)
	candidates := []Candidate{{"000000000000000000000001", "000000000000000000000011", "000000000000000000000021"}, {"000000000000000000000002", "000000000000000000000012", "000000000000000000000022"}}
	e, _ := New(candidates, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for _, candidate := range candidates {
		e.Record(candidate.ID, Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond}, true)
	}
	for i := 0; i < 150; i++ {
		e.SetPrimary(candidates[i%2].ID)
	}
	state := e.Snapshot()
	if len(state.Selections) != 128 {
		t.Fatalf("Unbounded history: %d", len(state.Selections))
	}
	state.Selections[0].Reason = "mutated"
	if e.Snapshot().Selections[0].Reason == "mutated" {
		t.Fatal("Snapshot shares mutable selection storage")
	}
	other, _ := New(candidates, func() time.Time { return now })
	other.Restore(e.Snapshot())
	if len(other.Snapshot().Selections) != 128 || len(other.Snapshot().Active) != 0 {
		t.Fatal("Restoration lost the history or restored an unverified route")
	}
}

func TestSelectionHistoryPersistsThroughExistingJournal(t *testing.T) {
	directory := t.TempDir()
	journal, err := OpenJournal(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{journal.ID("node"), journal.ID("relay"), journal.ID("airport")}
	e, _ := New([]Candidate{candidate}, nil)
	e.Record(candidate.ID, Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond, FirstResponse: 2 * time.Second}, true)
	if err := journal.Store(journal.ID("group"), e.Statistics()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenJournal(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := New([]Candidate{candidate}, nil)
	other.Restore(reopened.Statistics(journal.ID("group")))
	if len(other.Snapshot().Selections) != 1 || other.Snapshot().Selections[0].Latency != 200 {
		t.Fatal("Persisted selection history lost its normalized request latency")
	}
}
