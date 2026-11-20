package outboundgroup

import (
	"context"
	"fmt"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

func TestHedgeWinsDoNotRepeatedlyInvalidateTheSameRouteView(t *testing.T) {
	s, a, b, _ := testStable(t)
	s.engine.SetPrimary(s.byName["chain-a"])
	a.delay, b.delay = 2*time.Second, time.Millisecond
	previous := OnStableRouteChange
	t.Cleanup(func() { OnStableRouteChange = previous })
	seen := map[string]int{}
	OnStableRouteChange = func() {
		state := s.engine.Snapshot()
		key := fmt.Sprint(state.Epoch, state.Generation, state.Primary, state.Active)
		seen[key]++
	}
	for i := 0; i < 4; i++ {
		meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
		meta.SetRemoteAddress("example.com:443")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := s.DialContext(ctx, meta)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	for key, count := range seen {
		if count > 1 {
			t.Fatalf("unchanged route view notified %d times, repeatedly cancelling UI checks: %s", count, key)
		}
	}
	if len(seen) == 0 {
		t.Fatal("route notifications were disabled instead of deduplicated")
	}
	s.engine.Invalidate()
	s.notifyRouteChange()
	if len(seen) < 2 {
		t.Fatal("real network invalidation did not notify the UI")
	}
}
