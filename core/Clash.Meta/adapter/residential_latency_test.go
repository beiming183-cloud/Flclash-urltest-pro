package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
)

type delayedLatencyAdapter struct {
	C.ProxyAdapter
	kind  C.AdapterType
	relay string
}

func (a delayedLatencyAdapter) Type() C.AdapterType { return a.kind }

func (a delayedLatencyAdapter) ProxyInfo() C.ProxyInfo {
	return C.ProxyInfo{DialerProxy: a.relay}
}

func (a delayedLatencyAdapter) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}
	return a.ProxyAdapter.DialContext(ctx, metadata)
}

func TestResidentialURLTestShowsEstablishedRequestLatency(t *testing.T) {
	previous := UnifiedDelay.Load()
	UnifiedDelay.Store(true)
	t.Cleanup(func() { UnifiedDelay.Store(previous) })
	for _, test := range []struct {
		name  string
		kind  C.AdapterType
		relay string
	}{
		{"ordinary", C.Direct, ""},
		{"residential_http", C.Http, "RF2-Relay-one"},
		{"residential_socks", C.Socks5, "RF2-Relay-two"},
		{"stable_group", C.StableFailover, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				time.Sleep(10 * time.Millisecond)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			proxy := NewProxy(delayedLatencyAdapter{ProxyAdapter: outbound.NewDirect(), kind: test.kind, relay: test.relay})
			status, _ := utils.NewUnsignedRanges[uint16]("204")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			delay, err := proxy.URLTest(ctx, server.URL, status)
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 2 || delay < 5 || delay >= 140 {
				t.Fatalf("First establishment polluted displayed request latency: requests=%d delay=%d", requests.Load(), delay)
			}
		})
	}
}

func TestResidentialTimingSeparatesBothMeasurements(t *testing.T) {
	previous := UnifiedDelay.Load()
	UnifiedDelay.Store(false)
	t.Cleanup(func() { UnifiedDelay.Store(previous) })
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	proxy := NewProxy(delayedLatencyAdapter{ProxyAdapter: outbound.NewDirect(), kind: C.Socks5, relay: "RF2-Relay-one"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := proxy.URLTestWithTiming(ctx, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReusedConnection || result.Delay < 5 || result.Delay >= 140 || result.FirstResponse < 150*time.Millisecond || requests.Load() != 2 {
		t.Fatalf("Measurements mixed or missing: %+v requests=%d", result, requests.Load())
	}
	ordinary := NewProxy(delayedLatencyAdapter{ProxyAdapter: outbound.NewDirect(), kind: C.Direct})
	result, err = ordinary.URLTestWithTiming(ctx, server.URL, nil)
	if err != nil || result.ReusedConnection || result.Delay < 150 || requests.Load() != 3 {
		t.Fatalf("Ordinary unified-delay setting was overridden: %+v err=%v", result, err)
	}
}

func TestResidentialWarmRequestFailureDoesNotShowColdSuccess(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	proxy := NewProxy(delayedLatencyAdapter{ProxyAdapter: outbound.NewDirect(), kind: C.Http, relay: "RF2-Relay-one"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := proxy.URLTestWithTiming(ctx, server.URL, nil)
	if err == nil || result.Delay != 0 || proxy.AliveForTestUrl(server.URL) || result.FirstResponse < 140*time.Millisecond {
		t.Fatalf("Failed warm measurement was reported as success: %+v err=%v alive=%v", result, err, proxy.AliveForTestUrl(server.URL))
	}
}
