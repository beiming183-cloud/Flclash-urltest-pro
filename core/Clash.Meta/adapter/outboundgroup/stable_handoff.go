package outboundgroup

import (
	"sort"
	"strings"
	"sync"

	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

type stableHandoff struct {
	next    *Stable
	ready   <-chan struct{}
	success sync.Once
	failure sync.Once
}

func (*stableHandoff) Error() string { return stability.ErrServiceStopped.Error() }
func (*stableHandoff) Unwrap() error { return stability.ErrServiceStopped }

func stableBinding(j *stability.Journal, name, expected, account string, endpoints map[string]bool) string {
	keys := make([]string, 0, len(endpoints))
	for ep := range endpoints {
		keys = append(keys, ep)
	}
	sort.Strings(keys)
	return j.ID(strings.Join(append([]string{"stable-binding", name, expected, account}, keys...), "\x00"))
}

func (s *Stable) ArmStableHandoff(adapter C.ProxyAdapter, ready <-chan struct{}) func() {
	next, ok := adapter.(*Stable)
	if !ok || next == nil || s.reloadKey == "" || s.reloadKey != next.reloadKey || ready == nil {
		return func() {}
	}
	return s.armHandoff(next, ready)
}

func (s *Stable) armHandoff(next *Stable, ready <-chan struct{}) func() {
	s.mu.Lock()
	if s.handoff != nil {
		s.mu.Unlock()
		return func() {}
	}
	h := &stableHandoff{next: next, ready: ready}
	s.handoff = h
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if s.handoff == h {
			s.handoff = nil
		}
		s.mu.Unlock()
	}
}

func (s *Stable) inactiveErrorLocked() error {
	if s.handoff != nil {
		return s.handoff
	}
	return stability.ErrServiceStopped
}

func (h *stableHandoff) recordOutcome(err error) {
	if err == nil {
		h.success.Do(func() { h.next.log("dial_handoff", "config_or_network_restart", "", true, 0) })
		return
	}
	h.failure.Do(func() { reason, _ := stableError(err); h.next.log("dial_handoff", reason, "", false, 0) })
}
