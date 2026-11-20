package tunnel

import (
	"context"
	"fmt"
	"github.com/metacubex/mihomo/component/stability"
	"testing"
	"time"
)

func TestMobileExhaustedRouteBudgetIsNotRestartedByOuterRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	calls := 0
	_, err := retry(ctx, func(context.Context) (int, error) {
		calls++
		return 0, fmt.Errorf("failed routes: %w", stability.ErrEstablishmentExhausted)
	}, nil)
	if err == nil || calls != 1 {
		t.Fatalf("outer retry restarted exhausted route budget: calls=%d err=%v", calls, err)
	}
}

func TestMobileStoppedGroupIsNotDialedTenTimes(t *testing.T) {
	calls := 0
	_, err := retry(context.Background(), func(context.Context) (int, error) {
		calls++
		return 0, fmt.Errorf("reload: %w", stability.ErrServiceStopped)
	}, nil)
	if err == nil || calls != 1 {
		t.Fatalf("dialed a stopped group %d times: %v", calls, err)
	}
}
