package main

import (
	"github.com/metacubex/mihomo/tunnel"
	"testing"
)

func TestDeviceIdleDoesNotChangeTrafficLifecycle(t *testing.T) {
	previous := tunnel.Status()
	t.Cleanup(func() {
		if previous == tunnel.Running {
			tunnel.OnRunning()
		} else if previous == tunnel.Inner {
			tunnel.OnInnerLoading()
		} else {
			tunnel.OnSuspend()
		}
	})
	for _, running := range []bool{true, false} {
		if running {
			tunnel.OnRunning()
		} else {
			tunnel.OnSuspend()
		}
		expected := tunnel.Status()
		handleSuspend(true)
		if tunnel.Status() != expected {
			t.Fatalf("idle changed traffic lifecycle: running=%v", running)
		}
		handleSuspend(false)
		if tunnel.Status() != expected {
			t.Fatalf("wake changed traffic lifecycle: running=%v", running)
		}
	}
}
