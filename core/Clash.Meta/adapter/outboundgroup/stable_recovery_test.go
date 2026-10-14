package outboundgroup

import (
	"context"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func TestRequestLatencyDoesNotShortenKnownEstablishmentDeadline(t *testing.T) {
	s, a, _, _ := testStable(t)
	s.Stop()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	a.delay = 1800 * time.Millisecond
	epoch := s.engine.Snapshot().Epoch
	s.engine.Record(s.byName["chain-a"], stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 200 * time.Millisecond, FirstResponse: 2200 * time.Millisecond}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	metadata := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	_ = metadata.SetRemoteAddress("example.com:443")
	conn, err := s.DialContext(ctx, metadata)
	if err != nil {
		t.Fatalf("A healthy 200ms request route was timed out before its known establishment completed: %v", err)
	}
	_ = conn.Close()
}

func TestGreenObservationRequiresVerificationAndThenBootstraps(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.Stop()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.ObserveDelay("chain-a", "https://www.gstatic.com/generate_204", 300)
	state := s.engine.Snapshot()
	if state.Primary != "" || state.Nodes[0].Count == 0 {
		t.Fatal("ordinary measurement selected a route or was discarded")
	}
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}
	}
	if _, err := s.Probe(context.Background(), s.byName["chain-a"], true, true); err != nil {
		t.Fatal(err)
	}
	if s.Now() != "chain-a" {
		t.Fatal("verified green candidate failed to become primary")
	}
}

func TestDuplicateNetworkNotificationPreservesQualification(t *testing.T) {
	s, _, _, qualify := testStable(t)
	s.NetworkChanged("cellular-1", true)
	qualify()
	before := s.engine.Snapshot()
	s.NetworkChanged("cellular-1", true)
	after := s.engine.Snapshot()
	if before.Primary == "" || before.Primary != after.Primary || before.Epoch != after.Epoch {
		t.Fatal("duplicate network callback cleared the healthy primary")
	}
	s.NetworkChanged("wifi-2", true)
	if s.Now() != "" || s.engine.Snapshot().Epoch == before.Epoch {
		t.Fatal("real network change retained stale verification")
	}
}
