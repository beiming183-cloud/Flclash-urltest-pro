//go:build go1.21

package outboundgroup

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

const speedSampleBytes int64 = 1 << 20
const speedHourBytes int64 = 8 << 20

func (s *Stable) BusinessAnomaly(route stability.BusinessRoute, reason string) {
	state := s.engine.Snapshot()
	if route.Group != s.groupID || route.Epoch != state.Epoch || !containsStable(state.Active, route.Node) {
		return
	}
	s.mu.Lock()
	if s.cancel == nil || time.Since(s.recheckLast[route.Node]) < 10*time.Second {
		s.mu.Unlock()
		return
	}
	s.recheck[route.Node], s.recheckLast[route.Node] = time.Now(), time.Now()
	s.mu.Unlock()
	s.log("business_recheck", reason, route.Node, false, 0)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Stable) maybeMeasureThroughput(ctx context.Context) {
	state := s.engine.Snapshot()
	now := time.Now()
	if state.Paused || state.PowerSave || len(state.Active) < 2 || !competitionBudgetAvailable(state) {
		return
	}
	s.mu.Lock()
	if s.cancel == nil || ctx.Err() != nil || s.speedBusy || now.Sub(s.lastBusiness) > 2*time.Minute || len(s.scheduled) > 0 || len(s.probing) > 0 {
		s.mu.Unlock()
		return
	}
	if now.Sub(s.speedWindow) >= time.Hour {
		s.speedWindow, s.speedReserved = now, 0
	}
	if now.Sub(s.speedRound) >= 15*time.Minute {
		s.speedRound, s.speedMeasured = now, map[string]bool{}
	}
	if len(s.speedMeasured) >= 2 || s.speedReserved+speedSampleBytes > speedHourBytes {
		s.mu.Unlock()
		return
	}
	id := ""
	for _, active := range state.Active[:min(len(state.Active), 2)] {
		for _, node := range state.Nodes {
			if node.ID == active && stability.CompetitionReady(node) && !s.speedMeasured[active] {
				id = active
				break
			}
		}
		if id != "" {
			break
		}
	}
	if id == "" {
		s.mu.Unlock()
		return
	}
	s.speedBusy, s.speedMeasured[id] = true, true
	s.speedReserved += speedSampleBytes
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); s.speedBusy = false; s.mu.Unlock(); s.wg.Done() }()
		select {
		case s.backgroundSlots <- struct{}{}:
			defer func() { <-s.backgroundSlots }()
		default:
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		transport := &http.Transport{DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if !s.engine.Reserve(false) {
				return nil, errors.New("budget_exhausted")
			}
			m := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
			if err := m.SetRemoteAddress(address); err != nil {
				return nil, err
			}
			return s.byID[id].DialContext(ctx, m)
		}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		mbps, bytes, reliable, loaded, err := measureThroughput(ctx, client, func() bool { return s.engine.Reserve(false) })
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			reason, _ := stableError(err)
			s.log("throughput", reason, id, false, 0)
			return
		}
		if s.engine.ObserveThroughput(id, state.Epoch, mbps, bytes, reliable, loaded) {
			s.log("throughput", "", id, true, loaded)
			if s.engine.Snapshot().Generation != state.Generation {
				s.log("switch", s.engine.Snapshot().Reason, s.engine.Snapshot().Primary, true, 0)
			}
			OnStableRouteChange()
		}
	}()
}

func measureThroughput(ctx context.Context, client *http.Client, reserve func() bool) (float64, int64, bool, time.Duration, error) {
	const control = "https://www.gstatic.com/generate_204"
	head := func(requestCtx context.Context) error {
		if !reserve() {
			return errors.New("budget_exhausted")
		}
		req, err := http.NewRequestWithContext(requestCtx, http.MethodHead, control, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return errors.New("service_failed")
		}
		return nil
	}
	if err := head(ctx); err != nil {
		return 0, 0, false, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if !reserve() {
		return 0, 0, false, 0, errors.New("budget_exhausted")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://speed.cloudflare.com/__down?bytes=1048576", nil)
	if err != nil {
		return 0, 0, false, 0, err
	}
	req.Header.Set("Cache-Control", "no-store")
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, false, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" || resp.ContentLength != speedSampleBytes {
		return 0, 0, false, 0, errors.New("service_failed")
	}
	started := time.Now()
	first := make([]byte, 32<<10)
	bytes, readErr := io.ReadFull(resp.Body, first)
	if readErr != nil {
		return 0, int64(bytes), false, 0, readErr
	}
	type loadedResult struct {
		duration time.Duration
		ended    time.Time
		err      error
	}
	result := make(chan loadedResult, 1)
	go func() {
		start := time.Now()
		err := head(ctx)
		result <- loadedResult{time.Since(start), time.Now(), err}
	}()
	remaining, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, speedSampleBytes-int64(bytes)))
	ended := time.Now()
	total, elapsed := int64(bytes)+remaining, ended.Sub(started)
	loaded := <-result
	if readErr != nil && !errors.Is(readErr, context.DeadlineExceeded) && !errors.Is(readErr, context.Canceled) {
		return 0, total, false, 0, readErr
	}
	if total < 256<<10 || elapsed <= 0 {
		return 0, total, false, 0, errors.New("service_failed")
	}
	underLoad := time.Duration(0)
	if loaded.err == nil && !loaded.ended.After(ended) {
		underLoad = loaded.duration
	}
	return float64(total) * 8 / elapsed.Seconds() / 1e6, total, elapsed >= 500*time.Millisecond && underLoad > 0, underLoad, nil
}
