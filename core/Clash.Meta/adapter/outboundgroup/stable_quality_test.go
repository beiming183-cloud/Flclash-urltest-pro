package outboundgroup

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type qualityRoundTripper func(*http.Request) (*http.Response, error)

func (f qualityRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type pacedQualityBody struct {
	ctx       context.Context
	remaining int64
	read      int64
}

func (b *pacedQualityBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
	n := len(p)
	if n > 32<<10 {
		n = 32 << 10
	}
	if int64(n) > b.remaining {
		n = int(b.remaining)
	}
	b.remaining -= int64(n)
	b.read += int64(n)
	return n, nil
}
func (*pacedQualityBody) Close() error { return nil }

func TestThroughputMeasuresBoundedDownloadAndConcurrentLatency(t *testing.T) {
	var headCalls atomic.Int32
	var body *pacedQualityBody
	client := &http.Client{Transport: qualityRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodHead {
			time.Sleep(time.Millisecond)
			headCalls.Add(1)
			return &http.Response{StatusCode: 204, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		body = &pacedQualityBody{ctx: req.Context(), remaining: 2 * speedSampleBytes}
		return &http.Response{StatusCode: 200, ContentLength: speedSampleBytes, Header: http.Header{}, Body: body}, nil
	})}
	mbps, count, reliable, loaded, err := measureThroughput(context.Background(), client, func() bool { return true })
	if err != nil || count != speedSampleBytes || body.read != speedSampleBytes || !reliable || mbps <= 0 || loaded <= 0 || headCalls.Load() != 2 {
		t.Fatal("missing bounded throughput or overlapping latency measurement", mbps, count, reliable, loaded, err)
	}
}

func TestThroughputRejectsTinyResponsesAndSpentBudget(t *testing.T) {
	var calls int
	client := &http.Client{Transport: qualityRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		status, length := 200, int64(11)
		if req.Method == http.MethodHead {
			status, length = 204, 0
		}
		return &http.Response{StatusCode: status, ContentLength: length, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(make([]byte, length)))}, nil
	})}
	if _, _, _, _, err := measureThroughput(context.Background(), client, func() bool { return true }); err == nil {
		t.Fatal("an exit-IP response was mistaken for a speed download")
	}
	calls = 0
	if _, _, _, _, err := measureThroughput(context.Background(), client, func() bool { return false }); err == nil || calls != 0 {
		t.Fatal("spent budget still generated network traffic")
	}
}
