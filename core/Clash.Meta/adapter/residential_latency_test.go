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

func TestResidentialURLTestIncludesEstablishmentWithUnifiedDelay(t *testing.T) {
	previous := UnifiedDelay.Load()
	UnifiedDelay.Store(true)
	t.Cleanup(func() { UnifiedDelay.Store(previous) })
	for _, test := range []struct {
		name  string
		kind  C.AdapterType
		relay string
		full  bool
	}{
		{"ordinary", C.Direct, "", false},
		{"residential_http", C.Http, "RF2-Relay-one", true},
		{"residential_socks", C.Socks5, "RF2-Relay-two", true},
		{"stable_group", C.StableFailover, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
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
			if test.full {
				if requests.Load() != 1 || delay < 140 {
					t.Fatalf("Establishment excluded: requests=%d delay=%d", requests.Load(), delay)
				}
			} else if requests.Load() != 2 {
				t.Fatalf("Ordinary unified testing changed: requests=%d", requests.Load())
			}
		})
	}
}
