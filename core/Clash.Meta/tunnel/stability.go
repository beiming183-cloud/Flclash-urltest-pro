package tunnel

import "sync"

var stabilityLifecycle sync.Mutex
var stabilityRunning bool

type stabilityGroup interface {
	Start()
	Stop()
	NetworkChanged()
}

func SetStabilityRunning(running bool) {
	stabilityLifecycle.Lock()
	defer stabilityLifecycle.Unlock()
	stabilityRunning = running
	for _, p := range AllProxies() {
		if g, ok := p.Adapter().(stabilityGroup); ok {
			if running {
				g.Start()
			} else {
				g.Stop()
			}
		}
	}
}

func StabilityNetworkChanged() {
	stabilityLifecycle.Lock()
	defer stabilityLifecycle.Unlock()
	if !stabilityRunning {
		return
	}
	for _, p := range AllProxies() {
		if g, ok := p.Adapter().(stabilityGroup); ok {
			g.NetworkChanged()
		}
	}
}
