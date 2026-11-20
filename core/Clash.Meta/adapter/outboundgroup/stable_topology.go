package outboundgroup

import (
	"net"
	"net/netip"
	"strings"
)

func stableRelayFailureDomain(name, address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return "relay:" + name
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	}
	return "host:" + host
}
