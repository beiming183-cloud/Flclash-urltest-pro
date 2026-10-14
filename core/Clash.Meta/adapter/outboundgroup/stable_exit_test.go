package outboundgroup

import (
	"context"
	"crypto/x509"
	"testing"
)

func TestExitServiceTimeoutUsesVerifiedAlternate(t *testing.T) {
	calls := 0
	err, hard := verifyStableExit("203.0.113.10", func(string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, context.DeadlineExceeded
		}
		return []byte("h=www.cloudflare.com\nip=203.0.113.10\n"), nil
	})
	if err != nil || hard || calls != 2 {
		t.Fatal("usable fixed exit was blocked by one service timeout", err, hard, calls)
	}
}

func TestExitMismatchAndCertificateFailureNeverFallThrough(t *testing.T) {
	for _, certificateFailure := range []bool{false, true} {
		calls := 0
		err, hard := verifyStableExit("203.0.113.10", func(string) ([]byte, error) {
			calls++
			if certificateFailure {
				return nil, x509.UnknownAuthorityError{}
			}
			return []byte("203.0.113.11"), nil
		})
		if err == nil || !hard || calls != 1 {
			t.Fatal("unsafe candidate reached fallback", hard, calls)
		}
	}
}
