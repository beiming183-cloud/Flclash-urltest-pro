package outboundgroup

import "fmt"

func (s *Stable) notifyRouteChange() {
	s.routeNotificationMu.Lock()
	state := s.engine.Snapshot()
	key := fmt.Sprint(state.Epoch, ":", state.Generation, ":", state.Paused, ":", state.Primary, ":", state.Active)
	if key == s.routeNotificationKey {
		s.routeNotificationMu.Unlock()
		return
	}
	s.routeNotificationKey = key
	s.routeNotificationMu.Unlock()
	OnStableRouteChange()
}
