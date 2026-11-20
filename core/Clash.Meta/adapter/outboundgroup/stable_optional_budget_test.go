package outboundgroup

import (
	"context"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func TestOptionalProbeByteLimitDoesNotDisableQualifiedMaintenance(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.probeUsage(time.Now())
	s.probeTraffic.Sent.Add(33 << 20)
	if s.optionalQualityAvailable(time.Now()) {
		t.Fatal("optional probes exceeded their byte window")
	}
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		if !reserve() {
			t.Fatal("essential maintenance lost its independent request credits")
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true}
	}
	sample, err := s.Probe(s.ctx, s.engine.Snapshot().Primary, false, true)
	if err != nil || !sample.OK {
		t.Fatal("optional byte limit disabled a qualified route", err)
	}
}
