package stability

import (
	"testing"
	"time"
)

func TestVerifiedBootstrapDoesNotWaitForThreeRounds(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "airport"}}, func() time.Time { return now })
	e.Record("a", Sample{Epoch: e.Snapshot().Epoch, OK: true, Latency: 300 * time.Millisecond}, false)
	if len(e.Attempts()) != 0 {
		t.Fatal("ordinary green card bypassed exit verification")
	}
	e.Record("a", Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
	if e.Snapshot().Primary != "a" {
		t.Fatal("verified usable chain left the group without a primary")
	}
}

func TestPowerSaveStandbyDoesNotExpireBeforeNextCheck(t *testing.T) {
	now := time.Unix(1000, 0)
	e, _ := New([]Candidate{{"a", "r1", "airport"}}, func() time.Time { return now })
	e.Configure(false, true)
	for i := 0; i < 3; i++ {
		e.Record("a", Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}, true)
		now = now.Add(5 * time.Second)
	}
	now = now.Add(70 * time.Second)
	if len(e.Attempts()) != 1 {
		t.Fatal("power-save interval invalidated a healthy candidate")
	}
	now = now.Add(3 * time.Minute)
	if len(e.Attempts()) != 0 {
		t.Fatal("expired power-save candidate was retained indefinitely")
	}
}
