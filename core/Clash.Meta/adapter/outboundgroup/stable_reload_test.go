package outboundgroup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"
)

func TestStartupRequestSurvivesGroupReplacement(t *testing.T) {
	for _, during := range []bool{false, true} {
		name := "previously_resolved"
		if during {
			name = "awaiting_qualification"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			prev, a, b, _ := testStableAt(t, "http", home)
			next, c, d, _ := testStableAt(t, "http", home)
			for _, g := range []*Stable{prev, next} {
				g.online = true
				g.networkKey = "startup"
			}
			tunnel.SetStabilityRunning(true)
			t.Cleanup(func() { tunnel.SetStabilityRunning(false); tunnel.UpdateProxies(nil, nil) })
			tunnel.UpdateProxies(map[string]C.Proxy{"IP1": adapter.NewProxy(prev)}, nil)
			selected := tunnel.Proxies()["IP1"]
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
			meta.SetRemoteAddress("example.com:443")
			type outcome struct {
				c   C.Conn
				err error
			}
			result := make(chan outcome, 1)
			dial := func() { conn, err := selected.DialContext(ctx, meta); result <- outcome{conn, err} }
			if during {
				prev.engine.Invalidate()
				entered := make(chan struct{})
				var once sync.Once
				prev.probe = func(ctx context.Context, _ C.Proxy, _ string, _ bool, _ uint64, _ func() bool) stability.Sample {
					once.Do(func() { close(entered) })
					<-ctx.Done()
					return stability.Sample{Error: "cancelled"}
				}
				go dial()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("request never reached the original group")
				}
			}
			tunnel.UpdateProxies(map[string]C.Proxy{"IP1": adapter.NewProxy(next)}, nil)
			if !during {
				go dial()
			}
			out := <-result
			if out.err != nil {
				t.Fatalf("new IP1 is usable but a startup request failed: %v", out.err)
			}
			if _, err := out.c.Write([]byte("once")); err != nil {
				t.Fatal(err)
			}
			out.c.Close()
			until := time.Now().Add(time.Second)
			for c.payload.Load()+d.payload.Load() < 4 && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if a.payload.Load()+b.payload.Load() != 0 || c.payload.Load()+d.payload.Load() != 4 {
				t.Fatal("payload lost, replayed, or sent on retired route")
			}
		})
	}
}

func TestStartupHandoffKeepsResidentialBinding(t *testing.T) {
	for _, v := range []struct{ name, group, exit, account, server string }{
		{"group", "IP2", "203.0.113.10", "same-account", "198.51.100.10"},
		{"expected_exit", "IP1", "203.0.113.20", "same-account", "198.51.100.10"},
		{"account", "IP1", "203.0.113.10", "another-account", "198.51.100.10"},
		{"endpoint", "IP1", "203.0.113.10", "same-account", "198.51.100.11"},
	} {
		t.Run(v.name, func(t *testing.T) {
			home := t.TempDir()
			old, _, _, _ := testStableAt(t, "http", home)
			next, _, _, _ := testStableBinding(t, "http", home, v.group, v.exit, v.account, v.server)
			ready := make(chan struct{})
			old.ArmStableHandoff(next, ready)
			close(ready)
			old.Stop()
			if c, err := old.DialContext(context.Background(), &C.Metadata{}); !errors.Is(err, stability.ErrServiceStopped) || c != nil {
				t.Fatalf("a different %s accepted the old request: %v", v.name, err)
			}
		})
	}
}

func TestStartupHandoffHonorsCallerStopAndSingleTransfer(t *testing.T) {
	home := t.TempDir()
	old, _, _, _ := testStableAt(t, "http", home)
	next, _, _, _ := testStableAt(t, "http", home)
	third, _, _, _ := testStableAt(t, "http", home)
	ready := make(chan struct{})
	old.ArmStableHandoff(next, ready)
	old.Stop()
	meta := &C.Metadata{Type: C.INNER}
	meta.SetRemoteAddress("example.com:443")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Millisecond)
	defer cancel()
	if c, err := old.DialContext(ctx, meta); c != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("reload ignored original deadline", err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	close(ready)
	if c, err := old.DialContext(cancelled, meta); c != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled request transferred", err)
	}
	next.ArmStableHandoff(third, ready)
	next.Stop()
	if c, err := old.DialContext(context.Background(), meta); c != nil || !errors.Is(err, stability.ErrServiceStopped) {
		t.Fatal("retried more than one generation", err)
	}
	third.Stop()
	if c, err := third.DialContext(context.Background(), meta); c != nil || !errors.Is(err, stability.ErrServiceStopped) {
		t.Fatal("manual stop revived route", err)
	}
}

func TestStartupHandoffAcrossNetworkRestart(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.engine.Invalidate()
	firstOwner := s.ctx
	entered := make(chan struct{})
	var once sync.Once
	s.probe = func(ctx context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		if firstOwner.Err() == nil {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return stability.Sample{Error: "cancelled"}
		}
		return stability.Sample{Epoch: epoch, OK: true, Exit: true, TLS: true, Latency: time.Millisecond}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	meta := &C.Metadata{Type: C.INNER, NetWork: C.TCP}
	meta.SetRemoteAddress("example.com:443")
	result := make(chan error, 1)
	go func() {
		c, err := s.DialContext(ctx, meta)
		if c != nil {
			c.Close()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("never waited for first network")
	}
	s.NetworkChanged("new-test-network", true)
	if err := <-result; err != nil {
		t.Fatal("network restart lost pending request", err)
	}
	s.NetworkChanged("", false)
	if c, err := s.DialContext(ctx, meta); c != nil || !errors.Is(err, stability.ErrServiceStopped) {
		t.Fatal("offline transition revived a route", err)
	}
}

func TestStartupWaitingRequestsSkipScheduledPoolScan(t *testing.T) {
	s, _, _, _ := testStable(t)
	candidates := make([]stability.Candidate, 267)
	for i := range candidates {
		candidates[i] = stability.Candidate{ID: fmt.Sprintf("%024x", i+1), Relay: fmt.Sprintf("r%d", i), Airport: fmt.Sprintf("a%d", i%3)}
	}
	e, err := stability.New(candidates, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = e
	s.recoveryNext = time.Now().Add(time.Minute)
	t0 := time.Now()
	allocs := testing.AllocsPerRun(20, func() { s.startupProbes(s.ctx, true) })
	t.Logf("267 candidates, per deferred waiter: %.0f allocations, %s per call", allocs, time.Since(t0)/21)
	if allocs > 1 {
		t.Fatalf("each waiter rescanned an already scheduled 267-candidate pool: %.0f allocations", allocs)
	}
}
