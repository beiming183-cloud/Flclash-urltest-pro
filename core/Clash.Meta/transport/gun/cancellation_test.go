package gun

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func stalledHeadersTransport(t *testing.T) (*Transport, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	headerReceived := make(chan struct{}, 8)
	accepted := make(chan net.Conn, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
		defer conn.Close()
		preface := make([]byte, len(http2.ClientPreface))
		if _, err := io.ReadFull(conn, preface); err != nil {
			return
		}
		framer := http2.NewFramer(conn, conn)
		if err := framer.WriteSettings(); err != nil {
			return
		}
		for {
			frame, err := framer.ReadFrame()
			if err != nil {
				return
			}
			switch frame := frame.(type) {
			case *http2.SettingsFrame:
				if !frame.IsAck() {
					if err := framer.WriteSettingsAck(); err != nil {
						return
					}
				}
			case *http2.HeadersFrame:
				select {
				case headerReceived <- struct{}{}:
				default:
				}
			}
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		select {
		case conn := <-accepted:
			conn.Close()
		default:
		}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("HTTP2 fixture did not stop")
		}
	})
	dialer := &net.Dialer{}
	transport := NewTransport(func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, listener.Addr().String())
	}, nil, &Config{Host: listener.Addr().String()})
	t.Cleanup(func() { transport.Close() })
	return transport, headerReceived
}

func TestCloseCancelsPendingResponseHeaders(t *testing.T) {
	transport, headers := stalledHeadersTransport(t)
	conn, err := transport.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readDone := make(chan error, 1)
	go func() { _, err := conn.Read(make([]byte, 1)); readDone <- err }()
	select {
	case <-headers:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP2 stream did not send headers")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("Closed stream returned no error")
		}
	case <-time.After(time.Second):
		t.Fatal("Closing gRPC stream left response-header read blocked")
	}
}

func TestDeadlineCancelsPendingResponseHeaders(t *testing.T) {
	transport, headers := stalledHeadersTransport(t)
	conn, err := transport.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readDone := make(chan error, 1)
	go func() { _, err := conn.Read(make([]byte, 1)); readDone <- err }()
	select {
	case <-headers:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP2 stream did not send headers")
	}
	if err := conn.SetDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("Expired stream returned no error")
		}
	case <-time.After(time.Second):
		t.Fatal("Expired gRPC stream left response-header read blocked")
	}
}

func TestClosingOneStreamDoesNotCancelAnother(t *testing.T) {
	transport, headers := stalledHeadersTransport(t)
	connections := make([]net.Conn, 0, 2)
	reads := []chan error{make(chan error, 1), make(chan error, 1)}
	for i := range reads {
		conn, err := transport.Dial()
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		defer conn.Close()
		go func(result chan error) { _, err := conn.Read(make([]byte, 1)); result <- err }(reads[i])
		select {
		case <-headers:
		case <-time.After(3 * time.Second):
			t.Fatal("HTTP2 stream did not send headers")
		}
	}
	connections[0].Close()
	select {
	case <-reads[0]:
	case <-time.After(time.Second):
		t.Fatal("First stream was not cancelled")
	}
	select {
	case err := <-reads[1]:
		t.Fatalf("Closing one stream cancelled its sibling: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if transport.ctx.Err() != nil {
		t.Fatal("Closing stream cancelled shared transport owner")
	}
	connections[1].Close()
	select {
	case <-reads[1]:
	case <-time.After(time.Second):
		t.Fatal("Second stream was not cancelled")
	}
	if transport.count.Load() != 0 {
		t.Fatal("Closed streams remained registered")
	}
}
