package outboundgroup

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

type connectDialer struct {
	status  atomic.Int32
	payload atomic.Int32
	delay   time.Duration
}

func (d *connectDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	if d.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d.delay):
		}
	}
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		r := bufio.NewReader(b)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		status := d.status.Load()
		if status == 407 {
			io.WriteString(b, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
			return
		}
		if status == 502 {
			io.WriteString(b, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			return
		}
		io.WriteString(b, "HTTP/1.1 200 Connection Established\r\n\r\n")
		buf := make([]byte, 1024)
		n, _ := r.Read(buf)
		d.payload.Add(int32(n))
	}()
	return a, nil
}
func (*connectDialer) ListenPacket(context.Context, string, string, netip.AddrPort) (net.PacketConn, error) {
	return nil, errors.New("unused")
}

func testStable(t *testing.T) (*Stable, *connectDialer, *connectDialer, func()) {
	t.Helper()
	C.SetHomeDir(t.TempDir())
	a, b := &connectDialer{}, &connectDialer{}
	proxies := map[string]C.Proxy{}
	for i, name := range []string{"chain-a", "chain-b"} {
		d := a
		if i == 1 {
			d = b
		}
		relay := "RF2-Relay-airport-" + name
		proxies[relay] = adapter.NewProxy(outbound.NewBase(outbound.BaseOption{Name: relay, Type: C.Hysteria2}))
		p, err := outbound.NewHttp(outbound.HttpOption{BasicOption: outbound.BasicOption{DialerProxy: relay, DialerForAPI: d}, Name: name, Server: "198.51.100.10", Port: 8080, UserName: "same-account", Password: "test-password"})
		if err != nil {
			t.Fatal(err)
		}
		proxies[name] = adapter.NewProxy(p)
	}
	s, err := NewStable(GroupCommonOption{Name: "IP1", Proxies: []string{"chain-a", "chain-b"}}, StableOption{ExpectedExit: "203.0.113.10", Endpoints: []string{"198.51.100.10:8080"}}, proxies)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Now()
	cs := []stability.Candidate{}
	for _, n := range s.engine.Snapshot().Nodes {
		cs = append(cs, n.Candidate)
	}
	s.engine, err = stability.New(cs, func() time.Time { mu.Lock(); defer mu.Unlock(); return now })
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	t.Cleanup(s.Stop)
	qualify := func() {
		epoch := s.engine.Snapshot().Epoch
		for i := 0; i < 3; i++ {
			for _, n := range cs {
				s.engine.Record(n.ID, stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Second}, true)
			}
			if i < 2 {
				mu.Lock()
				now = now.Add(5 * time.Second)
				mu.Unlock()
			}
		}
	}
	qualify()
	return s, a, b, qualify
}

func TestStableCONNECTFailoverBeforeApplicationPayload(t *testing.T) {
	s, a, b, _ := testStable(t)
	a.status.Store(407)
	meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	meta.SetRemoteAddress("example.com:443")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := s.DialContext(ctx, meta)
	if err != nil {
		t.Fatal(err)
	}
	if s.Now() != "chain-b" {
		t.Fatal("backup did not become primary")
	}
	if a.payload.Load() != 0 || b.payload.Load() != 0 {
		t.Fatal("application payload sent during failover")
	}
	conn.Write([]byte("payload once"))
	conn.Close()
	deadline := time.Now().Add(time.Second)
	for b.payload.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if a.payload.Load() != 0 || b.payload.Load() != 12 {
		t.Fatal("payload replay or loss", a.payload.Load(), b.payload.Load())
	}
}

func TestStableTotalDeadlineAndAllCandidatesFailClosed(t *testing.T) {
	s, a, b, _ := testStable(t)
	a.delay = time.Second
	b.delay = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	meta := &C.Metadata{NetWork: C.TCP}
	meta.SetRemoteAddress("example.com:443")
	start := time.Now()
	if _, err := s.DialContext(ctx, meta); err == nil {
		t.Fatal("cancelled dial succeeded")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("deadline reset between attempts")
	}
	a.delay = 0
	b.delay = 0
	a.status.Store(502)
	b.status.Store(502)
	if _, err := s.DialContext(context.Background(), meta); err == nil {
		t.Fatal("all-dead residential group fell through")
	}
}

func TestStableLifecycleAndManualProbeRoute(t *testing.T) {
	s, _, _, _ := testStable(t)
	before := s.Now()
	s.probe = func(_ context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: time.Millisecond}
	}
	if _, err := s.Probe(context.Background(), s.byName["chain-b"], true, false); err != nil {
		t.Fatal(err)
	}
	if s.Now() != before {
		t.Fatal("manual test changed route")
	}
	for i := 0; i < 100; i++ {
		s.Stop()
		s.Start()
	}
	s.Stop()
	if len(s.engine.Attempts()) != 0 {
		t.Fatal("qualification survived service stop")
	}
	if _, err := s.Probe(context.Background(), s.byName["chain-b"], false, false); err == nil {
		t.Fatal("probe after service stop")
	}
}

func TestStableIdentityRejectsDIRECTAndMixedTerminal(t *testing.T) {
	C.SetHomeDir(t.TempDir())
	p := adapter.NewProxy(outbound.NewDirect())
	_, err := NewStable(GroupCommonOption{Name: "IP1", Proxies: []string{"DIRECT"}}, StableOption{ExpectedExit: "203.0.113.10", Endpoints: []string{"198.51.100.10:8080"}}, map[string]C.Proxy{"DIRECT": p})
	if err == nil {
		t.Fatal("DIRECT accepted")
	}
	s, _, _, _ := testStable(t)
	if strings.Contains(s.groupID, "IP1") {
		t.Fatal("group alias leaked")
	}
}

func TestStableTLSClassification(t *testing.T) {
	if code, hard := stableError(io.EOF); code != "tls_aborted" || hard {
		t.Fatal("EOF misclassified", code, hard)
	}
	if code, _ := stableError(errors.New("HTTP need auth")); code != "residential_auth" {
		t.Fatal(code)
	}
}
