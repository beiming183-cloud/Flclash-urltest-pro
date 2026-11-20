package outboundgroup

import (
	"context"
	"crypto/x509"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type mobileSlowExitTLS struct{ C.Proxy }

func (*mobileSlowExitTLS) DialContext(ctx context.Context, _ *C.Metadata) (C.Conn, error) {
	select {
	case <-time.After(1450 * time.Millisecond):
		return nil, x509.UnknownAuthorityError{}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestMobileSearchReachesDelayedTLSVerdict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), stableSearchTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, stableSearchKey{}, true)
	ctx = context.WithValue(ctx, stableAvailabilityKey{}, true)
	sample := probeStable(ctx, &mobileSlowExitTLS{}, "203.0.113.10", true, 1, func() bool { return true })
	if sample.OK || !sample.Hard || sample.Error != "cert_untrusted" {
		t.Fatalf("lost endpoint verdict: %s hard=%v", sample.Error, sample.Hard)
	}
}

func TestOrdinaryRecoveryCanCheckBeforeFirstBusiness(t *testing.T) {
	now := time.Now()
	if !allowOrdinaryRecovery(now, now.Add(-time.Second).UnixNano(), 0) || allowOrdinaryRecovery(now, now.Add(-time.Minute).UnixNano(), 0) || !allowOrdinaryRecovery(now, 0, now.Add(-time.Minute).UnixNano()) {
		t.Fatal("cold-start or idle policy broken")
	}
}

func TestQueuedConfirmationDoesNotCancelOnlyRemainingSearchSlot(t *testing.T) {
	d := newStableProbeDispatch(2)
	r1, e := d.acquire(context.Background(), "IP1", 1, func() {})
	if e != nil {
		t.Fatal(e)
	}
	defer r1()
	search, stop := context.WithCancel(context.Background())
	defer stop()
	r2, e := d.acquire(search, "airport", 2, stop)
	if e != nil {
		t.Fatal(e)
	}
	defer r2()
	wait, done := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer done()
	_, e = d.acquire(wait, "IP2", 1, done)
	if e == nil || search.Err() != nil {
		t.Fatal("lost the only exploration slot while confirmation was ineligible")
	}
}
