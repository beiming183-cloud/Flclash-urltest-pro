package tunnel

import (
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type dialBudgetProxy struct {
	C.Proxy
	kind C.AdapterType
	next C.Proxy
}

func (p *dialBudgetProxy) Type() C.AdapterType              { return p.kind }
func (p *dialBudgetProxy) Unwrap(*C.Metadata, bool) C.Proxy { return p.next }

func TestStableRouteKeepsBackupEstablishmentBudget(t *testing.T) {
	stable := &dialBudgetProxy{kind: C.StableFailover}
	for _, proxy := range []C.Proxy{stable, &dialBudgetProxy{kind: C.Selector, next: stable}} {
		if got := tcpDialTimeout(proxy, &C.Metadata{}); got != 12*time.Second {
			t.Fatalf("stable backup window truncated to %s", got)
		}
	}
}

func TestOrdinaryRouteBudgetAndCyclesStayBounded(t *testing.T) {
	for _, proxy := range []C.Proxy{nil, &dialBudgetProxy{kind: C.Http}, &dialBudgetProxy{kind: C.Selector}} {
		if got := tcpDialTimeout(proxy, &C.Metadata{}); got != C.DefaultTCPTimeout {
			t.Fatalf("ordinary route timeout changed to %s", got)
		}
	}
	loop := &dialBudgetProxy{kind: C.Selector}
	loop.next = loop
	if got := tcpDialTimeout(loop, &C.Metadata{}); got != C.DefaultTCPTimeout {
		t.Fatalf("cyclic route has an unbounded budget: %s", got)
	}
}
