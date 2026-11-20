package outboundgroup

import (
	"context"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
	"testing"
	"time"
)

func TestMobileWaitsForNewStandbyAfterOnlyRouteFails(t *testing.T) {
	s, a, _, _ := testStable(t)
	s.engine.Invalidate()
	epoch := s.engine.Snapshot().Epoch
	sample := stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}
	s.engine.Record(s.byName["chain-a"], sample, true)
	a.status.Store(502)
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		time.Sleep(200 * time.Millisecond)
		s.engine.Record(s.byName["chain-b"], sample, true)
	}()
	defer func() { <-ready }()
	meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	meta.SetRemoteAddress("example.com:443")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := s.DialContext(ctx, meta)
	if err != nil {
		t.Fatalf("gave up before newly qualified standby: %v", err)
	}
	conn.Close()
}
