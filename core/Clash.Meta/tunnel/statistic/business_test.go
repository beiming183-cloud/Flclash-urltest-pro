package statistic

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

type businessTestConn struct {
	N.ExtendedConn
	chain C.Chain
}

func (c *businessTestConn) Chains() C.Chain                 { return c.chain }
func (c *businessTestConn) ProviderChains() C.Chain         { return nil }
func (c *businessTestConn) AppendToChains(a C.ProxyAdapter) { c.chain = append(c.chain, a.Name()) }
func (c *businessTestConn) RemoteDestination() string       { return "test" }

func businessPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	a, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	deadline := time.Now().Add(5 * time.Second)
	a.SetDeadline(deadline)
	b.SetDeadline(deadline)
	return a.(*net.TCPConn), b.(*net.TCPConn)
}

func businessZIP(t *testing.T, j *stability.Journal) map[string][]byte {
	t.Helper()
	data, err := j.Export()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		r, _ := f.Open()
		files[f.Name], err = io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestBusinessRelayCountsLiveBytesAndObservesReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "upstream_reset"}[reset], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(1000)
			j, err := stability.OpenJournal(t.TempDir(), func() time.Time { return time.Unix(clock.Load(), 0) })
			if err != nil {
				t.Fatal(err)
			}
			j.ObserveBusiness()
			if stability.BeginBusiness("chatgpt.com", "", true) != nil {
				t.Fatal("active probe was recorded as business flow")
			}
			flow := stability.BeginBusiness("android.chat.openai.com", "com.openai.chatgpt", false)
			flow.Open([]string{"IP1-一分机场-013-S5", "IP1"}, 0)
			client, inbound := businessPair(t)
			outbound, server := businessPair(t)
			tracked := NewTCPTracker(&businessTestConn{ExtendedConn: N.NewExtendedConn(outbound), chain: C.Chain{"IP1"}}, DefaultManager, &C.Metadata{}, nil, 0, 0, false, flow)
			done := make(chan struct{})
			go func() { N.RelayObserved(inbound, tracked, flow.CopyEnded); flow.Finish(); close(done) }()
			payload := bytes.Repeat([]byte("PRIVATE-CHAT-BODY"), 64)
			if _, err = client.Write(payload); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(payload))
			if _, err = io.ReadFull(server, got); err != nil || !bytes.Equal(got, payload) {
				t.Fatal("forwarding changed", err)
			}
			deadline := time.Now().Add(time.Second)
			var files map[string][]byte
			for {
				files = businessZIP(t, j)
				var active []stability.BusinessEvent
				json.Unmarshal(files["chatgpt-active.json"], &active)
				if len(active) == 1 && active[0].Upload == int64(len(payload)) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("live fast-path upload missing", string(files["chatgpt-active.json"]))
				}
				time.Sleep(time.Millisecond)
			}
			clock.Store(1009)
			j.BusinessTick("", stability.Snapshot{})
			if !bytes.Contains(businessZIP(t, j)["chatgpt-connections.jsonl"], []byte("no_downstream_observed")) {
				t.Fatal("silent upstream unobserved")
			}
			if reset {
				server.SetLinger(0)
				server.Close()
			} else {
				if _, err = server.Write(payload[:73]); err != nil {
					t.Fatal(err)
				}
				got = make([]byte, 73)
				if _, err = io.ReadFull(client, got); err != nil || !bytes.Equal(got, payload[:73]) {
					t.Fatal("reply changed", err)
				}
				server.CloseWrite()
				client.CloseWrite()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("relay shutdown changed")
			}
			files = businessZIP(t, j)
			all := string(files["chatgpt-connections.jsonl"])
			if !bytes.Contains(files["chatgpt-active.json"], []byte("[]")) {
				t.Fatal("completed flow remained active")
			}
			if !bytes.Contains(files["chatgpt-connections.jsonl"], []byte("copy_ended")) {
				t.Fatal("copy termination missing")
			}
			if reset && !bytes.Contains(files["chatgpt-connections.jsonl"], []byte("connection_reset")) {
				t.Fatal("reset missing", all)
			}
			if !reset && (!bytes.Contains(files["chatgpt-connections.jsonl"], []byte(`"downloadBytes":73`)) || !bytes.Contains(files["chatgpt-connections.jsonl"], []byte("downstream_resumed"))) {
				t.Fatal("reply recovery missing", all)
			}
			for name, body := range files {
				if bytes.Contains(body, []byte("PRIVATE-CHAT-BODY")) {
					t.Fatal("payload leaked", name)
				}
			}
		})
	}
}
