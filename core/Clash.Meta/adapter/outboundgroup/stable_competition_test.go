package outboundgroup

import (
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/stability"
)

func TestFullActivePoolStillValidatesFastChallenger(t *testing.T) {
	now := time.Unix(1000, 0)
	state := stability.Snapshot{Active: []string{"a", "b", "c"}}
	for _, id := range state.Active {
		state.Nodes = append(state.Nodes, stability.Stats{Candidate: stability.Candidate{ID: id}, EWMA: 450, P95: 520})
	}
	state.Nodes = append(state.Nodes, stability.Stats{Candidate: stability.Candidate{ID: "challenger"}, EWMA: 220, P95: 235, Streak: 2, LastCheck: now.Add(-6 * time.Second), LastSuccess: now.Add(-6 * time.Second), TLSAt: now.Add(-6 * time.Second), ExitAt: now.Add(-6 * time.Second)})
	if got := competitionCandidate(state, now, func(string) bool { return false }); got != "challenger" {
		t.Fatalf("Full pool blocked faster challenger validation: %q", got)
	}
	if got := competitionCandidate(state, now, func(string) bool { return true }); got != "" {
		t.Fatalf("Pending challenger was scheduled twice: %q", got)
	}
	state.Nodes[3].Quarantined = true
	if got := competitionCandidate(state, now, func(string) bool { return false }); got != "" {
		t.Fatalf("Quarantined challenger scheduled: %q", got)
	}
}

func TestSlowCandidateCannotConsumeFullPoolCompetitionBudget(t *testing.T) {
	now := time.Unix(1000, 0)
	state := stability.Snapshot{Active: []string{"a", "b", "c"}}
	for _, id := range state.Active {
		state.Nodes = append(state.Nodes, stability.Stats{Candidate: stability.Candidate{ID: id}, EWMA: 230, P95: 250})
	}
	state.Nodes = append(state.Nodes, stability.Stats{Candidate: stability.Candidate{ID: "slow"}, EWMA: 600, P95: 750, Streak: 2, LastCheck: now.Add(-6 * time.Second), LastSuccess: now.Add(-6 * time.Second)})
	if got := competitionCandidate(state, now, func(string) bool { return false }); got != "" {
		t.Fatalf("Slow candidate displaced challenger work: %q", got)
	}
}
