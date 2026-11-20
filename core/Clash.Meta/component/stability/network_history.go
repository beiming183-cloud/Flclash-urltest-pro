package stability

import "math"

func (e *Engine) validStartupHint(h StartupHint) bool {
	return e.nodes[h.Node] != nil && h.Score > 0 && !math.IsInf(h.Score, 0) && !math.IsNaN(h.Score) && reasons[h.Error]
}

// Hints affect only probe order. LastSuccess/TLSAt/ExitAt are never restored.
func (e *Engine) restoreNetworkHints(saved map[string][]StartupHint) {
	if e.networkHints == nil {
		e.networkHints = map[string]map[string]StartupHint{}
	}
	for key, hints := range saved {
		if !validID(key) || len(e.networkHints) >= 8 || len(hints) > MaxCandidates {
			continue
		}
		network := map[string]StartupHint{}
		for _, hint := range hints {
			if e.validStartupHint(hint) {
				network[hint.Node] = hint
			}
		}
		e.networkHints[key] = network
	}
}

func (e *Engine) selectNetworkHints(key string) {
	if e.networkHints == nil {
		e.networkHints = map[string]map[string]StartupHint{}
	}
	if hints, known := e.networkHints[key]; known {
		e.startup = map[string]StartupHint{}
		for id, hint := range hints {
			e.startup[id] = hint
		}
	}
	// A previously unseen network can use old hints as an exploration seed,
	// but they do not become this network's history until freshly verified.
	if e.networkHints[key] == nil {
		e.networkHints[key] = map[string]StartupHint{}
	}
}
