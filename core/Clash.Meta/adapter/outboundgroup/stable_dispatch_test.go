package outboundgroup

import (
	"context"
	"testing"
	"time"
)

func TestStableDispatchPreemptsOptionalWorkForMaintenance(t *testing.T) {
	d := newStableProbeDispatch(4)
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		release, err := d.acquire(ctx, "IP1", 2, cancel)
		if err != nil {
			t.Fatal(err)
		}
		go func() { <-ctx.Done(); release() }()
		t.Cleanup(cancel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	release, err := d.acquire(ctx, "IP2", 0, cancel)
	if err != nil {
		t.Fatal("maintenance starved behind optional probes", err)
	}
	release()
	if d.snapshot()["preempted"].(uint64) != 1 {
		t.Fatal("optional capacity was not reclaimed")
	}
}

func TestStableDispatchRecoveryCannotTakeLastMaintenanceSlot(t *testing.T) {
	d := newStableProbeDispatch(3)
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		release, err := d.acquire(ctx, "IP1", 1, cancel)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		t.Cleanup(cancel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if release, err := d.acquire(ctx, "IP2", 1, cancel); err == nil {
		release()
		t.Fatal("recovery took maintenance reserve")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	release, err := d.acquire(ctx2, "IP2", 0, cancel2)
	if err != nil {
		t.Fatal("maintenance reserve unavailable", err)
	}
	release()
	release()
}

func TestStableDispatchAlternatesAccountsWithinPriority(t *testing.T) {
	d := newStableProbeDispatch(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.mu.Lock()
	for _, group := range []string{"IP1", "IP1", "IP1", "IP2"} {
		d.queue = append(d.queue, &stableProbeJob{group: group, class: 0, ctx: ctx, cancel: cancel, ready: make(chan struct{}), queued: time.Now()})
	}
	d.dispatchLocked()
	groups := map[string]int{}
	for job := range d.active {
		groups[job.group]++
	}
	d.mu.Unlock()
	if groups["IP1"] != 1 || groups["IP2"] != 1 {
		t.Fatal("same-priority account starvation", groups)
	}
}

func TestStableDispatchThreeGroupsProgressWithThousandQueuedJobs(t *testing.T) {
	d := newStableProbeDispatch(3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.mu.Lock()
	for i := 0; i < 1000; i++ {
		group := "IP1"
		if i == 998 {
			group = "ordinary-airport-health"
		}
		if i == 999 {
			group = "IP2"
		}
		d.queue = append(d.queue, &stableProbeJob{group: group, class: 0, ctx: ctx, cancel: cancel, ready: make(chan struct{}), queued: time.Now()})
	}
	d.dispatchLocked()
	groups := map[string]int{}
	for job := range d.active {
		groups[job.group]++
	}
	d.mu.Unlock()
	if groups["IP1"] != 1 || groups["IP2"] != 1 || groups["ordinary-airport-health"] != 1 {
		t.Fatal("one account starved behind other recurring groups", groups)
	}
}
