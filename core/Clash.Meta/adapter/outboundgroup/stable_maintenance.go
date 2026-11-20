package outboundgroup

import (
	"runtime"
	"time"

	"github.com/metacubex/mihomo/component/stability"
)

type stableQualityKey struct{}

func stableWarmTarget() int {
	if runtime.GOOS == "android" {
		return 0
	}
	return 2
}

func stableHealthInterval(powerSave bool) time.Duration {
	if runtime.GOOS == "android" || powerSave {
		return 45 * time.Second
	}
	return 30 * time.Second
}

func stableMaintenanceCandidate(state stability.Snapshot, now time.Time, pending func(string) bool) string {
	best, deadline := "", time.Time{}
	for _, node := range state.Nodes {
		active, warm := containsStable(state.Active, node.ID), containsStable(state.Warm, node.ID)
		if !active && !warm || pending(node.ID) {
			continue
		}
		interval := stableHealthInterval(state.PowerSave)
		if warm {
			interval = 60 * time.Second
		}
		if node.Health == "suspect" {
			interval = 3 * time.Second
		}
		due := node.LastCheck.Add(interval)
		if !now.Before(due) && (best == "" || due.Before(deadline)) {
			best, deadline = node.ID, due
		}
	}
	return best
}
