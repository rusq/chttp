// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"golang.org/x/net/http2"
)

// newTestH2Server returns a TLS server that speaks HTTP/2 and a function
// that dials it and returns an h2 ClientConn.
func newTestH2Server(t *testing.T) (srv *httptest.Server, dial func() *http2.ClientConn) {
	t.Helper()
	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	h2tr := &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	dial = func() *http2.ClientConn {
		t.Helper()
		conn, err := tls.Dial("tcp", srv.Listener.Addr().String(), &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		})
		if err != nil {
			t.Fatalf("tls.Dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		cc, err := h2tr.NewClientConn(conn)
		if err != nil {
			t.Fatalf("NewClientConn: %v", err)
		}
		return cc
	}
	return srv, dial
}

func TestConnPool_GetPut(t *testing.T) {
	_, dial := newTestH2Server(t)
	p := newConnPool()
	addr := "example.com:443"

	if _, ok := p.getH2(addr); ok {
		t.Fatal("expected empty pool to return nothing")
	}

	cc := dial()
	existing, stored := p.putH2(addr, cc)
	if !stored || existing != nil {
		t.Fatal("expected first put to store successfully")
	}

	got, ok := p.getH2(addr)
	if !ok {
		t.Fatal("expected to get cached connection")
	}
	if got != cc {
		t.Fatal("expected same connection back")
	}
}

func TestConnPool_PutRace(t *testing.T) {
	_, dial := newTestH2Server(t)
	p := newConnPool()
	addr := "example.com:443"

	cc1 := dial()
	p.putH2(addr, cc1)

	// A second put for the same addr should return the existing conn.
	cc2 := dial()
	existing, stored := p.putH2(addr, cc2)
	if stored {
		t.Fatal("expected second put to not store")
	}
	if existing != cc1 {
		t.Fatal("expected existing connection to be returned")
	}
}

func TestConnPool_RemoveIdentityAware(t *testing.T) {
	_, dial := newTestH2Server(t)
	p := newConnPool()
	addr := "example.com:443"

	cc1 := dial()
	p.putH2(addr, cc1)

	// Replace cc1 with cc2 (simulating a goroutine that dialed fresh).
	cc2 := dial()
	// Force-replace: remove cc1, then put cc2.
	p.removeH2(addr, cc1)
	p.putH2(addr, cc2)

	// Now a stale goroutine tries to remove using the old cc1 identity.
	// This must NOT evict cc2.
	p.removeH2(addr, cc1)

	got, ok := p.getH2(addr)
	if !ok {
		t.Fatal("removeH2 with stale identity should not have evicted the newer connection")
	}
	if got != cc2 {
		t.Fatal("expected cc2 to remain in pool")
	}
}

func TestConnPool_Close(t *testing.T) {
	_, dial := newTestH2Server(t)
	p := newConnPool()
	addr := "example.com:443"

	p.putH2(addr, dial())

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, ok := p.getH2(addr); ok {
		t.Fatal("expected closed pool to return nothing")
	}

	_, stored := p.putH2(addr, dial())
	if stored {
		t.Fatal("expected put to closed pool to fail")
	}
}

func TestConnPool_ConcurrentAccess(t *testing.T) {
	_, dial := newTestH2Server(t)
	p := newConnPool()

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		addr := net.JoinHostPort("example.com", "443")
		_ = i
		go func() {
			defer wg.Done()
			cc := dial()
			p.putH2(addr, cc)
			p.getH2(addr)
		}()
	}
	wg.Wait()

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
