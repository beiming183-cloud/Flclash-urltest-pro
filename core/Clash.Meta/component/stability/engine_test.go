package stability

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestEligibilityStickyPrimaryAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	e, err := New([]Candidate{{"a", "r1", "airport1"}, {"b", "r2", "airport2"}, {"c", "r1", "airport1"}, {"d", "r3", "airport3"}}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	epoch := e.Snapshot().Epoch
	for i := 0; i < 3; i++ {
		for _, id := range []string{"a", "b", "c", "d"} {
			ms := 100 * time.Millisecond
			if id == "b" {
				ms = 80 * time.Millisecond
			}
			e.Record(id, Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: ms}, true)
		}
		if i < 2 {
			if e.Snapshot().Primary != "a" {
				t.Fatal("verified bootstrap did not retain its first primary")
			}
			now = now.Add(5 * time.Second)
		}
	}
	if e.Snapshot().Primary != "a" {
		t.Fatal("healthy primary was replaced", e.Snapshot())
	}
	if got := e.Snapshot().Active; len(got) != 3 || got[2] != "d" {
		t.Fatal("relay diversity", got)
	}
	now = now.Add(91 * time.Second)
	if len(e.Attempts()) != 0 {
		t.Fatal("stale standby accepted")
	}
}

func TestNetworkLRUIsolationAndReverification(t *testing.T) {
	e, _ := New([]Candidate{{"a", "r1", "airport"}}, nil)
	first := fmt.Sprintf("%024x", 1)
	second := fmt.Sprintf("%024x", 2)
	e.SwitchNetwork(first)
	e.Reserve(false)
	epoch := e.Snapshot().Epoch
	e.Record("a", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}, true)
	e.SwitchNetwork(second)
	if e.Snapshot().Nodes[0].Count != 0 {
		t.Fatal("statistics crossed networks")
	}
	e.SwitchNetwork(first)
	if e.Snapshot().Nodes[0].Count != 1 || len(e.Attempts()) != 0 {
		t.Fatal("statistics not restored or eligibility restored")
	}
	if e.Snapshot().BudgetUsed != 1 {
		t.Fatal("network switch reset hourly budget")
	}
	for i := 3; i < 20; i++ {
		e.SwitchNetwork(fmt.Sprintf("%024x", i))
	}
	state := e.Statistics()
	data, _ := json.Marshal(state)
	if len(state.Networks) > 8 || len(data) > ExportLimit {
		t.Fatal("network statistics exceed bounds")
	}
	restored, _ := New([]Candidate{{"a", "r1", "airport"}}, nil)
	restored.Restore(state)
	if len(restored.Attempts()) != 0 {
		t.Fatal("restart restored qualification")
	}
}

func TestFailureDedupCooldownAndEpoch(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	e.Record("a", Sample{Epoch: epoch, Error: "tls_aborted"}, true)
	if e.Record("a", Sample{Epoch: epoch, Error: "tls_aborted"}, true) {
		t.Fatal("duplicate failure counted")
	}
	now = now.Add(3 * time.Second)
	e.Record("a", Sample{Epoch: epoch, Error: "tls_aborted"}, true)
	if !e.Snapshot().Nodes[0].CoolingUntil.After(now) {
		t.Fatal("no cooldown")
	}
	e.Invalidate()
	if e.Record("a", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true}, true) {
		t.Fatal("old epoch accepted")
	}
	if len(e.Attempts()) != 0 {
		t.Fatal("old eligibility survived")
	}
}

func TestBudgetAndConcurrency(t *testing.T) {
	e, _ := New([]Candidate{{"a", "r1", "x"}}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				e.Reserve(false)
				e.Snapshot()
			}
		}()
	}
	wg.Wait()
	if e.Snapshot().BudgetUsed != 840 {
		t.Fatal(e.Snapshot().BudgetUsed)
	}
	for i := 0; i < 60; i++ {
		if !e.Reserve(true) {
			t.Fatal("fault budget missing")
		}
	}
	if e.Reserve(true) {
		t.Fatal("budget exceeded")
	}
}

func TestHardFailureAndManualProbeDoesNotElect(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "x"}}, func() time.Time { return now })
	epoch := e.Snapshot().Epoch
	for i := 0; i < 3; i++ {
		e.Record("a", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}, false)
		now = now.Add(5 * time.Second)
	}
	if e.Snapshot().Primary != "" {
		t.Fatal("manual probe changed actual route")
	}
	if !e.SetPrimary("a") {
		t.Fatal("valid manual choice rejected")
	}
	e.Record("a", Sample{Epoch: epoch, Hard: true, Error: "unexpected_exit"}, true)
	if len(e.Attempts()) != 0 {
		t.Fatal("wrong exit still usable")
	}
}
