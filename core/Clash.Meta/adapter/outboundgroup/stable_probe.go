//go:build go1.21

package outboundgroup

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/metacubex/mihomo/common/utils"
	"github.com/metacubex/mihomo/component/stability"
	C "github.com/metacubex/mihomo/constant"
)

func stableError(err error) (string, bool) {
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var authority x509.UnknownAuthorityError
	if errors.As(err, &hostname) {
		return "cert_hostname", true
	}
	if errors.As(err, &authority) {
		return "cert_untrusted", true
	}
	if errors.As(err, &invalid) {
		if invalid.Reason == x509.Expired {
			return "cert_expired", true
		}
		return "cert_untrusted", true
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", false
	}
	var nerr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &nerr) && nerr.Timeout() {
		return "relay_dial_timeout", false
	}
	text := strings.ToLower(err.Error())
	if text == "service_failed" {
		return "service_failed", false
	}
	if text == "unexpected_exit" {
		return "unexpected_exit", true
	}
	if strings.Contains(text, "budget_exhausted") {
		return "budget_exhausted", false
	}
	if strings.Contains(text, "auth") || strings.Contains(text, "username/password") {
		return "residential_auth", false
	}
	if strings.Contains(text, "connect") || strings.Contains(text, "remote err code") {
		return "connect_rejected", false
	}
	if errors.Is(err, io.EOF) || strings.Contains(text, "reset") || strings.Contains(text, "tls") {
		return "tls_aborted", false
	}
	return "unknown", false
}

type stableAvailabilityKey struct{}

func probeStable(ctx context.Context, proxy C.Proxy, expected string, verify bool, epoch uint64, reserve func() bool) stability.Sample {
	quick, _ := ctx.Value(stableAvailabilityKey{}).(bool)
	sample := stability.Sample{Epoch: epoch, Phase: "delay", Service: "control"}
	status, _ := utils.NewUnsignedRanges[uint16]("204")
	measure := func(url string) (uint16, error) {
		if !reserve() {
			return 0, errors.New("budget_exhausted")
		}
		var delay uint16
		var err error
		if timed, ok := proxy.(interface {
			URLTestWithTiming(context.Context, string, utils.IntRanges[uint16]) (C.URLTestResult, error)
		}); ok {
			result, testErr := timed.URLTestWithTiming(ctx, url, status)
			delay, err = result.Delay, testErr
			sample.FirstResponse = result.FirstResponse
			if err == nil && !result.ReusedConnection {
				err = errors.New("service_failed")
			}
		} else {
			delay, err = proxy.URLTest(ctx, url, status)
		}
		if err == nil && !proxy.AliveForTestUrl(url) {
			return 0, errors.New("service_failed")
		}
		return delay, err
	}
	if !quick {
		delay, err := measure("https://www.gstatic.com/generate_204")
		if err != nil && ctx.Err() == nil {
			_, hard := stableError(err)
			if !hard {
				sample.Service = "control_backup"
				delay, err = measure("https://cp.cloudflare.com/generate_204")
			}
		}
		if err != nil {
			sample.Error, sample.Hard = stableError(err)
			if sample.Error == "relay_dial_timeout" {
				sample.Error = "https_timeout"
			}
			return sample
		}
		sample.Latency = time.Duration(delay) * time.Millisecond
		sample.TLS = true
		if !verify {
			sample.OK = true
			return sample
		}
	}
	if quick {
		sample.Phase, sample.Service = "exit", "bootstrap"
	}
	transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if !reserve() {
			return nil, errors.New("budget_exhausted")
		}
		m := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
		if err := m.SetRemoteAddress(address); err != nil {
			return nil, err
		}
		return proxy.DialContext(ctx, m)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(url string) ([]byte, error) {
		timeout := 4 * time.Second
		if quick {
			timeout = 1400 * time.Millisecond
		}
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, errors.New("service_failed")
		}
		return io.ReadAll(io.LimitReader(resp.Body, 4096))
	}
	if verify {
		sample.TLS = true
		if !quick {
			sample.Phase, sample.Service = "exit", "exit"
		}
		err, hard := verifyStableExit(expected, request)
		if err != nil {
			sample.Error, sample.Hard = stableError(err)
			sample.Hard = sample.Hard || hard
			if sample.Error == "relay_dial_timeout" {
				sample.Error = "exit_query_timeout"
			}
			return sample
		}
		sample.Exit = true
	}
	sample.OK = true
	return sample
}

func stableTLSInfo(service string, state tls.ConnectionState) stability.TLSInfo {
	info := stability.TLSInfo{Service: service, Version: state.Version, Verified: len(state.VerifiedChains) > 0, HandshakeComplete: state.HandshakeComplete}
	if len(state.PeerCertificates) > 0 {
		cert := state.PeerCertificates[0]
		fingerprint := sha256.Sum256(cert.Raw)
		spki := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		info.Certificate = hex.EncodeToString(fingerprint[:])
		info.SPKI = hex.EncodeToString(spki[:])
		info.SANCount = len(cert.DNSNames) + len(cert.IPAddresses)
		info.NotBefore = cert.NotBefore
		info.NotAfter = cert.NotAfter
	}
	return info
}
