package stability

import (
	"testing"
	"time"
)

func TestPhoneBackgroundTimeoutDoesNotEmptyVerifiedPool(t *testing.T) {
	now := time.Now()
	e, _ := New([]Candidate{{ID: "a", Relay: "ra"}, {ID: "b", Relay: "rb"}, {ID: "c", Relay: "rc"}}, func() time.Time { return now })
	for _, id := range []string{"a", "b", "c"} {
		e.Record(id, Sample{Epoch: 1, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
	}
	now = now.Add(5 * time.Second)
	for _, id := range []string{"a", "b", "c"} {
		e.Record(id, Sample{Epoch: 1, Error: "https_timeout", Phase: "delay", Service: "control_backup"}, true)
	}
	if len(e.Attempts()) != 3 {
		t.Fatalf("one background timeout removed recent verified routes: %v", e.Snapshot().Active)
	}
	now = now.Add(31 * time.Second)
	if len(e.Attempts()) != 0 {
		t.Fatal("a suspect route retained eligibility indefinitely")
	}
}

func TestPhoneNetworkRoundTripRetainsOnlyItsOwnOrdering(t *testing.T) {
	now := time.Now()
	cs := []Candidate{{"aaaaaaaaaaaaaaaaaaaaaaaa", "111111111111111111111111", "999999999999999999999999"}, {"bbbbbbbbbbbbbbbbbbbbbbbb", "222222222222222222222222", "999999999999999999999999"}}
	e, _ := New(cs, func() time.Time { return now })
	change := func(key string) { e.Invalidate(); e.SwitchNetwork(key); e.Invalidate() }
	qualify := func(a, b time.Duration) {
		for i, delay := range []time.Duration{a, b} {
			e.Record(cs[i].ID, Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: delay}, true)
		}
	}
	change("ffffffffffffffffffffffff")
	qualify(30*time.Millisecond, 600*time.Millisecond)
	change("eeeeeeeeeeeeeeeeeeeeeeee")
	qualify(800*time.Millisecond, 40*time.Millisecond)
	change("ffffffffffffffffffffffff")
	if got := e.StartupCandidates()[0]; got != cs[0].ID {
		t.Fatalf("returning network borrowed another network's ordering: %s", got)
	}
	if len(e.Attempts()) != 0 {
		t.Fatal("network history granted stale TLS or exit eligibility")
	}
}
