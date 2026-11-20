package outboundgroup

import (
	"errors"
	"fmt"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type relaySelection struct {
	sources []*Stable
	url     string
	cursor  int
	mu      sync.Mutex
}

func parseRelaySelection(opt *GroupCommonOption, mapping map[string]C.Proxy) (*relaySelection, error) {
	if len(opt.Use) > 0 || opt.IncludeAll || opt.IncludeAllProviders || opt.IncludeAllProxies || opt.Filter != "" || opt.ExcludeFilter != "" || opt.ExcludeType != "" {
		return nil, errors.New("relay-url-test requires explicit groups and relay nodes")
	}
	if opt.URL == "" {
		opt.URL = C.DefaultTestURL
	}
	r := &relaySelection{url: opt.URL}
	var leaves []string
	seen := map[string]bool{}
	for _, name := range opt.Proxies {
		if seen[name] {
			continue
		}
		seen[name] = true
		p := mapping[name]
		if p == nil {
			return nil, fmt.Errorf("unknown relay source or node %q", name)
		}
		if g, ok := p.Adapter().(*Stable); ok {
			r.sources = append(r.sources, g)
			continue
		}
		if _, ok := p.Adapter().(ProxyGroup); ok || p.ProxyInfo().DialerProxy != "" || p.Type() == C.Direct || p.Type() == C.Reject || p.Type() == C.Pass || p.Type() == C.Compatible {
			return nil, fmt.Errorf("invalid airport relay %q", name)
		}
		leaves = append(leaves, name)
	}
	if len(r.sources) == 0 || len(leaves) == 0 {
		return nil, errors.New("relay-url-test requires residential sources and airport relays")
	}
	opt.Proxies = leaves
	opt.Type = "url-test"
	return r, nil
}

func (r *relaySelection) parents() map[string]bool {
	names := make(map[string]bool, len(r.sources))
	for _, s := range r.sources {
		if p := s.byID[s.engine.FreshPrimary()]; p != nil {
			names[p.ProxyInfo().DialerProxy] = true
		}
	}
	return names
}

func (r *relaySelection) sample(p C.Proxy, now time.Time) C.DelayHistory {
	s, ok := p.(interface{ LatestRelaySample(string) C.DelayHistory })
	if !ok {
		return C.DelayHistory{}
	}
	h := s.LatestRelaySample(r.url)
	if h.Time.UnixNano() < ordinaryRecoveryStart.Load() || h.Time.After(now) || now.Sub(h.Time) > 2*time.Minute {
		return C.DelayHistory{}
	}
	return h
}

func (r *relaySelection) pick(ps []C.Proxy, parents map[string]bool, now time.Time) C.Proxy {
	var best C.Proxy
	bestTier := 4
	var bestDelay uint16
	for _, p := range ps {
		h := r.sample(p, now)
		tier := 2
		switch {
		case !h.Time.IsZero() && h.Delay > 0:
			tier = 0
		case !h.Time.IsZero():
			tier = 3
		case parents[p.Name()]:
			tier = 1
		}
		if best == nil || tier < bestTier || (tier == bestTier && (h.Delay < bestDelay || h.Delay == bestDelay && parents[p.Name()] && !parents[best.Name()])) {
			best, bestTier, bestDelay = p, tier, h.Delay
		}
	}
	return best
}

func (r *relaySelection) batch(ps []C.Proxy) []C.Proxy {
	if len(ps) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	parents := r.parents()
	out := make([]C.Proxy, 0, 8)
	seen := map[string]bool{}
	add := func(p C.Proxy) {
		if !seen[p.Name()] {
			out = append(out, p)
			seen[p.Name()] = true
		}
	}
	best := r.pick(ps, parents, now)
	add(best)
	for _, p := range ps {
		if parents[p.Name()] {
			add(p)
		}
	}
	budget := 5
	if r.sample(best, now).Delay == 0 {
		budget = 8
	}
	for i := 0; i < len(ps) && len(out) < budget; i++ {
		p := ps[r.cursor%len(ps)]
		r.cursor = (r.cursor + 1) % len(ps)
		add(p)
	}
	return out
}
