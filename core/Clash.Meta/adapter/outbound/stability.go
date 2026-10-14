package outbound

import C "github.com/metacubex/mihomo/constant"

type ResidentialInfo struct {
	Endpoint  string
	Account   string
	Relay     string
	UnsafeTLS bool
}

func ResidentialIdentity(adapter C.ProxyAdapter, hash func(string) string) (ResidentialInfo, bool) {
	if wrapper, ok := adapter.(*autoCloseProxyAdapter); ok {
		adapter = wrapper.ProxyAdapter
	}
	switch p := adapter.(type) {
	case *Http:
		return ResidentialInfo{p.Addr(), hash(p.user + "\x00" + p.pass), p.option.DialerProxy, p.option.SkipCertVerify}, true
	case *Socks5:
		return ResidentialInfo{p.Addr(), hash(p.user + "\x00" + p.pass), p.option.DialerProxy, p.option.SkipCertVerify}, true
	default:
		return ResidentialInfo{}, false
	}
}
