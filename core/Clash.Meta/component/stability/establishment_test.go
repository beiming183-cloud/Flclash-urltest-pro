package stability

import (
	"context"
	"testing"
	"time"
)

func TestEstablishmentHedgeUsesColdDialNotWarmRTT(t *testing.T) {
	now := time.Now()
	s := Stats{EWMA: 200, P50: 200, DialP90: 1200, DialCount: 3, LastDial: now}
	if got := EstablishmentHedgeDelay(s, now); got != 1350*time.Millisecond {
		t.Fatal("hedge ignored cold dial", got)
	}
	s.DialP90 = 100
	if got := EstablishmentHedgeDelay(s, now); got != 750*time.Millisecond {
		t.Fatal("hedge too aggressive", got)
	}
	s.DialP90 = 5000
	if got := EstablishmentHedgeDelay(s, now); got != 1500*time.Millisecond {
		t.Fatal("hedge unbounded", got)
	}
}

func TestOverlappingBusinessDialsKeepWinnerAndPerAttemptRoute(t *testing.T) {
	j, err := OpenJournal(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	f := j.business.begin("chatgpt.com", "")
	a := BusinessRoute{Group: j.ID("group"), Node: j.ID("a"), Epoch: 1, Protocol: "Http"}
	b := BusinessRoute{Group: a.Group, Node: j.ID("b"), Epoch: 1, Protocol: "Socks5"}
	an := f.DialAttempt(a, "IP1-兔兔云-001")
	bn := f.DialAttempt(b, "IP1-夜煞云-002-S5")
	f.DialOutcome(b, "IP1-夜煞云-002-S5", bn, nil, time.Second, true)
	f.DialOutcome(a, "IP1-兔兔云-001", an, context.Canceled, 2*time.Second, false)
	f.Open([]string{"IP1"}, 0)
	if f.event.Route.Node != b.Node {
		t.Fatal("cancelled loser overwrote winning route")
	}
	found := false
	for _, e := range j.business.pending {
		if e.Event == "proxy_dial_failed" && e.Attempt == an {
			found = true
			if e.Route.Node != a.Node || !validBusinessEvent(e) {
				t.Fatal("overlapping attempt log corrupt", e)
			}
		}
	}
	if !found {
		t.Fatal("loser cancellation missing from diagnostic")
	}
	for _, e := range []Event{{Event: "dial_hedge", Reason: "slow_establishment", Group: a.Group, Node: b.Node}, {Event: "dial_discarded", Reason: "qualification_changed", Group: a.Group, Node: a.Node}} {
		if err := j.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	if j.dropped != 0 {
		t.Fatal("new diagnostic events silently dropped")
	}
	f.Finish()
	files := businessArchive(t, j)
	if len(files["events.jsonl"]) == 0 {
		t.Fatal("establishment logs missing from export")
	}
}
