package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	C "github.com/metacubex/mihomo/constant"
)

func TestProbeAccountingChargesBothRequestsAndCountsTransportBytes(t *testing.T) {
	for _, credits := range []int64{1, 2} {
		t.Run(string(rune('0'+credits)), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			traffic := &C.ProbeTraffic{}
			left := credits
			ctx := C.WithProbeAccounting(context.Background(), traffic, func() bool {
				if left == 0 {
					return false
				}
				left--
				return true
			})
			proxy := NewProxy(delayedLatencyAdapter{ProxyAdapter: outbound.NewDirect(), kind: C.Socks5, relay: "RF2-Relay-test"})
			_, err := proxy.URLTestWithTiming(ctx, server.URL, nil)
			if credits == 1 && (err == nil || !strings.Contains(err.Error(), "budget_exhausted")) {
				t.Fatal("second request escaped its budget", err)
			}
			if credits == 2 && err != nil {
				t.Fatal(err)
			}
			if requests.Load() != credits || traffic.Requests.Load() != credits || traffic.Sent.Load() == 0 || traffic.Received.Load() == 0 {
				t.Fatal("actual public checks missing from accounting", requests.Load(), traffic.Requests.Load(), traffic.Sent.Load(), traffic.Received.Load())
			}
		})
	}
}

func TestCanceledOptionalHealthCheckDoesNotPoisonRouteHistory(t *testing.T) {
	started := make(chan struct{}, 1)
	blocking := atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blocking.Load() {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	proxy := NewProxy(outbound.NewDirect())
	if _, err := proxy.URLTest(context.Background(), server.URL, nil); err != nil {
		t.Fatal(err)
	}
	before := proxy.LastDelayForTestUrl(server.URL)
	blocking.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := proxy.URLTest(ctx, server.URL, nil); done <- err }()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("optional health check did not begin")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("optional cancellation did not release its connection")
	}
	if !proxy.AliveForTestUrl(server.URL) || proxy.LastDelayForTestUrl(server.URL) != before {
		t.Fatal("priority preemption turned a working ordinary route unhealthy")
	}
}

func TestPublicChecksDoNotKeepMobileQualityActiveButOrdinaryTrafficDoes(t *testing.T) {
	previous := C.ProxyBusinessActivity.Load()
	t.Cleanup(func() { C.ProxyBusinessActivity.Store(previous) })
	C.ProxyBusinessActivity.Store(42)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	proxy := NewProxy(outbound.NewDirect())
	ctx := C.WithProbeAccounting(context.Background(), &C.ProbeTraffic{}, func() bool { return true })
	if _, err := proxy.URLTest(ctx, server.URL, nil); err != nil {
		t.Fatal(err)
	}
	if C.ProxyBusinessActivity.Load() != 42 {
		t.Fatal("public health traffic extended the business activity window")
	}
	meta := &C.Metadata{NetWork: C.TCP, Type: C.HTTP}
	meta.SetRemoteAddress(server.Listener.Addr().String())
	started := time.Now().UnixNano()
	conn, err := proxy.DialContext(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if C.ProxyBusinessActivity.Load() < started {
		t.Fatal("ordinary user traffic failed to wake mobile quality work")
	}
}
