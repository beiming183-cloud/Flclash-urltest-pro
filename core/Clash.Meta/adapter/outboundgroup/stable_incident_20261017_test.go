package outboundgroup

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

// Incident regression: a failed probe period exhausts the hour quota. Even
// after the network recovers, a healthy replacement never gets revalidated.
// This drives the same awaitStartup path used by a real browser request.
func TestRecoveredNetworkCanProbeAfterHourlyBudgetExhaustion(t *testing.T) {
	s, _, _, _ := testStable(t)
	for s.engine.Reserve(true) {
	}
	s.engine.Invalidate()
	var calls atomic.Int32
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		calls.Add(1)
		if !reserve() {
			return stability.Sample{Epoch: epoch, Error: "budget_exhausted"}
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := s.awaitStartup(ctx, s.ctx); err != nil {
		t.Fatalf("recovered healthy chain cannot re-enter empty pool: probes=%d, quota=%d, request=%v", calls.Load(), s.engine.Snapshot().BudgetUsed, err)
	}
	if calls.Load() == 0 {
		t.Fatal("recovery returned without a fresh validation")
	}
}

func TestActiveMaintenanceDoesNotUseExplorationQuota(t *testing.T) {
	s, _, _, _ := testStable(t)
	for s.engine.Reserve(true) {
	}
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		if !reserve() {
			t.Error("active maintenance blocked by exploration quota")
			return stability.Sample{Epoch: epoch, Error: "budget_exhausted"}
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}
	}
	id := s.engine.Snapshot().Primary
	sample, err := s.Probe(context.Background(), id, true, true)
	if err != nil || !sample.OK {
		t.Fatal("active chain could not renew validation", err)
	}
	state := s.engine.Snapshot()
	if state.BudgetUsed != 900 || state.ProbeBudgets[stability.MaintenanceProbe].HourRequests != 1 {
		t.Fatal("maintenance was not isolated from exploration", state.ProbeBudgets)
	}
}

func TestConcurrentRecoveryWaitersDoNotSweepPool(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.engine.Invalidate()
	var calls atomic.Int32
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		if reserve() {
			calls.Add(1)
		}
		return stability.Sample{Epoch: epoch, Error: "https_timeout"}
	}
	var wg sync.WaitGroup
	started := time.Now()
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.startupProbes(s.ctx, true) }()
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatalf("concurrent business retries scanned multiple candidates: %d", calls.Load())
	}
	s.mu.Lock()
	failures, next := s.recoveryFailures, s.recoveryNext
	s.mu.Unlock()
	budgets := s.engine.Snapshot().ProbeBudgets
	if failures > 1 || next.Before(started.Add(75*time.Millisecond)) || budgets[stability.SearchProbe].HourRequests != 1 || budgets[stability.RecoveryProbe].HourRequests != 1 {
		t.Fatal("concurrent waiters duplicated the independent search/confirmation pair", failures, budgets)
	}
}

func TestIdleSchedulerRecoversAfterIndependentRateRefills(t *testing.T) {
	s, _, _, _ := testStable(t)
	cs := []stability.Candidate{}
	for _, n := range s.engine.Snapshot().Nodes {
		cs = append(cs, n.Candidate)
	}
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	e, _ := stability.New(cs, func() time.Time { return time.Unix(0, clock.Load()) })
	for e.Reserve(true) {
	}
	for e.ReservePriority(stability.RecoveryProbe) {
	}
	s.engine = e
	s.tlsChecking = true
	calls := make(chan struct{}, 1)
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		if !reserve() {
			return stability.Sample{Epoch: epoch, Error: "budget_exhausted"}
		}
		select {
		case calls <- struct{}{}:
		default:
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Second}
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.schedule(s.ctx) }()
	clock.Add(int64(10 * time.Second))
	s.wake <- struct{}{}
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("idle group requires a browser request to recover")
	}
}

// The parent must leave the fallback a complete request allowance after the
// primary times out. Use the same total deadline as the actual recovery probe.
func TestAvailabilityExitFallbackHasUsableRemainingDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), stableAvailabilityTimeout())
	defer cancel()
	err, hard := verifyStableExit("203.0.113.10", func(url string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(parent, stableExitRequestTimeout(true))
		defer cancel()
		wait := 10 * time.Second
		if url == "https://www.cloudflare.com/cdn-cgi/trace" {
			wait = 800 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
			return []byte("ip=203.0.113.10\n"), nil
		}
	})
	if err != nil || hard {
		t.Fatalf("healthy 800ms fallback cannot fit after first exit timeout: %v (hard=%v)", err, hard)
	}
}
