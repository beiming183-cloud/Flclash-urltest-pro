package dialer

import (
	"context"
	"net"
	"syscall"
	"testing"
)

func TestSocketHookRemovalDoesNotInvalidatePendingDial(t *testing.T) {
	previous := DefaultSocketHook
	t.Cleanup(func() { DefaultSocketHook = previous })
	for _, listen := range []bool{false, true} {
		calls := 0
		DefaultSocketHook = func(string, string, syscall.RawConn) error { calls++; return nil }
		d := &net.Dialer{}
		lc := &net.ListenConfig{}
		if listen {
			socketHookToListenConfig(lc)
		} else {
			socketHookToToDialer(d)
		}
		DefaultSocketHook = nil
		if listen {
			if err := lc.Control("udp", "127.0.0.1:0", nil); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := d.ControlContext(context.Background(), "tcp", "127.0.0.1:443", nil); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 1 {
			t.Fatal("pending operation lost its original hook", calls)
		}
	}
}
