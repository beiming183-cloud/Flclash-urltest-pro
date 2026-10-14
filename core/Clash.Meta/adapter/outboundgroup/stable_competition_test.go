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
	state.Nodes[3].LastSuccess = now.Add(-3 * time.Minute)
	state.Nodes[3].LastCheck = state.Nodes[3].LastSuccess
	if got := competitionCandidate(state, now, func(string) bool { return false }); got != "challenger" {
		t.Fatalf("Previously fast idle challenger lost its revalidation opportunity: %q", got)
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

func TestCompetitionPreservesRemainingMaintenanceBudget(t *testing.T) {
	for _, test := range []struct {
		used, reset int
		want        bool
	}{
		{0, 3600, true},
		{200, 3600, true},
		{240, 3600, false},
		{345, 3000, false},
		{500, 1200, true},
		{780, 600, false},
	} {
		state := stability.Snapshot{BudgetUsed: test.used, BudgetResetIn: test.reset}
		if got := competitionBudgetAvailable(state); got != test.want {
			t.Fatalf("used=%d reset=%d got=%t want=%t", test.used, test.reset, got, test.want)
		}
	}
}

func TestCompetitionLeavesCapacityForFullHourMaintenance(t *testing.T) {
	used, checks := 0, 0
	for second := 0; second < 3600; second++ {
		if second%15 == 0 {
			used++
			checks++
		}
		if second%30 == 0 {
			used += 2
			checks += 2
		}
		if second%540 == 0 {
			used += 9
		}
		if used >= 840 {
			t.Fatalf("Maintenance exhausted budget at second %d: %d", second, used)
		}
		state := stability.Snapshot{BudgetUsed: used, BudgetResetIn: 3600 - second}
		if competitionBudgetAvailable(state) {
			used += 2
		}
	}
	if checks != 480 || used > 840 {
		t.Fatalf("Maintenance incomplete: checks=%d budget=%d", checks, used)
	}
}
