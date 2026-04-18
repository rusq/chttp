// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	utls "github.com/refraction-networking/utls"
)

// dialCounter wraps a net.Dialer and counts the number of DialContext calls.
type dialCounter struct {
	inner *net.Dialer
	count atomic.Int64
}

func (d *dialCounter) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.count.Add(1)
	return d.inner.DialContext(ctx, network, addr)
}

func TestUTLSTransport_RoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*UTLSTransport) error
	}{
		{
			name:      "default chrome hello",
			configure: func(*UTLSTransport) error { return nil },
		},
		{
			name: "custom hello spec",
			configure: func(tr *UTLSTransport) error {
				spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
				if err != nil {
					return err
				}
				tr.WithCustomClientHelloSpec(&spec)
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			}))
			defer srv.Close()

			tr := NewUTLSTransport(&utls.Config{InsecureSkipVerify: true})
			if err := tc.configure(tr); err != nil {
				t.Fatalf("configure transport: %v", err)
			}

			cl := &http.Client{Transport: tr}
			resp, err := cl.Get(srv.URL)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("invalid status code: want %d got %d", http.StatusOK, resp.StatusCode)
			}

			b, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(b) != "ok" {
				t.Fatalf("unexpected body: %q", string(b))
			}
		})
	}
}

func TestUTLSTransport_ConnectionReuse(t *testing.T) {
	var (
		mu            sync.Mutex
		reqCount      int
		protoVersions = make(map[string]int)
	)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqCount++
		protoVersions[r.Proto]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tr := NewUTLSTransport(&utls.Config{InsecureSkipVerify: true})
	defer tr.Close()

	// Wrap the dialer to count TCP connections (each represents a TLS handshake).
	dc := &dialCounter{inner: tr.dialer.(*net.Dialer)}
	tr.dialer = dc

	cl := &http.Client{Transport: tr}

	// Make multiple sequential requests to the same server.
	for range 5 {
		resp, err := cl.Get(srv.URL)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	if reqCount != 5 {
		t.Fatalf("expected 5 requests, got %d", reqCount)
	}
	if protoVersions["HTTP/2.0"] != 5 {
		t.Fatalf("expected all 5 requests over HTTP/2, got protocol distribution: %v", protoVersions)
	}
	if dials := dc.count.Load(); dials != 1 {
		t.Fatalf("expected 1 TCP dial (connection reuse), got %d", dials)
	}
}

func TestUTLSTransport_WithUserAgent(t *testing.T) {
	const wantUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0"
	uaCh := make(chan string, 1)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uaCh <- r.UserAgent()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := NewUTLSTransport(&utls.Config{InsecureSkipVerify: true}).WithUserAgent(wantUA)

	cl := &http.Client{Transport: tr}
	resp, err := cl.Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invalid status code: want %d got %d", http.StatusOK, resp.StatusCode)
	}

	if got := <-uaCh; got != wantUA {
		t.Fatalf("user agent mismatch: want %q, got %q", wantUA, got)
	}
}
