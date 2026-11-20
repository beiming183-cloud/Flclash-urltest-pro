package main

import (
	"errors"
	"net"
	"testing"

	"github.com/metacubex/mihomo/component/process"
	C "github.com/metacubex/mihomo/constant"
)

type hookTestOwner struct {
	protected int
	name      string
}

func (o *hookTestOwner) handleProtect(fd int)                      { o.protected = fd }
func (o *hookTestOwner) handleResolveProcess(_, _ net.Addr) string { return o.name }

type hookRawConn struct{}

func (hookRawConn) Control(f func(uintptr)) error  { f(73); return nil }
func (hookRawConn) Read(func(uintptr) bool) error  { return nil }
func (hookRawConn) Write(func(uintptr) bool) error { return nil }

func TestTunHooksKeepTheirOwnerDuringPublicationAndReplacement(t *testing.T) {
	owner := &hookTestOwner{name: "starting-vpn"}
	socket, resolve := makeTunHooks(owner, func() bool { return false })
	var published *hookTestOwner
	if err := socket("tcp", "example.com:443", hookRawConn{}); err != nil {
		t.Fatal(err)
	}
	if owner.protected != 73 || published != nil {
		t.Fatal("starting owner was not used before publication")
	}
	published = &hookTestOwner{name: "replacement-vpn"}
	meta := &C.Metadata{RawSrcAddr: &net.TCPAddr{}, RawDstAddr: &net.TCPAddr{}}
	name, err := resolve(meta)
	if err != nil || name != owner.name || published.protected != 0 {
		t.Fatal("hook followed another VPN lifetime", name, err)
	}
}

func TestTunHooksKeepBlockAndInvalidMetadataChecks(t *testing.T) {
	owner := &hookTestOwner{}
	socket, resolve := makeTunHooks(owner, func() bool { return true })
	if !errors.Is(socket("tcp", "example.com:443", hookRawConn{}), errBlocked) || owner.protected != 0 {
		t.Fatal("blocked socket was protected")
	}
	if _, err := resolve(&C.Metadata{}); !errors.Is(err, process.ErrInvalidNetwork) {
		t.Fatal("invalid metadata accepted", err)
	}
}
