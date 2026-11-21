package outboundgroup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func recoveryAcceptanceStable(t *testing.T, count int, validation time.Duration) (*Stable, *atomic.Int64) {
	t.Helper()
	s := newColdStable(t)
	goodProxy := s.byID[s.byName["chain-a"]]
	s.byID, s.byName = map[string]C.Proxy{}, map[string]string{}
	candidates := []stability.Candidate{}
	for i := 0; i < count; i++ {
		id, name := fmt.Sprintf("%024x", i+100), fmt.Sprintf("acceptance-%04d", i)
		candidates = append(candidates, stability.Candidate{ID: id, Relay: fmt.Sprintf("%024x", i+2000), Airport: fmt.Sprintf("%024x", i%3+5000)})
		s.byID[id] = adapter.NewProxy(outbound.NewBase(outbound.BaseOption{Name: name, Type: C.Http}))
		s.byName[name] = id
	}
	var err error
	s.engine, err = stability.New(candidates, nil)
	if err != nil {
		t.Fatal(err)
	}
	good := s.engine.StartupCandidates()[0]
	s.byID[good] = goodProxy
	s.byName[goodProxy.Name()] = good
	s.ctx, s.cancel = context.WithCancel(context.Background())
	started := &atomic.Int64{}
	s.probe = func(ctx context.Context, p C.Proxy, _ string, _ bool, epoch uint64, reserve func() bool) stability.Sample {
		if !reserve() || !reserve() {
			return stability.Sample{Epoch: epoch, Error: "budget_exhausted"}
		}
		search, _ := ctx.Value(stableSearchKey{}).(bool)
		if !search && p.Name() == goodProxy.Name() {
			started.CompareAndSwap(0, time.Now().UnixNano())
			select {
			case <-time.After(validation):
				return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true}
			case <-ctx.Done():
				return stability.Sample{Epoch: epoch, Error: "cancelled"}
			}
		}
		<-ctx.Done()
		return stability.Sample{Epoch: epoch, Error: "exit_query_timeout"}
	}
	return s, started
}

func TestQualificationWaitTimeoutHasDistinctDiagnosticReason(t *testing.T) {
	s := newColdStable(t)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.probe = func(ctx context.Context, _ C.Proxy, _ string, _ bool, epoch uint64, _ func() bool) stability.Sample {
		<-ctx.Done()
		return stability.Sample{Epoch: epoch, Error: "cancelled"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	meta.SetRemoteAddress("example.com:443")
	if conn, err := s.DialContext(ctx, meta); err == nil {
		conn.Close()
		t.Fatal("unqualified route became usable")
	}
	data, err := s.journal.Export()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range archive.File {
		if file.Name != "events.jsonl" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(reader)
		for decoder.More() {
			var event stability.Event
			if decoder.Decode(&event) != nil {
				break
			}
			if event.Event == "dial_final" && event.Group == s.groupID {
				if event.Reason != "qualification_wait_timeout" {
					t.Fatal("qualification waiting mislabeled as a network dial", event.Reason)
				}
				found = true
			}
		}
		reader.Close()
	}
	if !found {
		t.Fatal("final qualification timeout missing from bounded diagnostics")
	}
}

func TestRecoveryConfirmationIndependentOfCandidateCount(t *testing.T) {
	for _, count := range []int{16, 180, 267, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			if count > stability.MaxCandidates {
				t.Skip("stress overlay raises only the admission limit for this case")
			}
			s, started := recoveryAcceptanceStable(t, count, 100*time.Millisecond)
			began := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.awaitStartup(ctx, s.ctx); err != nil {
				t.Fatal("complete confirmation waited behind cold search", err)
			}
			delay := time.Duration(started.Load() - began.UnixNano())
			if started.Load() == 0 || delay > 500*time.Millisecond {
				t.Fatal("critical confirmation dispatch exceeded 500ms", delay)
			}
			t.Logf("candidates=%d dispatch=%s qualified=%s", count, delay, time.Since(began))
		})
	}
}

func TestKnownEightSecondQualificationRecoversTwoAccountsWithinTenSeconds(t *testing.T) {
	for _, count := range []int{16, 180, 267, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			if count > stability.MaxCandidates {
				t.Skip("stress overlay raises only the admission limit for this case")
			}
			a, aStart := recoveryAcceptanceStable(t, count, 7800*time.Millisecond)
			b, bStart := recoveryAcceptanceStable(t, count, 7800*time.Millisecond)
			type result struct {
				elapsed, dispatch time.Duration
				err               error
			}
			results := make(chan result, 2)
			for _, entry := range []struct {
				s       *Stable
				started *atomic.Int64
			}{{a, aStart}, {b, bStart}} {
				go func(s *Stable, started *atomic.Int64) {
					began := time.Now()
					meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
					meta.SetRemoteAddress("example.com:443")
					ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
					defer cancel()
					conn, err := s.DialContext(ctx, meta)
					if conn != nil {
						conn.Close()
					}
					results <- result{time.Since(began), time.Duration(started.Load() - began.UnixNano()), err}
				}(entry.s, entry.started)
			}
			for i := 0; i < 2; i++ {
				r := <-results
				if r.err != nil || r.elapsed > 10*time.Second || r.dispatch > 500*time.Millisecond {
					t.Fatal("known valid route missed bounded two-account recovery", r)
				}
				t.Logf("candidates=%d account=%d dispatch=%s endToEnd=%s", count, i+1, r.dispatch, r.elapsed)
			}
		})
	}
}

type recoveryTimedProxy struct {
	C.Proxy
	started chan time.Time
	ended   chan time.Time
}

func (p *recoveryTimedProxy) DialContext(ctx context.Context, meta *C.Metadata) (C.Conn, error) {
	p.started <- time.Now()
	conn, err := p.Proxy.DialContext(ctx, meta)
	p.ended <- time.Now()
	return conn, err
}

func TestQualifiedBackupDispatchAfterFailedConnectUnderThreeHundredMilliseconds(t *testing.T) {
	s, a, _, _ := testStable(t)
	a.status.Store(502)
	ids := s.engine.Attempts()
	first := &recoveryTimedProxy{Proxy: s.byID[ids[0]], started: make(chan time.Time, 1), ended: make(chan time.Time, 1)}
	backup := &recoveryTimedProxy{Proxy: s.byID[ids[1]], started: make(chan time.Time, 1), ended: make(chan time.Time, 1)}
	s.byID[ids[0]], s.byID[ids[1]] = first, backup
	meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
	meta.SetRemoteAddress("example.com:443")
	conn, err := s.DialContext(context.Background(), meta)
	if err != nil {
		t.Fatal("qualified backup failed", err)
	}
	conn.Close()
	failedAt, backupAt := <-first.ended, <-backup.started
	if delay := backupAt.Sub(failedAt); delay > 300*time.Millisecond {
		t.Fatal("failed CONNECT delayed the qualified standby", delay)
	} else {
		t.Logf("qualified backup dispatch after failure=%s", delay)
	}
}

func TestMobileFirstBusinessWith267Candidates(t *testing.T) {
	for _, historicalWinner := range []bool{true, false} {
		t.Run(fmt.Sprint("history-", historicalWinner), func(t *testing.T) {
			s, _ := recoveryAcceptanceStable(t, 267, time.Second)
			candidates := make([]stability.Candidate, 0, 267)
			for _, n := range s.engine.Snapshot().Nodes {
				candidates = append(candidates, n.Candidate)
			}
			clock := time.Now().Add(-30 * time.Hour)
			e, err := stability.New(candidates, func() time.Time { return clock })
			if err != nil {
				t.Fatal(err)
			}
			oldOrder := s.engine.StartupCandidates()
			goodProxy := s.byID[oldOrder[0]]
			s.engine = e
			past := candidates[200].ID
			e.Record(past, stability.Sample{Epoch: e.Snapshot().Epoch, OK: true, TLS: true, Exit: true, Latency: time.Second}, true)
			clock = clock.Add(30 * time.Hour)
			e.Invalidate()
			wave := e.StartupCandidates()
			if wave[0] != past || len(e.Attempts()) != 0 {
				t.Fatal("history must change probe order only")
			}
			winner := past
			if !historicalWinner {
				for _, c := range candidates {
					if c.Airport != candidates[200].Airport {
						winner = c.ID
						break
					}
				}
			}
			s.byID[oldOrder[0]], s.byID[winner] = s.byID[winner], goodProxy
			s.probe = func(ctx context.Context, p C.Proxy, _ string, verify bool, epoch uint64, _ func() bool) stability.Sample {
				if p != goodProxy {
					<-ctx.Done()
					return stability.Sample{Epoch: epoch, Error: "exit_query_timeout"}
				}
				if !verify {
					t.Error("missing exit check")
				}
				select {
				case <-time.After(1300 * time.Millisecond):
					return stability.Sample{Epoch: epoch, OK: true, TLS: true, Exit: true, Latency: 1300 * time.Millisecond}
				case <-ctx.Done():
					return stability.Sample{Epoch: epoch, Error: "cancelled"}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			meta := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
			meta.SetRemoteAddress("example.com:443")
			started := time.Now()
			conn, err := s.DialContext(ctx, meta)
			if err != nil {
				t.Fatal("first business connection", err)
			}
			conn.Close()
			if e.Snapshot().Primary != winner {
				t.Fatal("unexpected primary", e.Snapshot().Primary, winner)
			}
			t.Logf("one usable of 267, first business connection %v", time.Since(started))
		})
	}
}
