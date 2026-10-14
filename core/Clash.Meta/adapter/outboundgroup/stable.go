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
		j.ObserveBusiness()
	}
	return sharedJournal, nil
}

type StableOption struct {
	ExpectedExit string            `group:"expected-exit"`
	Endpoints    []string          `group:"residential-endpoints"`
	Airports     map[string]string `group:"relay-airports,omitempty"`
	Preferred    []string          `group:"preferred-candidates,omitempty"`
}

type Stable struct {
	*GroupBase
	engine      *stability.Engine
	journal     *stability.Journal
	groupID     string
	expected    string
	byID        map[string]C.Proxy
	byName      map[string]string
	mu          sync.Mutex
	cancel      context.CancelFunc
	ctx         context.Context
	wg          sync.WaitGroup
	slots       chan struct{}
	probing     map[string]bool
	scheduled   map[string]bool
	wake        chan struct{}
	networkKey  string
	online      bool
	preferred   []string
	requested   string
	fixed       string
	tlsChecking bool
	tlsChecked  map[string]time.Time
	transition  sync.Mutex
	probe       func(context.Context, C.Proxy, string, bool, uint64, func() bool) stability.Sample
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
	s := &Stable{GroupBase: NewGroupBase(GroupBaseOption{Name: option.Name, Type: C.StableFailover, Hidden: option.Hidden, Icon: option.Icon}), journal: j, groupID: j.ID("group:" + option.Name), expected: opt.ExpectedExit, byID: map[string]C.Proxy{}, byName: map[string]string{}, slots: make(chan struct{}, 2), probing: map[string]bool{}, probe: probeStable}
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
	for _, name := range opt.Preferred {
		if s.byName[name] == "" {
			return nil, errors.New("preferred candidate is not owned by the group")
		}
		s.preferred = append(s.preferred, s.byName[name])
	}
	s.scheduled = map[string]bool{}
	s.wake = make(chan struct{}, 1)
	s.tlsChecked = map[string]time.Time{}
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
	state := s.engine.Snapshot()
	visible := append([]string(nil), s.preferred...)
	visible = append(visible, state.Active...)
	for _, node := range state.Nodes {
		if len(s.preferred) > 0 && !containsStable(visible, node.ID) {
			continue
		}
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
	s.mu.Lock()
	if name == "" {
		s.requested, s.fixed = "", ""
		s.engine.ClearManual()
		s.mu.Unlock()
		OnStableRouteChange()
		return nil
	}
	id := s.byName[name]
	if id == "" {
		s.mu.Unlock()
		return errors.New("unknown residential candidate")
	}
	if !s.engine.SetPrimary(s.byName[name]) {
		s.requested, s.fixed = id, name
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default:
		}
		OnStableRouteChange()
		return nil
	}
	s.fixed, s.requested = name, ""
	s.mu.Unlock()
	OnStableRouteChange()
	return nil
}
func (s *Stable) ForceSet(name string) { _ = s.Set(name) }

func (s *Stable) Snapshot() map[string]any {
	state := s.engine.Snapshot()
	s.mu.Lock()
	state.Running = s.cancel != nil
	inFlight := len(s.probing)
	pending := s.requested
	s.mu.Unlock()
	names := map[string]string{}
	for id, p := range s.byID {
		names[id] = p.Name()
	}
	status := "ready"
	if state.Primary == "" {
		status = "validating"
		latest := time.Time{}
		for _, node := range state.Nodes {
			if node.LastError != "" && node.LastCheck.After(latest) {
				latest, status = node.LastCheck, node.LastError
			}
		}
		if !state.Running {
			status = "stopped"
		}
	}
	return map[string]any{"group": s.Name(), "state": state, "names": names, "status": status, "pending": pending, "inFlight": inFlight, "debugRemainingSec": s.journal.DebugRemaining()}
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
	s.mu.Lock()
	fixed := s.fixed
	s.mu.Unlock()
	return json.Marshal(map[string]any{"type": s.Type().String(), "now": s.Now(), "all": all, "hidden": s.Hidden(), "icon": s.Icon(), "testUrl": "https://www.gstatic.com/generate_204", "fixed": fixed})
}

func (s *Stable) log(event, reason, id string, ok bool, latency time.Duration, samples ...stability.Sample) {
	state := s.engine.Snapshot()
	entry := stability.Event{Event: event, Reason: reason, Group: s.groupID, Node: id, OK: ok, LatencyMs: latency.Milliseconds(), Epoch: state.Epoch, Generation: state.Generation}
	if len(samples) > 0 {
		entry.Phase, entry.Service = samples[0].Phase, samples[0].Service
		entry.FirstResponseMs = samples[0].FirstResponse.Milliseconds()
	}
	_ = s.journal.Record(entry)
}

func (s *Stable) DiagnosticState() (string, stability.Snapshot) {
	state := s.engine.Snapshot()
	s.mu.Lock()
	state.Running = s.cancel != nil
	s.mu.Unlock()
	return s.groupID, state
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
	s.tlsChecked = map[string]time.Time{}
	if s.fixed != "" {
		s.requested = s.byName[s.fixed]
	}
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
		_ = s.journal.Store(s.groupID, s.engine.Statistics())
		s.engine.Invalidate()
		s.log("network_changed", "stopped", "", false, 0)
		s.journal.BusinessTick(s.groupID, s.engine.Snapshot())
	}
}
func (s *Stable) Close() error { s.Stop(); return nil }

func (s *Stable) NetworkChanged(key string, online bool) {
	s.mu.Lock()
	unchanged := s.networkKey == key && s.online == online && (s.cancel != nil) == online
	s.networkKey, s.online = key, online
	s.mu.Unlock()
	if unchanged {
		return
	}
	s.Stop()
	s.engine.SwitchNetwork(s.journal.ID("network:" + key))
	if online {
		s.Start()
	}
	s.log("network_changed", "network_changed", "", false, 0)
	OnStableRouteChange()
}

func (s *Stable) Probe(ctx context.Context, id string, verify, elect bool) (stability.Sample, error) {
	return s.probeCandidate(ctx, id, verify, elect, false)
}

func (s *Stable) probeCandidate(ctx context.Context, id string, verify, elect, fault bool) (stability.Sample, error) {
	p := s.byID[id]
	if p == nil {
		return stability.Sample{}, errors.New("unknown stability candidate")
	}
	s.mu.Lock()
	if s.probing[id] {
		s.mu.Unlock()
		return stability.Sample{}, errors.New("candidate probe in progress")
	}
	if s.cancel == nil {
		s.mu.Unlock()
		return stability.Sample{}, errors.New("stability service stopped")
	}
	owner := s.ctx
	s.probing[id] = true
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.probing, id); s.mu.Unlock(); s.wg.Done() }()
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
	timeout := 5 * time.Second
	for _, node := range state.Nodes {
		if node.ID == id && node.Failures > 0 {
			timeout = 5 * time.Second
		}
	}
	if verify {
		timeout = 18 * time.Second
	}
	ctx, cancelProbe := context.WithTimeout(ctx, timeout)
	defer cancelProbe()
	sample := s.probe(ctx, p, s.expected, verify, state.Epoch, func() bool { return s.engine.Reserve(fault) })
	if owner.Err() != nil || sample.Error == "cancelled" {
		return sample, context.Canceled
	}
	if sample.Error == "budget_exhausted" {
		s.log("budget_exhausted", "budget_exhausted", id, false, 0)
		return sample, errors.New("probe budget exhausted")
	}
	s.engine.Record(id, sample, elect)
	if elect {
		s.mu.Lock()
		if s.requested == id && sample.OK && s.engine.SetPrimary(id) {
			s.requested = ""
		}
		s.mu.Unlock()
	}
	if len(sample.TLSInfo) > 0 {
		s.journal.TLS(id, sample.TLSInfo)
	}
	s.log("probe", sample.Error, id, sample.OK, sample.Latency, sample)
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
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	stop := context.AfterFunc(owner, cancel)
	defer stop()
	state := s.engine.Snapshot()
	attempts := s.engine.Attempts()
	if len(attempts) == 0 {
		checked := 0
		for _, node := range state.Nodes {
			if node.Quarantined || time.Now().Before(node.CoolingUntil) {
				continue
			}
			_, _ = s.probeCandidate(ctx, node.ID, true, true, true)
			checked++
			if len(s.engine.Attempts()) > 0 || checked >= 3 {
				break
			}
		}
		attempts = s.engine.Attempts()
		if len(attempts) == 0 {
			return nil, errors.New("no validated residential chain")
		}
	}
	for _, id := range attempts {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !containsStable(s.engine.Attempts(), id) {
			continue
		}
		timeout := 4 * time.Second
		for _, node := range state.Nodes {
			if node.ID == id && node.P95 > 0 {
				timeout = time.Duration(max(node.P95*3, node.FirstResponseP95*1.5))*time.Millisecond + 750*time.Millisecond
				timeout = max(1500*time.Millisecond, min(timeout, 4*time.Second))
			}
		}
		attempt, release := context.WithTimeout(ctx, timeout)
		copyMeta := *metadata
		flow := stability.BusinessFromContext(ctx)
		flow.DialAttempt(stability.BusinessRoute{Group: s.groupID, Node: id, Protocol: s.byID[id].Type().String(), Epoch: state.Epoch, Generation: state.Generation}, s.byID[id].Name())
		started := time.Now()
		conn, err := s.byID[id].DialContext(attempt, &copyMeta)
		observedError := err
		if conn == nil && observedError == nil {
			observedError = errors.New("empty connection")
		}
		flow.DialResult(observedError, time.Since(started))
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
