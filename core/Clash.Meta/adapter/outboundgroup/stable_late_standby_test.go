package outboundgroup

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

type arrivingStandbyDialer struct {
	*connectDialer
	ready func()
}

func (d *arrivingStandbyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.ready()
	return d.connectDialer.DialContext(ctx, network, address)
}

func TestStableUsesStandbyValidatedDuringFailedEstablishment(t *testing.T) {
	s, a, b, _ := testStable(t)
	s.engine.Invalidate()
	epoch := s.engine.Snapshot().Epoch
	aID, bID := s.byName["chain-a"], s.byName["chain-b"]
	s.engine.Record(aID, stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
	a.status.Store(502)
	dialer := &arrivingStandbyDialer{connectDialer: a, ready: func() {
		s.engine.Record(bID, stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 300 * time.Millisecond}, true)
	}}
	proxy, err := outbound.NewHttp(outbound.HttpOption{Name: "chain-a", Server: "198.51.100.10", Port: 8080, UserName: "same-account", Password: "test-password", BasicOption: outbound.BasicOption{DialerProxy: "RF2-Relay-airport-chain-a", DialerForAPI: dialer}})
	if err != nil {
		t.Fatal(err)
	}
	s.byID[aID] = adapter.NewProxy(proxy)
	metadata := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	_ = metadata.SetRemoteAddress("example.com:443")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := s.DialContext(ctx, metadata)
	if err != nil {
		t.Fatalf("newly validated standby was ignored after primary establishment failed: %v", err)
	}
	_ = conn.Close()
	if s.Now() != "chain-b" || a.payload.Load() != 0 || b.payload.Load() != 0 {
		t.Fatal("standby was not used before application payload")
	}
}
