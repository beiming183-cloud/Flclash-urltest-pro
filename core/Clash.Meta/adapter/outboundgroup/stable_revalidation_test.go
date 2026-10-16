package outboundgroup

import (
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
)

func TestManuallyRefreshedFastChallengerRevalidatesExpiredIdentity(t *testing.T) {
	now := time.Unix(10000, 0)
	state := stability.Snapshot{Active: []string{"a", "b", "c"}}
	for _, id := range state.Active {
		state.Nodes = append(state.Nodes, stability.Stats{Candidate: stability.Candidate{ID: id}, EWMA: 650, P50: 650, P95: 680})
	}
	challenger := stability.Stats{Candidate: stability.Candidate{ID: "fast"}, EWMA: 280, P50: 275, P95: 290,
		Streak: 5, QualityStreak: 5, FirstQuality: now.Add(-30 * time.Second), LastSuccess: now.Add(-6 * time.Second), LastCheck: now.Add(-6 * time.Second),
		TLSAt: now.Add(-11 * time.Minute), ExitAt: now.Add(-31 * time.Minute)}
	state.Nodes = append(state.Nodes, challenger)
	if got := competitionCandidate(state, now, func(string) bool { return false }); got != "fast" {
		t.Fatalf("Fresh manual delay result hid expired identity verification: got %q", got)
	}
}
