package outboundgroup

import "testing"

func TestPreferredStartupHintsDoNotHideOtherCandidates(t *testing.T) {
	s, _, _, _ := testStable(t)
	s.engine.Invalidate()
	s.preferred = []string{s.byName["chain-a"]}
	if got := len(s.Proxies()); got != len(s.byID) {
		t.Fatalf("preferred startup hint hid full candidate list: got %d, want %d", got, len(s.byID))
	}
}

func TestLiveDiagnosticIncludesSchedulerEligibility(t *testing.T) {
	s, _, _, _ := testStable(t)
	_, state := s.DiagnosticState()
	if state.Scheduler == nil || state.Scheduler["candidates"].(map[string]int)["total"] != len(s.byID) {
		t.Fatal("diagnostic omitted scheduler eligibility")
	}
}
