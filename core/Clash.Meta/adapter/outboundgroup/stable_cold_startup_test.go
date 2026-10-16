package outboundgroup

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAvailabilityExitRequestAllowsHealthyColdTLS(t *testing.T) {
	start := time.Now()
	err, hard := verifyStableExit("72.1.144.187", func(url string) ([]byte, error) {
		parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		request, cancelRequest := context.WithTimeout(parent, stableExitRequestTimeout(true))
		defer cancelRequest()
		select {
		case <-time.After(1700 * time.Millisecond):
			if url == "https://api.ipify.org" {
				return []byte("72.1.144.187"), nil
			}
			return nil, errors.New("backup must not be required")
		case <-request.Done():
			return nil, request.Err()
		}
	})
	if err != nil || hard {
		t.Fatalf("healthy cold TLS rejected after %s: %v", time.Since(start), err)
	}
}
