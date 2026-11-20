package dialer

import (
	"context"
	"net"
	"syscall"
)

// SocketControl
// never change type traits because it's used in CMFA
type SocketControl func(network, address string, conn syscall.RawConn) error

// DefaultSocketHook
// never change type traits because it's used in CMFA
var DefaultSocketHook SocketControl

func socketHookToToDialer(dialer *net.Dialer) {
	hook := DefaultSocketHook
	addControlToDialer(dialer, func(ctx context.Context, network, address string, c syscall.RawConn) error {
		if hook == nil {
			return nil
		}
		return hook(network, address, c)
	})
}

func socketHookToListenConfig(lc *net.ListenConfig) {
	hook := DefaultSocketHook
	addControlToListenConfig(lc, func(ctx context.Context, network, address string, c syscall.RawConn) error {
		if hook == nil {
			return nil
		}
		return hook(network, address, c)
	})
}
