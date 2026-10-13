package tunnel

import "sync"

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
				g.Start()
			} else {
				g.Stop()
			}
		}
	}
}

func StabilityNetworkChanged(key string, online bool) {
	stabilityLifecycle.Lock()
	defer stabilityLifecycle.Unlock()
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
