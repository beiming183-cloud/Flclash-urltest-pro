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

func TestIP2WarmPrimaryDoesNotStarveQualifiedBackupMaintenance(t *testing.T) {
	s := newColdStable(t)
	cs := []stability.Candidate{}
	for _, n := range s.engine.Snapshot().Nodes {
		cs = append(cs, n.Candidate)
	}
	var clockMu sync.Mutex
	now := time.Now().Add(-89 * time.Second)
	s.engine, _ = stability.New(cs, func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now })
	s.ctx, s.cancel = context.WithCancel(context.Background())
	primary, backup, cold := s.byName["chain-a"], s.byName["chain-b"], s.byName["chain-c"]
	epoch := s.engine.Snapshot().Epoch
	s.engine.Record(primary, stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true}, true)
	s.engine.Record(backup, stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true}, true)
	clockMu.Lock()
	now = now.Add(89 * time.Second)
	clockMu.Unlock()
	s.engine.Record(primary, stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true}, true)
	if s.engine.Snapshot().Primary != primary || len(s.engine.Snapshot().Active) != 2 {
		t.Fatal("fixture did not retain a warming primary and recently verified backup")
	}
	maintained := make(chan struct{}, 1)
	s.probe = func(ctx context.Context, p C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		if p.Name() == "chain-b" {
			select {
			case maintained <- struct{}{}:
			default:
			}
			return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 100 * time.Millisecond}
		}
		<-ctx.Done()
		return stability.Sample{Epoch: epoch, Error: "cancelled"}
	}
	if !s.launchProbeMode(s.ctx, cold, true, true, true) {
		t.Fatal("cold availability search did not start")
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.schedule(s.ctx) }()
	s.wake <- struct{}{}
	select {
	case <-maintained:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("qualified backup maintenance was blocked while the primary warmed and a cold search occupied a slot")
	}
}

func TestIP2FullConfirmationDoesNotWaitBehindEntireColdSweep(t *testing.T) {
	s := newColdStable(t)
	cs := []stability.Candidate{}
	s.byID, s.byName = map[string]C.Proxy{}, map[string]string{}
	for i := 0; i < 16; i++ {
		id, name := fmt.Sprintf("%024x", i+100), fmt.Sprintf("incident-route-%02d", i)
		cs = append(cs, stability.Candidate{ID: id, Relay: fmt.Sprintf("%024x", i+1000), Airport: fmt.Sprintf("%024x", i%3+2000)})
		s.byID[id] = adapter.NewProxy(outbound.NewBase(outbound.BaseOption{Name: name, Type: C.Http}))
		s.byName[name] = id
	}
	s.engine, _ = stability.New(cs, nil)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	good := s.byID[s.engine.StartupCandidates()[0]].Name()
	var searches, confirmations atomic.Int32
	s.probe = func(ctx context.Context, p C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		if !reserve() || !reserve() {
			return stability.Sample{Epoch: epoch, Error: "budget_exhausted"}
		}
		if search, _ := ctx.Value(stableSearchKey{}).(bool); search {
			searches.Add(1)
			<-ctx.Done()
			return stability.Sample{Epoch: epoch, Error: "exit_query_timeout"}
		}
		if p.Name() == good {
			confirmations.Add(1)
			select {
			case <-time.After(100 * time.Millisecond):
				return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true}
			case <-ctx.Done():
				return stability.Sample{Epoch: epoch, Error: "cancelled"}
			}
		}
		<-ctx.Done()
		return stability.Sample{Epoch: epoch, Error: "exit_query_timeout"}
	}
	deadline := time.Now().Add(stableSearchTimeout + time.Second)
	for len(s.engine.Snapshot().Active) == 0 && time.Now().Before(deadline) {
		s.startupProbes(s.ctx)
		time.Sleep(10 * time.Millisecond)
	}
	if len(s.engine.Snapshot().Active) == 0 {
		t.Fatalf("usable first route never received full confirmation before the remaining cold sweep: searches=%d confirmations=%d", searches.Load(), confirmations.Load())
	}
}
