package tunnel

import (
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
	"sync"
)

func StabilityBusinessAnomaly(route stability.BusinessRoute, reason string) {
	for _, p := range AllProxies() {
		if g, ok := p.Adapter().(interface {
			BusinessAnomaly(stability.BusinessRoute, string)
		}); ok {
			g.BusinessAnomaly(route, reason)
		}
	}
}

var stabilityLifecycle sync.Mutex
var stabilityRunning bool
var stabilityOnline = true
var stabilityNetworkKey = "startup"

type stabilityGroup interface {
	Start()
	Stop()
	NetworkChanged(string, bool)
}

func SetStabilityRunning(running bool) {
	stabilityLifecycle.Lock()
	defer stabilityLifecycle.Unlock()
	stabilityRunning = running
	for _, p := range AllProxies() {
		if g, ok := p.Adapter().(stabilityGroup); ok {
			if running && stabilityOnline {
				g.NetworkChanged(stabilityNetworkKey, true)
			} else {
				g.Stop()
			}
		}
	}
}

func StabilityNetworkChanged(key string, online bool) {
	stabilityLifecycle.Lock()
	defer stabilityLifecycle.Unlock()
	if key == stabilityNetworkKey && online == stabilityOnline {
		return
	}
	stabilityNetworkKey = key
	stabilityOnline = online
	if !stabilityRunning {
		return
	}
	for _, p := range AllProxies() {
		if g, ok := p.Adapter().(stabilityGroup); ok {
			g.NetworkChanged(key, online)
		}
	}
}

func StabilityDelay(name, url string, delay uint16) {
	for _, p := range AllProxies() {
		if g, ok := p.Adapter().(interface{ ObserveDelay(string, string, uint16) }); ok {
			g.ObserveDelay(name, url, delay)
		}
	}
}

type stableReloadGroup interface {
	ArmStableHandoff(C.ProxyAdapter, <-chan struct{}) func()
}

func prepareStableUpdate(previous, next map[string]C.Proxy) func() {
	published := make(chan struct{})
	var cleanup []func()
	for name, p := range next {
		replacement := p.Adapter()
		n, ok := replacement.(stableReloadGroup)
		if !ok {
			continue
		}
		cleanup = append(cleanup, n.ArmStableHandoff(replacement, published))
		if old := previous[name]; old != nil && old.Adapter() != replacement {
			if g, ok := old.Adapter().(stableReloadGroup); ok {
				g.ArmStableHandoff(replacement, published)
			}
		}
	}
	return func() {
		close(published)
		for _, release := range cleanup {
			release()
		}
	}
}
