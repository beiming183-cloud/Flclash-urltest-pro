//go:build go1.21

package outboundgroup

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/common/utils"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

var OnStableRouteChange = func() {}
var journalMu sync.Mutex
var sharedJournal *stability.Journal
var journalHome string

func StabilityJournal() (*stability.Journal, error) {
	journalMu.Lock()
	defer journalMu.Unlock()
	home := C.Path.HomeDir()
	if sharedJournal == nil || journalHome != home {
		j, err := stability.OpenJournal(filepath.Join(home, "stability-diagnostics"), nil)
		if err != nil {
			return nil, err
		}
		sharedJournal = j
		journalHome = home
	}
	return sharedJournal, nil
}

type StableOption struct {
	ExpectedExit string            `group:"expected-exit"`
	Endpoints    []string          `group:"residential-endpoints"`
	Airports     map[string]string `group:"relay-airports,omitempty"`
}

type Stable struct {
	*GroupBase
	engine     *stability.Engine
	journal    *stability.Journal
	groupID    string
	expected   string
	byID       map[string]C.Proxy
	byName     map[string]string
	mu         sync.Mutex
	cancel     context.CancelFunc
	ctx        context.Context
	wg         sync.WaitGroup
	slots      chan struct{}
	transition sync.Mutex
	probe      func(context.Context, C.Proxy, string, bool, uint64, func() bool) stability.Sample
}

func NewStable(option GroupCommonOption, opt StableOption, proxyMap map[string]C.Proxy) (*Stable, error) {
	if _, err := netip.ParseAddr(opt.ExpectedExit); err != nil {
		return nil, errors.New("stable-failover requires a valid expected-exit")
	}
	if len(option.Proxies) == 0 || len(option.Proxies) > stability.MaxCandidates || len(option.Use) > 0 || option.IncludeAll || option.IncludeAllProxies || option.IncludeAllProviders || option.Filter != "" || option.ExcludeFilter != "" || option.ExcludeType != "" {
		return nil, errors.New("stable-failover requires explicit residential candidates (1–512)")
	}
	j, err := StabilityJournal()
	if err != nil {
		return nil, err
	}
	endpoints := map[string]bool{}
	for _, endpoint := range opt.Endpoints {
		endpoints[endpoint] = true
	}
	if len(endpoints) == 0 {
		return nil, errors.New("stable-failover requires residential-endpoints")
	}
	s := &Stable{GroupBase: NewGroupBase(GroupBaseOption{Name: option.Name, Type: C.StableFailover, Hidden: option.Hidden, Icon: option.Icon}), journal: j, groupID: j.ID("group:" + option.Name), expected: opt.ExpectedExit, byID: map[string]C.Proxy{}, byName: map[string]string{}, slots: make(chan struct{}, 2), probe: probeStable}
	candidates := []stability.Candidate{}
	account := ""
	for _, name := range option.Proxies {
		p := proxyMap[name]
		if p == nil || name == option.Name {
			return nil, errors.New("invalid stable-failover candidate")
		}
		info, ok := outbound.ResidentialIdentity(p.Adapter(), j.ID)
		if !ok || !endpoints[info.Endpoint] || info.UnsafeTLS || !strings.HasPrefix(info.Relay, "RF2-Relay-") {
			return nil, errors.New("candidate must be a verified HTTP/SOCKS5 residential endpoint through a specific RF2-Relay")
		}
		if account != "" && account != info.Account {
			return nil, errors.New("mixed residential identities")
		}
		account = info.Account
		relay := proxyMap[info.Relay]
		if relay == nil || relay.Name() == name || relay.ProxyInfo().DialerProxy != "" || relay.Type() == C.Direct || relay.Type() == C.Compatible || relay.Type() == C.Reject || relay.Type() == C.Pass {
			return nil, errors.New("invalid or recursive stable relay")
		}
		if _, group := relay.Adapter().(ProxyGroup); group {
			return nil, errors.New("stable relay cannot be an automatic group")
		}
		id := j.ID(option.Name + "\x00" + opt.ExpectedExit + "\x00" + info.Endpoint + "\x00" + account + "\x00" + info.Relay + "\x00" + p.Type().String())
		airport := opt.Airports[info.Relay]
		if airport == "" {
			airport = info.Relay
		}
		candidates = append(candidates, stability.Candidate{ID: id, Relay: j.ID(info.Relay), Airport: j.ID(airport)})
		s.byID[id] = p
		s.byName[name] = id
	}
	s.engine, err = stability.New(candidates, nil)
	if err == nil {
		s.engine.Restore(j.Statistics(s.groupID))
	}
	return s, err
}

func (s *Stable) Now() string {
	state := s.engine.Snapshot()
	if p := s.byID[state.Primary]; p != nil {
		return p.Name()
	}
	return ""
}
func (s *Stable) Proxies() []C.Proxy {
	ps := []C.Proxy{}
	for _, node := range s.engine.Snapshot().Nodes {
		ps = append(ps, s.byID[node.ID])
	}
	return ps
}
func (s *Stable) Providers() []P.ProxyProvider     { return nil }
func (s *Stable) Touch()                           {}
func (s *Stable) Unwrap(*C.Metadata, bool) C.Proxy { return s.byID[s.engine.Snapshot().Primary] }
func (s *Stable) SupportUDP() bool                 { return false }
func (s *Stable) ListenPacketContext(context.Context, *C.Metadata) (C.PacketConn, error) {
	return nil, C.ErrNotSupport
}
func (s *Stable) Set(name string) error {
	if !s.engine.SetPrimary(s.byName[name]) {
		return errors.New("candidate requires current TLS and exit validation")
	}
	OnStableRouteChange()
	return nil
}
func (s *Stable) ForceSet(name string) { _ = s.Set(name) }

func (s *Stable) Snapshot() map[string]any {
	state := s.engine.Snapshot()
	s.mu.Lock()
	state.Running = s.cancel != nil
	s.mu.Unlock()
	names := map[string]string{}
	for id, p := range s.byID {
		names[id] = p.Name()
	}
	return map[string]any{"group": s.Name(), "state": state, "names": names, "debugRemainingSec": s.journal.DebugRemaining()}
}

func (s *Stable) Configure(paused, powerSave bool) {
	s.engine.Configure(paused, powerSave)
	OnStableRouteChange()
}

func (s *Stable) MarshalJSON() ([]byte, error) {
	all := []string{}
	for _, p := range s.Proxies() {
		all = append(all, p.Name())
	}
	return json.Marshal(map[string]any{"type": s.Type().String(), "now": s.Now(), "all": all, "hidden": s.Hidden(), "icon": s.Icon(), "testUrl": "https://www.gstatic.com/generate_204", "fixed": ""})
}

func (s *Stable) log(event, reason, id string, ok bool, latency time.Duration) {
	state := s.engine.Snapshot()
	_ = s.journal.Record(stability.Event{Event: event, Reason: reason, Group: s.groupID, Node: id, OK: ok, LatencyMs: latency.Milliseconds(), Epoch: state.Epoch, Generation: state.Generation})
}

func (s *Stable) Start() {
	s.transition.Lock()
	defer s.transition.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	ctx := s.ctx
	s.engine.Invalidate()
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.schedule(ctx) }()
}

func (s *Stable) Stop() {
	s.transition.Lock()
	defer s.transition.Unlock()
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		s.wg.Wait()
		_ = s.journal.Store(s.groupID, s.engine.Snapshot())
		s.engine.Invalidate()
		s.log("network_changed", "stopped", "", false, 0)
	}
}
func (s *Stable) Close() error { s.Stop(); return nil }

func (s *Stable) NetworkChanged() {
	s.Stop()
	s.Start()
	s.log("network_changed", "network_changed", "", false, 0)
	OnStableRouteChange()
}

func (s *Stable) schedule(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastExplore := time.Time{}
	coldIndex := 0
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			state := s.engine.Snapshot()
			task := ""
			verify := false
			for _, id := range state.Active {
				for _, node := range state.Nodes {
					if node.ID != id {
						continue
					}
					interval := 20 * time.Second
					if id == state.Primary {
						interval = 10 * time.Second
					}
					if state.PowerSave {
						interval *= 3
					}
					if now.Sub(node.LastCheck) >= interval {
						task = id
						break
					}
				}
				if task != "" {
					break
				}
			}
			if task == "" {
				for i, node := range state.Nodes {
					if i >= 12 && (node.Streak == 0 || now.Sub(node.LastCheck) > 5*time.Minute) {
						continue
					}
					if (node.Streak < 3 || len(state.Active) == 0) && !node.Quarantined && !now.Before(node.CoolingUntil) && now.Sub(node.LastCheck) >= 5*time.Second {
						task = node.ID
						break
					}
				}
			}
			if task == "" && now.Sub(lastExplore) >= time.Minute {
				for n := 0; n < len(state.Nodes); n++ {
					node := state.Nodes[coldIndex%len(state.Nodes)]
					coldIndex++
					if node.Quarantined || now.Before(node.CoolingUntil) {
						continue
					}
					task = node.ID
					lastExplore = now
					break
				}
			}
			if task == "" {
				continue
			}
			for _, node := range state.Nodes {
				if node.ID == task {
					verify = node.TLSAt.IsZero() || now.Sub(node.TLSAt) >= 9*time.Minute || node.ExitAt.IsZero() || now.Sub(node.ExitAt) >= 29*time.Minute
				}
			}
			_, _ = s.Probe(ctx, task, verify, true)
		}
	}
}

func (s *Stable) Probe(ctx context.Context, id string, verify, elect bool) (stability.Sample, error) {
	p := s.byID[id]
	if p == nil {
		return stability.Sample{}, errors.New("unknown stability candidate")
	}
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return stability.Sample{}, errors.New("stability service stopped")
	}
	owner := s.ctx
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(owner, cancel)
	defer stop()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return stability.Sample{}, ctx.Err()
	}
	state := s.engine.Snapshot()
	timeout := 2500 * time.Millisecond
	if verify {
		timeout = 10 * time.Second
	}
	ctx, cancelProbe := context.WithTimeout(ctx, timeout)
	defer cancelProbe()
	sample := s.probe(ctx, p, s.expected, verify, state.Epoch, func() bool { return s.engine.Reserve(false) })
	if owner.Err() != nil || sample.Error == "cancelled" {
		return sample, context.Canceled
	}
	s.engine.Record(id, sample, elect)
	s.log("probe", sample.Error, id, sample.OK, sample.Latency)
	if elect && s.engine.Snapshot().Generation != state.Generation {
		s.log("switch", s.engine.Snapshot().Reason, s.engine.Snapshot().Primary, true, 0)
		OnStableRouteChange()
	}
	return sample, nil
}

func (s *Stable) DialContext(ctx context.Context, metadata *C.Metadata) (C.Conn, error) {
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return nil, errors.New("stability service stopped")
	}
	owner := s.ctx
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 7500*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(owner, cancel)
	defer stop()
	state := s.engine.Snapshot()
	attempts := s.engine.Attempts()
	if len(attempts) == 0 {
		return nil, errors.New("no validated residential chain")
	}
	for _, id := range attempts {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !containsStable(s.engine.Attempts(), id) {
			continue
		}
		timeout := 2500 * time.Millisecond
		for _, node := range state.Nodes {
			if node.ID == id && node.P95 > 0 {
				timeout = time.Duration(node.P95*3) * time.Millisecond
				timeout = max(800*time.Millisecond, min(timeout, 2500*time.Millisecond))
			}
		}
		attempt, release := context.WithTimeout(ctx, timeout)
		copyMeta := *metadata
		conn, err := s.byID[id].DialContext(attempt, &copyMeta)
		release()
		if err == nil && conn != nil {
			if ctx.Err() != nil || !s.engine.PromoteDial(id, state.Epoch) {
				conn.Close()
				return nil, errors.New("stale residential establishment")
			}
			conn.AppendToChains(s)
			if state.Primary != id {
				s.log("switch", "dial_failover", id, true, 0)
				OnStableRouteChange()
			}
			return conn, nil
		}
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			err = errors.New("empty connection")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		reason, hard := stableError(err)
		s.engine.Record(id, stability.Sample{Epoch: state.Epoch, Error: reason, Hard: hard}, true)
		s.log("probe", reason, id, false, 0)
		OnStableRouteChange()
	}
	return nil, errors.New("no available residential chain")
}

func containsStable(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (s *Stable) URLTest(ctx context.Context, _ string, _ utils.IntRanges[uint16]) (map[string]uint16, error) {
	result := map[string]uint16{}
	ids := s.engine.Snapshot().Active
	for _, id := range ids {
		sample, err := s.Probe(ctx, id, false, false)
		if err == nil && sample.OK {
			result[s.byID[id].Name()] = uint16(min(sample.Latency.Milliseconds(), 65535))
		}
	}
	return result, nil
}

var _ ProxyGroup = (*Stable)(nil)
var _ SelectAble = (*Stable)(nil)
