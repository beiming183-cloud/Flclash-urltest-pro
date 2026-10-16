package stability

import (
	"testing"
	"time"
)

func TestBootstrapConnectsBeforeMeasurementThenUpgradesToStableFastNode(t *testing.T) {
	for _, manual := range []bool{false, true} {
		now := time.Unix(1000, 0)
		e, _ := New([]Candidate{{"a", "r1", "x"}, {"b", "r2", "y"}}, func() time.Time { return now })
		epoch := e.Snapshot().Epoch
		e.Record("a", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Phase: "exit", Service: "bootstrap"}, true)
		if e.Snapshot().Primary != "a" || len(e.Attempts()) != 1 || e.Snapshot().Nodes[0].EWMA != 0 {
			t.Fatal("verified availability waited for a latency result")
		}
		if manual && !e.SetPrimary("a") {
			t.Fatal("manual selection failed")
		}
		for i := 0; i < 3; i++ {
			e.Record("a", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 600 * time.Millisecond}, true)
			e.Record("b", Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond}, true)
			if i < 2 {
				if e.Snapshot().Primary != "a" {
					t.Fatal("an unconfirmed faster sample replaced the bootstrap route")
				}
				now = now.Add(5 * time.Second)
			}
		}
		want := "b"
		if manual {
			want = "a"
		}
		if e.Snapshot().Primary != want {
			t.Fatal("bootstrap upgrade waited for the thirty-second hold or ignored manual choice", manual, e.Snapshot().Primary)
		}
	}
}
