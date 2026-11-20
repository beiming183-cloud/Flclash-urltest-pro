package tunnel

import (
	"time"

	C "github.com/metacubex/mihomo/constant"
)

func tcpDialTimeout(proxy C.Proxy, metadata *C.Metadata) time.Duration {
	for depth := 0; proxy != nil && depth < 16; depth++ {
		switch proxy.Type() {
		case C.StableFailover:
			return 12 * time.Second
		case C.Selector, C.Fallback, C.URLTest, C.LoadBalance:
			proxy = proxy.Unwrap(metadata, false)
		default:
			return C.DefaultTCPTimeout
		}
	}
	return C.DefaultTCPTimeout
}
