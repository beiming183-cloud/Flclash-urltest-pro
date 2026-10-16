package stability

import "testing"

func TestColdResponseFromPhoneDiagnosticDoesNotFavorSlowNewRoute(t *testing.T) {
	fast := Stats{EWMA: 482.11379010643805, P50: 354, P90: 798, P95: 798, MAD: 9, FirstResponseMedian: 2638.467, FirstResponseCount: 5, Recent: []bool{true, true, true, true, true, true, true}}
	slow := Stats{EWMA: 632.5460636384256, P50: 631, P90: 704, P95: 704, MAD: 73, FirstResponseMedian: 2826.164308, FirstResponseCount: 1, Recent: []bool{true, true, true}}
	if fastCost, slowCost := LatencyCost(fast), LatencyCost(slow); !(fastCost < slowCost*.88 && fastCost+25 < slowCost) {
		t.Fatalf("Cold-response evidence favored the slower new route: fast=%v slow=%v", fastCost, slowCost)
	}
}
