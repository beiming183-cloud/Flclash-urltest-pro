package constant

import (
	"context"
	"net"
	"sync/atomic"
)

type ProbeTraffic struct {
	Requests atomic.Int64
	Sent     atomic.Int64
	Received atomic.Int64
}

var ProxyBusinessActivity atomic.Int64

type probeAccountingKey struct{}
type probeAccounting struct {
	traffic *ProbeTraffic
	charge  func() bool
}

func WithProbeAccounting(ctx context.Context, traffic *ProbeTraffic, charge func() bool) context.Context {
	return context.WithValue(ctx, probeAccountingKey{}, probeAccounting{traffic: traffic, charge: charge})
}

func HasProbeAccounting(ctx context.Context) bool {
	_, ok := ctx.Value(probeAccountingKey{}).(probeAccounting)
	return ok
}

func ChargeProbeRequest(ctx context.Context) bool {
	a, ok := ctx.Value(probeAccountingKey{}).(probeAccounting)
	if !ok {
		return true
	}
	if !a.charge() {
		return false
	}
	a.traffic.Requests.Add(1)
	return true
}

type probeTrafficConn struct {
	net.Conn
	traffic *ProbeTraffic
}

func (c *probeTrafficConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.traffic.Received.Add(int64(n))
	return n, err
}

func (c *probeTrafficConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.traffic.Sent.Add(int64(n))
	return n, err
}

func TrackProbeConn(ctx context.Context, conn net.Conn) net.Conn {
	if a, ok := ctx.Value(probeAccountingKey{}).(probeAccounting); ok && conn != nil {
		return &probeTrafficConn{Conn: conn, traffic: a.traffic}
	}
	return conn
}
