package outboundgroup

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func TestV7RecoveryReservesCreditsBeforeMarkingCandidateTried(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.engine.Invalidate()
	for i := 0; i < 4; i++ {
		s.engine.ReservePriority(stability.RecoveryProbe)
	}
	s.probe = func(ctx context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		<-ctx.Done()
		return stability.Sample{Epoch: epoch, Error: "cancelled"}
	}
	ids := s.engine.StartupCandidates()
	if !s.launchProbeMode(s.ctx, ids[0], true, true, true) {
		t.Fatal("the funded candidate did not launch")
	}
	if s.launchProbeMode(s.ctx, ids[1], true, true, true) {
		t.Fatal("two workers claimed the same two recovery credits")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startupTried[ids[1]] {
		t.Fatal("a candidate that never acquired a lease was marked tried")
	}
}

func TestV6RecoveryFindsSparseReachableRouteWithinBoundedRounds(t *testing.T) {
	s, _, _, _ := testStable(t)
	var clockMu sync.Mutex
	now := time.Now().Add(-time.Hour)
	candidates := []stability.Candidate{}
	s.byID, s.byName = map[string]C.Proxy{}, map[string]string{}
	for i := 0; i < 12; i++ {
		id, name := fmt.Sprintf("%024x", 100+i), fmt.Sprintf("route-%02d", i)
		candidates = append(candidates, stability.Candidate{ID: id, Relay: fmt.Sprintf("%024x", 1000+i), Airport: fmt.Sprintf("%024x", 2000+i%3)})
		s.byID[id] = adapter.NewProxy(outbound.NewBase(outbound.BaseOption{Name: name, Type: C.Http}))
		s.byName[name] = id
	}
	var err error
	s.engine, err = stability.New(candidates, func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now })
	if err != nil {
		t.Fatal(err)
	}
	var seenMu sync.Mutex
	seen := map[string]bool{}
	s.probe = func(_ context.Context, p C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		if !reserve() || !reserve() {
			return stability.Sample{Epoch: epoch, Error: "budget_exhausted"}
		}
		seenMu.Lock()
		seen[p.Name()] = true
		seenMu.Unlock()
		if p.Name() != "route-11" {
			return stability.Sample{Epoch: epoch, Error: "https_timeout"}
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}
	}
	for round := 0; round < 4 && s.Now() == ""; round++ {
		clockMu.Lock()
		now = now.Add(30 * time.Second)
		clockMu.Unlock()
		s.mu.Lock()
		s.recoveryNext = time.Time{}
		// Advance the coordinator's wall-clock deadlines with the test clock.
		for id, retry := range s.recoveryNodes {
			retry.next = time.Time{}
			s.recoveryNodes[id] = retry
		}
		s.mu.Unlock()
		s.startupProbes(s.ctx)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			pending := len(s.scheduled)
			s.mu.Unlock()
			if pending == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	if s.Now() != "route-11" {
		seenMu.Lock()
		defer seenMu.Unlock()
		t.Fatalf("the only reachable route was starved after four funded recovery rounds; tried %d/12 routes", len(seen))
	}
}

func TestV6SchedulerDoesNotLaunchAnUnaffordableProbe(t *testing.T) {
	for _, verify := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "identity"}[verify], func(t *testing.T) {
			s, _, _, _ := testStable(t)
			remaining := 3
			if verify {
				remaining = 5
			}
			for i := 0; i < 12-remaining; i++ {
				s.engine.ReservePriority(stability.MaintenanceProbe)
			}
			var calls atomic.Int32
			s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
				calls.Add(1)
				return stability.Sample{Epoch: epoch, OK: true}
			}
			for i := 0; i < 50; i++ {
				if s.launchProbe(s.ctx, s.engine.Snapshot().Primary, verify, true) {
					t.Fatal("insufficient whole-operation credits still launch a worker and wake the scheduler again")
				}
			}
			if calls.Load() != 0 {
				t.Fatal("unaffordable work made a network request")
			}
		})
	}
}

func TestRecoveryScansUnseenRouteAlongsideHistoricalConfirmation(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.engine.Invalidate()
	ids := s.engine.StartupCandidates()
	if len(ids) != 2 {
		t.Fatal("fixture")
	}
	s.startupTried[ids[0]] = true
	s.recoveryActive = true
	s.scheduled["held-one"], s.scheduled["held-two"] = true, true
	s.quickScheduled = map[string]bool{"held-one": true, "held-two": true}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("held-extra-%d", i)
		s.scheduled[id], s.quickScheduled[id] = true, true
	}
	called := make(chan string, 2)
	s.probe = func(ctx context.Context, p C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		called <- p.Name()
		<-ctx.Done()
		return stability.Sample{Epoch: epoch, Error: "cancelled"}
	}
	s.startupProbes(s.ctx)
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case name := <-called:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("search and confirmation did not both get a turn", seen)
		}
	}
}
