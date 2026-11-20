package outboundgroup

import "testing"

func TestStableTopologyDoesNotCountAliasesOrPortsAsIndependentHosts(t *testing.T) {
	a := stableRelayFailureDomain("RF2-a", "Example.COM.:443")
	b := stableRelayFailureDomain("RF2-b", "example.com:8443")
	if a != b {
		t.Fatal("same host counted as two failure domains", a, b)
	}
	if a == stableRelayFailureDomain("RF2-c", "other.example:443") {
		t.Fatal("independent configured hosts collapsed")
	}
	if stableRelayFailureDomain("RF2-a", "") == stableRelayFailureDomain("RF2-b", "") {
		t.Fatal("unknown endpoints falsely correlated")
	}
}
