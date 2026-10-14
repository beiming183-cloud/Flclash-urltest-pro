package outboundgroup

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func (s *Stable) checkTargetTLS(ctx context.Context) {
	state := s.engine.Snapshot()
	if len(state.Active) == 0 || !competitionBudgetAvailable(state) {
		return
	}
	s.mu.Lock()
	if s.cancel == nil || s.tlsChecking {
		s.mu.Unlock()
		return
	}
	id := ""
	for _, candidate := range state.Active {
		if time.Since(s.tlsChecked[candidate]) >= 9*time.Minute {
			id = candidate
			break
		}
	}
	if id == "" {
		s.mu.Unlock()
		return
	}
	s.tlsChecking = true
	s.tlsChecked[id] = time.Now()
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); s.tlsChecking = false; s.mu.Unlock(); s.wg.Done() }()
		infos := []stability.TLSInfo{}
		for _, address := range []string{"chatgpt.com:443", "android.chat.openai.com:443"} {
			if !s.engine.Reserve(false) {
				return
			}
			child, cancel := context.WithTimeout(ctx, 5*time.Second)
			meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
			meta.SetRemoteAddress(address)
			conn, err := s.byID[id].DialContext(child, meta)
			service := "chatgpt"
			if strings.HasPrefix(address, "android.") {
				service = "android"
			}
			if err == nil {
				host, _, _ := net.SplitHostPort(address)
				tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
				err = tc.HandshakeContext(child)
				info := stableTLSInfo(service, tc.ConnectionState())
				info.CheckedAt, info.Epoch = time.Now(), state.Epoch
				infos = append(infos, info)
				tc.Close()
			}
			cancel()
			if ctx.Err() != nil || s.engine.Snapshot().Epoch != state.Epoch {
				return
			}
			reason := ""
			if err != nil {
				reason, _ = stableError(err)
				if reason == "relay_dial_timeout" {
					reason = "tls_handshake_timeout"
				}
			}
			_ = s.journal.Record(stability.Event{Event: "tls_check", Reason: reason, Group: s.groupID, Node: id, Epoch: state.Epoch, Generation: state.Generation, OK: err == nil, Phase: "tls", Service: service})
		}
		s.journal.TLS(id, infos)
	}()
}
