package main

import (
	"errors"
	"net"
	"syscall"

	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/process"
	C "github.com/metacubex/mihomo/constant"
)

var errBlocked = errors.New("blocked")

type tunHookOwner interface {
	handleProtect(int)
	handleResolveProcess(net.Addr, net.Addr) string
}

func makeTunHooks(owner tunHookOwner, shouldBlock func() bool) (dialer.SocketControl, process.PackageNameResolver) {
	socket := func(_, _ string, conn syscall.RawConn) error {
		if shouldBlock() {
			return errBlocked
		}
		return conn.Control(func(fd uintptr) { owner.handleProtect(int(fd)) })
	}
	resolve := func(metadata *C.Metadata) (string, error) {
		src, dst := metadata.RawSrcAddr, metadata.RawDstAddr
		if src == nil || dst == nil {
			return "", process.ErrInvalidNetwork
		}
		return owner.handleResolveProcess(src, dst), nil
	}
	return socket, resolve
}
