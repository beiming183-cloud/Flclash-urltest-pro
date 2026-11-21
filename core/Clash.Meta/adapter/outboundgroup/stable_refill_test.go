package outboundgroup

import (
	"context"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
	"testing"
	"time"
)

func TestSchedulerRefillsUnderfilledPoolWhenCompetitionBudgetPaused(t *testing.T) {
	s, _, _, _ := testStable(t)
	cs := []stability.Candidate{}
	for _, n := range s.engine.Snapshot().Nodes {
		cs = append(cs, n.Candidate)
	}
	cs = append(cs, stability.Candidate{ID: "third", Relay: "relay-third", Airport: "airport-third"})
	s.byID["third"] = s.byID[cs[0].ID]
	frozen := time.Now()
	e, err := stability.New(cs, func() time.Time { return frozen })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 510; i++ {
		if !e.Reserve(true) {
			t.Fatal("fixture budget")
		}
	}
	frozen = frozen.Add(28 * time.Minute)
	for i := 0; i < 3; i++ {
		for _, c := range cs[:2] {
			e.Record(c.ID, stability.Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
		}
		frozen = frozen.Add(5 * time.Second)
	}
	s.engine = e
	state := e.Snapshot()
	if len(state.Active) != 2 || competitionBudgetAvailable(state) {
		t.Fatalf("fixture does not reproduce budget gate: %+v", state)
	}
	s.tlsChecking = true
	calls := make(chan struct{}, 4)
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		select {
		case calls <- struct{}{}:
		default:
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 350 * time.Millisecond}
	}
	ctx := s.ctx
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.schedule(ctx) }()
	s.wake <- struct{}{}
	select {
	case <-calls:
		deadline := time.Now().Add(300 * time.Millisecond)
		for len(e.Snapshot().Active) < 3 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if len(e.Snapshot().Active) != 3 {
			t.Fatal("refill probe did not restore third validated candidate")
		}
	case <-time.After(700 * time.Millisecond):
		t.Fatal("pool stayed 2/3: refill incorrectly shares paused competition budget")
	}
}

func TestRefillHasIndependentRateBudget(t *testing.T) {
	state := stability.Snapshot{BudgetUsed: 900, BudgetResetIn: 1912, ProbeBudgets: map[stability.ProbeClass]stability.ProbeBudgetState{stability.RecoveryProbe: {Tokens: 2}}}
	if !refillBudgetAvailable(state) {
		t.Fatal("exploration quota must not prevent refill")
	}
	state.ProbeBudgets[stability.RecoveryProbe] = stability.ProbeBudgetState{Tokens: 1}
	if refillBudgetAvailable(state) {
		t.Fatal("refill must reserve room for a full verification")
	}
}

func TestFirstStandbyReserveOnlyAfterPrimary(t *testing.T) {
	e, _ := stability.New([]stability.Candidate{{ID: "a", Relay: "r1"}, {ID: "b", Relay: "r2"}}, nil)
	for e.ReservePriority(stability.RecoveryProbe) {
	}
	state := e.Snapshot()
	if refillBudgetAvailable(state) {
		t.Fatal("cold scan used first-standby reserve")
	}
	e.Record("a", stability.Sample{Epoch: state.Epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}, true)
	state = e.Snapshot()
	_, _, priority, cost := stableProbePolicy(state, "b", true, true, true)
	if !refillBudgetAvailable(state) || priority != stability.StandbyProbe || cost != 2 {
		t.Fatal("standby budget unavailable after restoring primary", state.Active, priority)
	}
	consume, release, ok := e.LeaseProbe(priority, cost)
	if !ok || !consume() || !consume() {
		t.Fatal("reserve cannot fund full verification")
	}
	release()
	if refillBudgetAvailable(e.Snapshot()) {
		t.Fatal("standby budget unbounded")
	}
}
