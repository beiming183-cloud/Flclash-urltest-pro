package outboundgroup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func TestBusinessActualStableFailoverIsCorrelated(t *testing.T) {
	s, a, b, _ := testStable(t)
	a.status.Store(407)
	flow := stability.BeginBusiness("android.chat.openai.com", "com.openai.chatgpt", false)
	if flow == nil {
		t.Fatal("native journal did not register observation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = stability.WithBusiness(ctx, flow)
	meta := &C.Metadata{NetWork: C.TCP}
	meta.SetRemoteAddress("android.chat.openai.com:443")
	conn, err := s.DialContext(ctx, meta)
	if err != nil {
		t.Fatal(err)
	}
	flow.Open(conn.Chains(), 0)
	conn.Close()
	flow.Finish()
	if a.payload.Load() != 0 || b.payload.Load() != 0 {
		t.Fatal("observer sent application data")
	}
	data, err := s.journal.Export()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var events []stability.BusinessEvent
	for _, file := range z.File {
		r, _ := file.Open()
		body, _ := io.ReadAll(r)
		r.Close()
		for _, secret := range []string{"test-password", "same-account", "198.51.100.10"} {
			if strings.Contains(string(body), secret) {
				t.Fatal("secret leaked", secret)
			}
		}
		if file.Name == "chatgpt-connections.jsonl" {
			for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
				var e stability.BusinessEvent
				if json.Unmarshal(line, &e) != nil {
					t.Fatal("invalid event")
				}
				events = append(events, e)
			}
		}
	}
	failed, ready := 0, 0
	for _, e := range events {
		if e.Route.Group != "" && e.Route.Group != s.groupID {
			t.Fatal("wrong group")
		}
		if e.Event == "proxy_dial_failed" {
			failed++
			if e.Route.Node != s.byName["chain-a"] {
				t.Fatal("failure associated with wrong candidate")
			}
		}
		if e.Event == "proxy_dial_ready" {
			ready++
			if e.Route.Node != s.byName["chain-b"] {
				t.Fatal("success associated with wrong candidate")
			}
		}
	}
	if failed != 1 || ready != 1 {
		t.Fatal("missing actual failover observations", failed, ready)
	}
}
