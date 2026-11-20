package outboundgroup

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/adapter/provider"
	C "github.com/metacubex/mihomo/constant"
)

var stableCoordinationEnabled atomic.Bool
var ordinaryRecoveryStart atomic.Int64

func allowOrdinaryRecovery(now time.Time, started, business int64) bool {
	return business > 0 && now.Sub(time.Unix(0, business)) <= 2*time.Minute || started > 0 && now.Sub(time.Unix(0, started)) <= 20*time.Second
}

var ordinaryProbeTraffic C.ProbeTraffic
var ordinaryProbeMu sync.Mutex
var ordinaryProbeWindow time.Time
var ordinaryProbeWindowBytes int64

func ordinaryProbeUsage(now time.Time) map[string]any {
	ordinaryProbeMu.Lock()
	defer ordinaryProbeMu.Unlock()
	total := ordinaryProbeTraffic.Sent.Load() + ordinaryProbeTraffic.Received.Load()
	if ordinaryProbeWindow.IsZero() || now.Sub(ordinaryProbeWindow) >= time.Hour {
		ordinaryProbeWindow, ordinaryProbeWindowBytes = now, total
	}
	limit := int64(32 << 20)
	if runtime.GOOS == "android" {
		limit = 8 << 20
	}
	used := total - ordinaryProbeWindowBytes
	return map[string]any{"issuedHttpRequests": ordinaryProbeTraffic.Requests.Load(), "sentBytes": ordinaryProbeTraffic.Sent.Load(), "receivedBytes": ordinaryProbeTraffic.Received.Load(), "windowBytes": used, "optionalHourLimitBytes": limit, "optionalAvailable": used < limit}
}

func init() {
	provider.AcquireBackgroundHealth = func(owner context.Context) (context.Context, func(), error) {
		if !stableCoordinationEnabled.Load() {
			return owner, func() {}, nil
		}
		if runtime.GOOS == "android" && !allowOrdinaryRecovery(time.Now(), ordinaryRecoveryStart.Load(), C.ProxyBusinessActivity.Load()) {
			return nil, nil, context.Canceled
		}
		if !ordinaryProbeUsage(time.Now())["optionalAvailable"].(bool) {
			return nil, nil, context.Canceled
		}
		ctx, cancel := context.WithCancel(owner)
		release, err := stableBackgroundDispatch.acquire(ctx, "ordinary-airport-health", 2, cancel)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		ctx = C.WithProbeAccounting(ctx, &ordinaryProbeTraffic, func() bool { return ordinaryProbeUsage(time.Now())["optionalAvailable"].(bool) })
		return ctx, func() { cancel(); release() }, nil
	}
}
