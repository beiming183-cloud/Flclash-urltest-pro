package provider

import (
	"context"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
)

type queuedHealthProxy struct {
	C.Proxy
	called chan error
}

func (p *queuedHealthProxy) Name() string { return "queued-background" }
func (p *queuedHealthProxy) URLTest(ctx context.Context, _ string, _ utils.IntRanges[uint16]) (uint16, error) {
	p.called <- ctx.Err()
	return 1, nil
}
func (p *queuedHealthProxy) AliveForTestUrl(string) bool       { return true }
func (p *queuedHealthProxy) LastDelayForTestUrl(string) uint16 { return 1 }

func TestHealthCheckNetworkDeadlineStartsAfterSharedQueue(t *testing.T) {
	p := &queuedHealthProxy{called: make(chan error, 1)}
	hc := NewHealthCheck([]C.Proxy{p}, "https://example.invalid", 20, 0, true, nil)
	defer hc.close()
	hc.backgroundLease = func(ctx context.Context) (context.Context, func(), error) {
		select {
		case <-time.After(80 * time.Millisecond):
			return ctx, func() {}, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	hc.check()
	select {
	case err := <-p.called:
		if err != nil {
			t.Fatal("queue wait consumed the network deadline", err)
		}
	default:
		t.Fatal("queued health check never reached its network operation")
	}
}
