package outboundgroup

import (
	"fmt"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type testRelay struct {
	C.Proxy
	id     string
	latest C.DelayHistory
}

func (p *testRelay) Name() string { return p.id }
func (p *testRelay) LatestRelaySample(url string) C.DelayHistory {
	if url == "probe" {
		return p.latest
	}
	return C.DelayHistory{}
}

func TestLinkedRelaySelection(t *testing.T) {
	now := time.Now()
	r := &relaySelection{url: "probe"}
	a := &testRelay{id: "A"}
	b := &testRelay{id: "B"}
	ps := []C.Proxy{a, b}
	parents := map[string]bool{"B": true}
	check := func(want C.Proxy) {
		t.Helper()
		if got := r.pick(ps, parents, now); got != want {
			t.Fatalf("got %s want %s", got.Name(), want.Name())
		}
	}
	check(b)
	a.latest = C.DelayHistory{Time: now, Delay: 100}
	b.latest = C.DelayHistory{Time: now, Delay: 200}
	check(a)
	a.latest.Delay = 200
	check(b)
	b.latest.Delay = 0
	check(a)
	a.latest.Time = now.Add(-3 * time.Minute)
	check(a)
	b.latest.Time = time.Time{}
	check(b)
}

func TestLinkedRelayRotation(t *testing.T) {
	r := &relaySelection{url: "probe"}
	ps := make([]C.Proxy, 165)
	for i := range ps {
		ps[i] = &testRelay{id: fmt.Sprint(i)}
	}
	seen := map[string]bool{}
	for i := 0; i < 25; i++ {
		batch := r.batch(ps)
		if len(batch) != 8 {
			t.Fatal(len(batch))
		}
		for _, p := range batch {
			seen[p.Name()] = true
		}
	}
	if len(seen) != len(ps) {
		t.Fatalf("covered %d/%d", len(seen), len(ps))
	}
	p := ps[110].(*testRelay)
	p.latest = C.DelayHistory{Time: time.Now(), Delay: 29}
	if batch := r.batch(ps); len(batch) != 5 || batch[0] != p {
		t.Fatal("healthy winner is not in bounded sample")
	}
}
