package outboundgroup

import (
	"runtime"
	"time"
)

func (s *Stable) probeUsage(now time.Time) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	sent, received := s.probeTraffic.Sent.Load(), s.probeTraffic.Received.Load()
	if s.probeWindow.IsZero() || now.Sub(s.probeWindow) >= time.Hour {
		s.probeWindow, s.probeWindowSent, s.probeWindowReceived = now, sent, received
	}
	limit := int64(32 << 20)
	if runtime.GOOS == "android" {
		limit = 8 << 20
	}
	used := sent - s.probeWindowSent + received - s.probeWindowReceived
	return map[string]any{"issuedHttpRequests": s.probeTraffic.Requests.Load(), "sentBytes": sent, "receivedBytes": received, "windowBytes": used, "optionalHourLimitBytes": limit, "optionalAvailable": used < limit, "scope": "probe transport bytes; excludes relay framing and physical-link overhead"}
}

func (s *Stable) optionalTrafficAvailable(now time.Time) bool {
	return s.probeUsage(now)["optionalAvailable"].(bool)
}

func (s *Stable) optionalQualityAvailable(now time.Time) bool {
	state := s.engine.Snapshot()
	if state.Paused || state.PowerSave || !s.optionalTrafficAvailable(now) {
		return false
	}
	if runtime.GOOS == "android" {
		s.mu.Lock()
		lastBusiness := s.lastBusiness
		s.mu.Unlock()
		return !lastBusiness.IsZero() && now.Sub(lastBusiness) <= 2*time.Minute
	}
	return true
}

func (s *Stable) Snapshot() map[string]any {
	result := s.snapshot()
	result["probeTraffic"] = s.probeUsage(time.Now())
	result["backgroundDispatch"] = stableBackgroundDispatch.snapshot()
	result["probeSites"] = stableProbeSites.snapshot()
	result["ordinaryProbeTraffic"] = ordinaryProbeUsage(time.Now())
	state := s.engine.Snapshot()
	relays, airports := map[string]bool{}, map[string]bool{}
	quality := 0
	for _, node := range state.Nodes {
		if containsStable(state.Active, node.ID) {
			relays[node.Relay], airports[node.Airport] = true, true
			if node.QualityReady {
				quality++
			}
		}
	}
	warmTarget := 2
	if runtime.GOOS == "android" {
		warmTarget = 0
	}
	result["workPool"] = map[string]any{"eligibleActive": len(state.Active), "targetActive": 3, "qualityReadyActive": quality, "warmCandidates": len(state.Warm), "targetWarm": warmTarget, "configuredEntryHosts": len(relays), "supplierLabels": len(airports), "underfilled": len(state.Active) < 3, "physicalIndependenceVerified": false}
	return result
}
