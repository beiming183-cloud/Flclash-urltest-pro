package outboundgroup

import (
	"context"
	"crypto/x509"
	"fmt"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
)

type stableSiteFaultProxy struct {
	C.Proxy
	calls []string
}

func (p *stableSiteFaultProxy) URLTestWithTiming(_ context.Context, url string, _ utils.IntRanges[uint16]) (C.URLTestResult, error) {
	p.calls = append(p.calls, url)
	if url == "https://www.gstatic.com/generate_204" {
		return C.URLTestResult{}, context.DeadlineExceeded
	}
	return C.URLTestResult{Delay: 120, ReusedConnection: true}, nil
}
func (p *stableSiteFaultProxy) AliveForTestUrl(string) bool { return true }

func TestSharedControlSiteFailureDeprioritizesSiteWithoutRejectingGoodRoutes(t *testing.T) {
	for i := 0; i < 4; i++ {
		ctx := context.WithValue(context.Background(), stableProbeRouteKey{}, stableProbeRoute{network: t.Name(), relay: fmt.Sprint(i)})
		p := &stableSiteFaultProxy{}
		sample := probeStable(ctx, p, "203.0.113.10", false, 1, func() bool { return true })
		if !sample.OK {
			t.Fatal("independent working control was ignored", sample)
		}
		if i == 3 && (len(p.calls) != 1 || p.calls[0] != "https://cp.cloudflare.com/generate_204") {
			t.Fatal("shared slow control kept delaying all routes", p.calls)
		}
	}
}

func TestProbeSiteEvidenceIsScopedAndDoesNotCountAliasesAsIndependent(t *testing.T) {
	h := &stableSiteHealth{sites: map[string]*stableSiteState{}}
	now := time.Now()
	sites := []string{"first", "second"}
	ctx := context.WithValue(context.Background(), stableProbeRouteKey{}, stableProbeRoute{network: "a", relay: "same-host"})
	for i := 0; i < 10; i++ {
		h.observe(ctx, "first", context.DeadlineExceeded, now)
	}
	if h.ordered(ctx, sites, now)[0] != "first" {
		t.Fatal("one host falsely provided independent site evidence")
	}
	for _, relay := range []string{"second-host", "third-host"} {
		other := context.WithValue(context.Background(), stableProbeRouteKey{}, stableProbeRoute{network: "a", relay: relay})
		h.observe(other, "first", context.DeadlineExceeded, now)
	}
	if h.ordered(ctx, sites, now)[0] != "second" {
		t.Fatal("independent repeated failures did not change preference")
	}
	otherNetwork := context.WithValue(context.Background(), stableProbeRouteKey{}, stableProbeRoute{network: "b", relay: "same-host"})
	if h.ordered(otherNetwork, sites, now)[0] != "first" || h.ordered(ctx, sites, now.Add(31*time.Second))[0] != "first" {
		t.Fatal("site preference leaked to another network or never recovered")
	}
	for _, relay := range []string{"x", "y", "z"} {
		other := context.WithValue(context.Background(), stableProbeRouteKey{}, stableProbeRoute{network: "b", relay: relay})
		h.observe(other, "first", x509.UnknownAuthorityError{}, now)
	}
	if h.ordered(otherNetwork, sites, now)[0] != "first" {
		t.Fatal("certificate rejection was hidden by site rotation")
	}
}

func TestReorderedExitServicesKeepParsingAndIdentityChecks(t *testing.T) {
	for _, ip := range []string{"203.0.113.10", "203.0.113.11"} {
		calls := 0
		err, hard := verifyStableExitServices("203.0.113.10", func(string) ([]byte, error) { calls++; return []byte("ip=" + ip + "\n"), nil }, []string{"https://www.cloudflare.com/cdn-cgi/trace", "https://api.ipify.org"})
		if calls != 1 || (ip == "203.0.113.10" && (err != nil || hard)) || (ip != "203.0.113.10" && (err == nil || !hard)) {
			t.Fatal("site rotation changed exit identity requirements", ip, err, hard, calls)
		}
	}
}
