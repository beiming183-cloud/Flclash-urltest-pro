package outboundgroup

import (
	"context"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func TestManualSelectionVerifiesThenAppliesAndCanReturnToAuto(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.engine.Invalidate()
	s.ObserveDelay("chain-b", "https://www.gstatic.com/generate_204", 300)
	if err := s.Set("chain-b"); err != nil {
		t.Fatal(err)
	}
	if s.Now() != "" {
		t.Fatal("unverified manual choice was used")
	}
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}
	}
	if _, err := s.Probe(context.Background(), s.byName["chain-b"], true, true); err != nil {
		t.Fatal(err)
	}
	if s.Now() != "chain-b" || s.requested != "" {
		t.Fatal("verified manual selection was not applied")
	}
	if err := s.Set(""); err != nil {
		t.Fatal(err)
	}
	if s.fixed != "" || s.engine.Snapshot().Paused {
		t.Fatal("return to automatic selection failed")
	}
}
