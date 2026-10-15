package outboundgroup

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func TestStartupFirstRequestUsesVerifiedBackupWhilePrimaryProbeStalls(t *testing.T) {
	s, a, b, _ := testStable(t)
	s.Stop()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	var goodChecks atomic.Int32
	s.probe = func(ctx context.Context, p C.Proxy, _ string, verify bool, epoch uint64, _ func() bool) stability.Sample {
		if p.Name() == "chain-a" {
			<-ctx.Done()
			return stability.Sample{Epoch: epoch, Error: "cancelled"}
		}
		if !verify {
			t.Error("startup bypassed exit validation")
		}
		goodChecks.Add(1)
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	m := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	m.SetRemoteAddress("example.com:443")
	started := time.Now()
	conn, err := s.DialContext(ctx, m)
	if err != nil {
		t.Fatalf("first request waited behind a stalled candidate instead of the verified backup: %v", err)
	}
	defer conn.Close()
	if time.Since(started) > 300*time.Millisecond || goodChecks.Load() != 1 || s.Now() != "chain-b" {
		t.Fatal("startup recovery was delayed or duplicated")
	}
	if a.payload.Load() != 0 || b.payload.Load() != 0 {
		t.Fatal("startup sent application data during validation")
	}
}

func TestStartupConcurrentCallersShareValidationAndCallerCancelIsLocal(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.Stop()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	release := make(chan struct{})
	var checks atomic.Int32
	s.probe = func(ctx context.Context, p C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		if p.Name() == "chain-a" {
			<-ctx.Done()
			return stability.Sample{Epoch: epoch, Error: "cancelled"}
		}
		checks.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return stability.Sample{Epoch: epoch, Error: "cancelled"}
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			m := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
			m.SetRemoteAddress("example.com:443")
			c, err := s.DialContext(ctx, m)
			if c != nil {
				c.Close()
			}
			errors <- err
		}()
	}
	deadline := time.Now().Add(time.Second)
	for checks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	m := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	m.SetRemoteAddress("example.com:443")
	if c, err := s.DialContext(cancelled, m); err == nil {
		c.Close()
		t.Fatal("cancelled caller succeeded")
	}
	close(release)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal("another caller cancelled shared validation", err)
		}
	}
	if checks.Load() != 1 {
		t.Fatal("concurrent startup duplicated validation", checks.Load())
	}
}

func TestStartupStillRequiresFreshTLSAndExpectedExit(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.Stop()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Latency: time.Millisecond}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	m.SetRemoteAddress("example.com:443")
	if c, err := s.DialContext(ctx, m); err == nil {
		c.Close()
		t.Fatal("startup accepted a node without an exit check")
	}
}

func TestStartupSchedulerBeginsWithoutWaitingForFirstTimer(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.Stop()
	s.probe = func(_ context.Context, _ C.Proxy, _ string, verify bool, epoch uint64, _ func() bool) stability.Sample {
		if !verify {
			t.Error("startup did not request fresh exit validation")
		}
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}
	}
	s.Start()
	deadline := time.Now().Add(400 * time.Millisecond)
	for s.Now() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Now() == "" {
		t.Fatal("startup waited for the one-second maintenance timer")
	}
}
