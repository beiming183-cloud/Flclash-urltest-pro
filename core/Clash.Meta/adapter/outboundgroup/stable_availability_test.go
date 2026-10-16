package outboundgroup

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
)

type availabilityOnlyProxy struct {
	C.Proxy
	benchmarks atomic.Int32
	exitDials  atomic.Int32
}

func (p *availabilityOnlyProxy) URLTestWithTiming(context.Context, string, utils.IntRanges[uint16]) (C.URLTestResult, error) {
	p.benchmarks.Add(1)
	return C.URLTestResult{}, errors.New("benchmark must not block availability")
}

func (p *availabilityOnlyProxy) DialContext(_ context.Context, metadata *C.Metadata) (C.Conn, error) {
	if metadata.Host == "api.ipify.org" || metadata.Host == "www.cloudflare.com" {
		p.exitDials.Add(1)
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
}

func TestAvailabilityChecksExitBeforeLatencyBenchmarkAndStillFailsClosed(t *testing.T) {
	p := &availabilityOnlyProxy{}
	ctx := context.WithValue(context.Background(), stableAvailabilityKey{}, true)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	sample := probeStable(ctx, p, "203.0.113.10", true, 1, func() bool { return true })
	if p.benchmarks.Load() != 0 || p.exitDials.Load() != 2 {
		t.Fatal("startup benchmark blocked the lightweight exit check", p.benchmarks.Load(), p.exitDials.Load())
	}
	if sample.OK || sample.Exit || sample.Latency != 0 || sample.Service != "bootstrap" {
		t.Fatal("failed exit verification became a usable route or latency sample", sample)
	}
}
