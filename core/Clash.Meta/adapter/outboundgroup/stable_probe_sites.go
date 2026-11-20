package outboundgroup

import (
	"context"
	"sync"
	"time"
)

type stableProbeRouteKey struct{}
type stableProbeRoute struct{ network, relay string }
type stableSiteState struct {
	failures       map[string]time.Time
	until, updated time.Time
}
type stableSiteHealth struct {
	mu    sync.Mutex
	sites map[string]*stableSiteState
}

var stableProbeSites = &stableSiteHealth{sites: map[string]*stableSiteState{}}

func (h *stableSiteHealth) ordered(ctx context.Context, sites []string, now time.Time) []string {
	route, ok := ctx.Value(stableProbeRouteKey{}).(stableProbeRoute)
	if !ok {
		return sites
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	result, deferred := []string{}, []string{}
	for _, site := range sites {
		state := h.sites[route.network+"\x00"+site]
		if state != nil && now.Before(state.until) {
			deferred = append(deferred, site)
		} else {
			result = append(result, site)
		}
	}
	return append(result, deferred...)
}

func (h *stableSiteHealth) observe(ctx context.Context, site string, err error, now time.Time) {
	route, ok := ctx.Value(stableProbeRouteKey{}).(stableProbeRoute)
	if !ok || ctx.Err() == context.Canceled {
		return
	}
	if err != nil {
		reason, hard := stableError(err)
		if hard || reason == "cancelled" || reason == "budget_exhausted" || reason == "residential_auth" {
			return
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, state := range h.sites {
		if now.Sub(state.updated) > 2*time.Minute {
			delete(h.sites, key)
		}
	}
	key := route.network + "\x00" + site
	state := h.sites[key]
	if state == nil {
		if len(h.sites) >= 32 {
			return
		}
		state = &stableSiteState{failures: map[string]time.Time{}}
		h.sites[key] = state
	}
	state.updated = now
	if err == nil {
		state.failures = map[string]time.Time{}
		state.until = time.Time{}
		return
	}
	for relay, at := range state.failures {
		if now.Sub(at) > 10*time.Second {
			delete(state.failures, relay)
		}
	}
	state.failures[route.relay] = now
	if len(state.failures) >= 3 {
		state.until = now.Add(30 * time.Second)
	}
}

func (h *stableSiteHealth) snapshot() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	open := 0
	for _, state := range h.sites {
		if time.Now().Before(state.until) {
			open++
		}
	}
	return map[string]any{"temporarilyDeprioritized": open, "tracked": len(h.sites), "evidence": "soft failures across at least three configured entry hosts; root cause unproven"}
}
