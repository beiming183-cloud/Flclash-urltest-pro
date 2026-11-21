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
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/common/utils"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

var OnStableRouteChange = func() {}
var startupProbeSequence atomic.Uint64
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
	networkTransition    sync.Mutex
	routeNotificationMu  sync.Mutex
	routeNotificationKey string
	*GroupBase
	engine                *stability.Engine
	journal               *stability.Journal
	groupID               string
	reloadKey             string
	handoff               *stableHandoff
	expected              string
	byID                  map[string]C.Proxy
	byName                map[string]string
	mu                    sync.Mutex
	cancel                context.CancelFunc
	ctx                   context.Context
	wg                    sync.WaitGroup
	slots                 chan struct{}
	backgroundSlots       chan struct{}
	hedgeSlots            chan struct{}
	targetFailures        map[string]targetFailure
	recoveryNodes         map[string]recoveryNode
	startupScheduling     bool
	recoveryActive        bool
	recoveryHadPrimary    bool
	startupTried          map[string]bool
	recoveryNext          time.Time
	recoveryFailures      int
	probing               map[string]bool
	scheduled             map[string]bool
	quickScheduled        map[string]bool
	confirmationScheduled map[string]bool
	wake                  chan struct{}
	ready                 chan struct{}
	networkKey            string
	online                bool
	preferred             []string
	requested             string
	fixed                 string
	tlsChecking           bool
	tlsChecked            map[string]time.Time
	recheck               map[string]time.Time
	recheckLast           map[string]time.Time
	lastBusiness          time.Time
	speedBusy             bool
	speedRound            time.Time
	speedMeasured         map[string]bool
	speedWindow           time.Time
	speedReserved         int64
	transition            sync.Mutex
	probe                 func(context.Context, C.Proxy, string, bool, uint64, func() bool) stability.Sample
	probeTraffic          C.ProbeTraffic
	probeWindow           time.Time
	probeWindowSent       int64
	probeWindowReceived   int64
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
	s := &Stable{GroupBase: NewGroupBase(GroupBaseOption{Name: option.Name, Type: C.StableFailover, Hidden: option.Hidden, Icon: option.Icon}), journal: j, groupID: j.ID("group:" + option.Name), expected: opt.ExpectedExit, byID: map[string]C.Proxy{}, byName: map[string]string{}, slots: make(chan struct{}, 8), backgroundSlots: make(chan struct{}, 2), hedgeSlots: make(chan struct{}, 2), startupTried: map[string]bool{}, probing: map[string]bool{}, probe: probeStable}
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
		candidates = append(candidates, stability.Candidate{ID: id, Relay: j.ID(stableRelayFailureDomain(info.Relay, relay.Addr())), Airport: j.ID(airport)})
		s.byID[id] = p
		s.byName[name] = id
	}
	s.reloadKey = stableBinding(j, option.Name, opt.ExpectedExit, account, endpoints)
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
	s.recheck, s.recheckLast = map[string]time.Time{}, map[string]time.Time{}
	s.speedMeasured = map[string]bool{}
	if err == nil {
		s.engine.Restore(j.Statistics(s.groupID))
		s.engine.SetSelectionObserver(func(event stability.SelectionEvent, epoch, generation uint64) {
			if event.Kind == "empty_enter" || event.Kind == "empty_exit" {
				_ = j.Record(stability.Event{Event: event.Kind, Reason: event.Reason, Group: s.groupID, Node: event.Node, Epoch: epoch, Generation: generation, OK: event.Kind == "empty_exit"})
			}
			if event.Kind == "primary" && event.Node != event.Previous {
				_ = j.Record(stability.Event{Event: "primary_changed", Reason: event.Reason, Group: s.groupID, Node: event.Node, Previous: event.Previous, Epoch: epoch, Generation: generation, OK: true})
			}
		})
	}
	if err == nil {
		stableCoordinationEnabled.Store(true)
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
	for _, node := range state.Nodes {
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
		s.notifyRouteChange()
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
		s.notifyRouteChange()
		return nil
	}
	s.fixed, s.requested = name, ""
	s.mu.Unlock()
	s.notifyRouteChange()
	return nil
}
func (s *Stable) ForceSet(name string) { _ = s.Set(name) }

func (s *Stable) snapshot() map[string]any {
	state := s.engine.Snapshot()
	s.mu.Lock()
	state.Running = s.cancel != nil
	inFlight := len(s.probing)
	pending := s.requested
	speedReserved := s.speedReserved
	recoveryWait := time.Until(s.recoveryNext).Milliseconds()
	if recoveryWait < 0 {
		recoveryWait = 0
	}
	recoveryFailures := s.recoveryFailures
	searchTried := len(s.startupTried)
	s.mu.Unlock()
	names := map[string]string{}
	s.journal.ObserveMinute(s.groupID, state)
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
	return map[string]any{"group": s.Name(), "expectedExit": s.expected, "state": state, "names": names, "status": status, "pending": pending, "inFlight": inFlight, "debugRemainingSec": s.journal.DebugRemaining(), "speedPayloadReservedBytes": speedReserved, "speedPayloadHourLimitBytes": speedHourBytes, "scheduler": schedulerDiagnostics(state, time.Now()), "recoveryBackoffMs": recoveryWait, "recoveryFailureCount": recoveryFailures, "availabilitySearch": map[string]any{"active": len(state.Active) == 0 && state.Running, "tried": searchTried, "total": len(state.Nodes), "parallelLimit": 8, "firstPassDeadlineMs": stableSearchTimeout.Milliseconds()}}
}

func (s *Stable) Configure(paused, powerSave bool) {
	s.engine.Configure(paused, powerSave)
	s.notifyRouteChange()
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
	state.Scheduler = schedulerDiagnostics(state, time.Now())
	return s.groupID, state
}

func (s *Stable) Start() { s.start("service_start") }

func (s *Stable) start(reason string) {
	s.transition.Lock()
	defer s.transition.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	if s.journal != nil {
		_ = s.journal.Record(stability.Event{Event: "startup_begin", Reason: reason, Group: s.groupID, OK: true})
	}
	ordinaryRecoveryStart.Store(time.Now().UnixNano())
	s.ctx, s.cancel = context.WithCancel(context.Background())
	ctx := s.ctx
	s.engine.Invalidate()
	s.startupTried = map[string]bool{}
	s.recoveryNext = time.Time{}
	s.recoveryFailures = 0
	s.recoveryActive = false
	s.recoveryHadPrimary = false
	s.recoveryNodes = map[string]recoveryNode{}
	s.targetFailures = map[string]targetFailure{}
	s.tlsChecked = map[string]time.Time{}
	s.recheck, s.recheckLast = map[string]time.Time{}, map[string]time.Time{}
	if s.fixed != "" {
		s.requested = s.byName[s.fixed]
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.schedule(ctx) }()
	select {
	case s.wake <- struct{}{}:
	default:
	}
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
	s.networkTransition.Lock()
	defer s.networkTransition.Unlock()
	s.mu.Lock()
	unchanged := s.networkKey == key && s.online == online && (s.cancel != nil) == online
	s.networkKey, s.online = key, online
	s.mu.Unlock()
	if unchanged {
		return
	}
	if online {
		ready := make(chan struct{})
		release := s.armHandoff(s, ready)
		defer func() { close(ready); release() }()
	}
	s.Stop()
	s.engine.SwitchNetwork(s.journal.ID("network:" + key))
	if online {
		s.start("network_changed")
	}
	s.log("network_changed", "network_changed", "", false, 0)
	s.notifyRouteChange()
}

func (s *Stable) Probe(ctx context.Context, id string, verify, elect bool) (stability.Sample, error) {
	return s.probeCandidate(ctx, id, verify, elect, false)
}

func (s *Stable) probeCandidate(ctx context.Context, id string, verify, elect, fault bool, bootstrap ...bool) (stability.Sample, error) {
	quick := len(bootstrap) > 0 && bootstrap[0]
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
	defer func() { s.mu.Lock(); delete(s.probing, id); s.notifyReadyLocked(); s.mu.Unlock(); s.wg.Done() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(owner, cancel)
	defer stop()
	class := 2
	queuedState := s.engine.Snapshot()
	_, _, queuedPriority, _ := stableProbePolicy(queuedState, id, verify, quick, fault)
	if queuedPriority == stability.MaintenanceProbe {
		class = 0
	} else if queuedPriority == stability.RecoveryProbe || queuedPriority == stability.StandbyProbe {
		class = 1
	}
	if search, _ := ctx.Value(stableSearchKey{}).(bool); search {
		class = 2
	}
	if quality, _ := ctx.Value(stableQualityKey{}).(bool); quality {
		class = 2
	}
	queuedAt := time.Now()
	var traceID uint64
	traceStartup := len(queuedState.Active) == 0 && quick && s.journal != nil
	if traceStartup {
		traceID = startupProbeSequence.Add(1)
		_ = s.journal.Record(stability.Event{Event: "startup_probe", Reason: "startup_availability", Group: s.groupID, Node: id, Epoch: queuedState.Epoch, ProbeID: traceID})
	}
	recordQueueCancel := func() {
		if traceStartup {
			_ = s.journal.Record(stability.Event{Event: "startup_probe_result", Reason: "cancelled", Group: s.groupID, Node: id, Epoch: queuedState.Epoch, ProbeID: traceID, QueueMs: time.Since(queuedAt).Milliseconds()})
		}
	}
	if admitted, _ := ctx.Value(admittedSearchKey{}).(bool); !admitted {
		releaseDispatch, dispatchErr := stableBackgroundDispatch.acquire(ctx, s.groupID, class, cancel, len(queuedState.Active) == 0)
		if dispatchErr != nil {
			recordQueueCancel()
			return stability.Sample{Error: "cancelled"}, dispatchErr
		}
		defer releaseDispatch()
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		recordQueueCancel()
		return stability.Sample{}, ctx.Err()
	}
	state := s.engine.Snapshot()
	verify, quick, priority, credits := stableProbePolicy(state, id, verify, quick, fault)
	if quality, _ := ctx.Value(stableQualityKey{}).(bool); quality {
		priority = ""
	}
	search, _ := ctx.Value(stableSearchKey{}).(bool)
	if search {
		priority = stability.SearchProbe
	}
	timeout := 9 * time.Second
	for _, node := range state.Nodes {
		if node.ID == id && node.Failures > 0 {
			timeout = 9 * time.Second
		}
	}
	if verify {
		timeout = 18 * time.Second
		if len(state.Active) == 0 {
			timeout = 6 * time.Second
		}
	}
	if quick {
		timeout = stableAvailabilityTimeout()
		ctx = context.WithValue(ctx, stableAvailabilityKey{}, true)
	}
	if search {
		timeout = stableSearchTimeout
	}
	ctx, cancelProbe := context.WithTimeout(ctx, timeout)
	defer cancelProbe()
	probeStarted := time.Now()
	reserve := func() bool { return s.engine.Reserve(false) }
	var sample stability.Sample
	if scheduled, ok := ctx.Value(stableProbeLeaseKey{}).(func() bool); ok {
		reserve = scheduled
	} else if priority != "" {
		consume, release, ok := s.engine.LeaseProbe(priority, credits)
		if ok {
			defer release()
			reserve = consume
		} else {
			sample = stability.Sample{Epoch: state.Epoch, Error: "budget_exhausted"}
		}
	}
	if sample.Error == "" {
		ctx = C.WithProbeAccounting(ctx, &s.probeTraffic, reserve)
		for _, node := range state.Nodes {
			if node.ID == id {
				ctx = context.WithValue(ctx, stableProbeRouteKey{}, stableProbeRoute{network: state.NetworkKey, relay: node.Relay})
				break
			}
		}
		sample = s.probe(ctx, p, s.expected, verify, state.Epoch, reserve)
		if quick && s.journal != nil {
			_ = s.journal.Record(stability.Event{Event: "startup_probe_result", ProbeID: traceID, QueueMs: probeStarted.Sub(queuedAt).Milliseconds(), Group: s.groupID, Node: id, Epoch: state.Epoch, OK: sample.OK, Reason: sample.Error, DialMs: sample.ProbeDial.Milliseconds(), HandshakeMs: sample.ProbeTLS.Milliseconds(), ExitCheckMs: sample.ExitCheck.Milliseconds(), LatencyMs: time.Since(probeStarted).Milliseconds()})
		}
	}
	// A short search deadline means "not yet verified", not proof of a bad
	// route. Retain it for the slower, independent recovery pass.
	searchTimeout := ctx.Err() == context.DeadlineExceeded || sample.Error == "exit_query_timeout" || sample.Error == "https_timeout" || sample.Error == "relay_dial_timeout"
	if search && !sample.OK && searchTimeout && !sample.Hard {
		s.log("probe", "search_deferred", id, false, 0)
		return stability.Sample{Epoch: state.Epoch, Error: "cancelled"}, context.Canceled
	}
	if owner.Err() != nil || ctx.Err() == context.Canceled || sample.Error == "cancelled" {
		return sample, context.Canceled
	}
	if sample.Error == "budget_exhausted" {
		b := s.engine.Snapshot().ProbeBudgets[priority]
		_ = s.journal.Record(stability.Event{Event: "budget_exhausted", Reason: "budget_exhausted", Group: s.groupID, Node: id, Epoch: state.Epoch, ProbeClass: string(priority), Tokens: b.Tokens})
		return sample, errors.New("probe budget exhausted")
	}
	if !sample.OK {
		sample.FailureWait = time.Since(probeStarted)
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
		s.notifyRouteChange()
	}
	return sample, nil
}

func (s *Stable) DialContext(ctx context.Context, metadata *C.Metadata) (conn C.Conn, resultErr error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	active := s
	defer func() {
		reason := ""
		if resultErr != nil {
			reason, _ = stableError(resultErr)
		}
		deadline, _ := ctx.Deadline()
		remaining := int64(0)
		if !deadline.IsZero() {
			remaining = max(0, time.Until(deadline).Milliseconds())
		}
		_ = s.journal.Record(stability.Event{Event: "dial_final", Reason: reason, Group: active.groupID, Epoch: active.engine.Snapshot().Epoch, OK: resultErr == nil, DialMs: time.Since(started).Milliseconds(), Target: s.journal.ID(metadata.RemoteAddress()), Service: targetService(metadata.RemoteAddress()), RemainingMs: remaining})
	}()
	conn, resultErr = s.dialCurrent(ctx, metadata)
	var move *stableHandoff
	if !errors.As(resultErr, &move) {
		return
	}
	defer func() { move.recordOutcome(resultErr) }()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-move.ready:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	active = move.next
	return active.dialCurrent(ctx, metadata)
}

func (s *Stable) dialCurrent(parent context.Context, metadata *C.Metadata) (conn C.Conn, resultErr error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.cancel == nil {
		err := s.inactiveErrorLocked()
		s.mu.Unlock()
		return nil, err
	}
	owner := s.ctx
	s.lastBusiness = time.Now()
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer func() {
		if resultErr == nil {
			return
		}
		if err := parent.Err(); err != nil {
			if !errors.Is(resultErr, err) {
				resultErr = err
			}
			return
		}
		if owner.Err() != nil {
			s.mu.Lock()
			resultErr = s.inactiveErrorLocked()
			s.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(owner, cancel)
	defer stop()
	attempts := s.engine.Attempts()
	if len(attempts) == 0 {
		if err := s.awaitStartup(ctx, owner); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, errors.Join(errors.New("qualification_wait_timeout"), err)
			}
			return nil, err
		}
		attempts = s.engine.Attempts()
		if len(attempts) == 0 {
			return nil, errors.New("no validated residential chain")
		}
	}
	return s.establish(ctx, metadata)
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
