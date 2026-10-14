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
	"net/netip"
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

func probeStable(ctx context.Context, proxy C.Proxy, expected string, verify bool, epoch uint64, reserve func() bool) stability.Sample {
	sample := stability.Sample{Epoch: epoch, Phase: "delay", Service: "control"}
	status, _ := utils.NewUnsignedRanges[uint16]("204")
	measure := func(url string) (uint16, error) {
		if !reserve() {
			return 0, errors.New("budget_exhausted")
		}
		delay, err := proxy.URLTest(ctx, url, status)
		if err == nil && !proxy.AliveForTestUrl(url) {
			return 0, errors.New("service_failed")
		}
		return delay, err
	}
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
	if !verify {
		sample.OK = true
		return sample
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
	request := func(url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	if verify {
		for _, host := range []string{"chatgpt.com:443", "android.chat.openai.com:443"} {
			sample.Phase = "residential"
			sample.Service = "chatgpt"
			if strings.HasPrefix(host, "android.") {
				sample.Service = "android"
			}
			if !reserve() {
				sample.Error = "budget_exhausted"
				return sample
			}
			m := &C.Metadata{NetWork: C.TCP, Type: C.INNER}
			m.SetRemoteAddress(host)
			conn, err := proxy.DialContext(ctx, m)
			if err != nil {
				sample.Error, sample.Hard = stableError(err)
				return sample
			}
			hostname, _, _ := net.SplitHostPort(host)
			sample.Phase = "tls"
			tc := tls.Client(conn, &tls.Config{ServerName: hostname, MinVersion: tls.VersionTLS12})
			err = tc.HandshakeContext(ctx)
			service := "chatgpt"
			if strings.HasPrefix(host, "android.") {
				service = "android"
			}
			sample.TLSInfo = append(sample.TLSInfo, stableTLSInfo(service, tc.ConnectionState()))
			tc.Close()
			if err != nil {
				sample.Error, sample.Hard = stableError(err)
				if sample.Error == "relay_dial_timeout" {
					sample.Error = "tls_handshake_timeout"
				}
				return sample
			}
		}
		sample.TLS = true
		sample.Phase, sample.Service = "exit", "exit"
		resp, err := request("https://api.ipify.org")
		if err != nil {
			sample.Error, sample.Hard = stableError(err)
			if sample.Error == "relay_dial_timeout" {
				sample.Error = "exit_query_timeout"
			}
			return sample
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 128))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != 200 {
			sample.Error = "service_failed"
			return sample
		}
		ip, parseErr := netip.ParseAddr(strings.TrimSpace(string(data)))
		want, _ := netip.ParseAddr(expected)
		if parseErr != nil {
			sample.Error = "service_failed"
			return sample
		}
		if ip.Unmap() != want.Unmap() {
			sample.Error = "unexpected_exit"
			sample.Hard = true
			return sample
		}
		sample.Exit = true
	}
	sample.OK = true
	return sample
}

func stableTLSInfo(service string, state tls.ConnectionState) stability.TLSInfo {
	info := stability.TLSInfo{Service: service, Version: state.Version, Verified: len(state.VerifiedChains) > 0}
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
