// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"sync"

	"golang.org/x/net/http2"
)

// connPool manages pooled HTTP/2 client connections keyed by host:port.
// A single http2.ClientConn can multiplex many concurrent requests.
type connPool struct {
	mu      sync.Mutex
	h2Conns map[string]*http2.ClientConn
	closed  bool
}

func newConnPool() *connPool {
	return &connPool{
		h2Conns: make(map[string]*http2.ClientConn),
	}
}

// getH2 returns a cached HTTP/2 client connection for addr if one exists
// and can still accept new requests. Stale entries are evicted.
func (p *connPool) getH2(addr string) (*http2.ClientConn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, false
	}

	cc, ok := p.h2Conns[addr]
	if !ok {
		return nil, false
	}
	if !cc.CanTakeNewRequest() {
		delete(p.h2Conns, addr)
		return nil, false
	}
	return cc, true
}

// putH2 stores an HTTP/2 client connection for addr. If a usable connection
// already exists (e.g. from a concurrent dial race), the existing one is kept
// and the new one is returned so the caller can close it.
func (p *connPool) putH2(addr string, cc *http2.ClientConn) (existing *http2.ClientConn, stored bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, false
	}

	if prev, ok := p.h2Conns[addr]; ok && prev.CanTakeNewRequest() {
		// A usable connection already exists; caller should use it and
		// close the new one.
		return prev, false
	}

	p.h2Conns[addr] = cc
	return nil, true
}

// removeH2 removes the cached connection for addr, but only if it is the
// same connection as cc. This prevents a stale request from evicting a
// newer healthy connection that another goroutine has already stored.
func (p *connPool) removeH2(addr string, cc *http2.ClientConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.h2Conns[addr] == cc {
		delete(p.h2Conns, addr)
	}
}

// Close closes all pooled connections and marks the pool as closed.
func (p *connPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	for addr, cc := range p.h2Conns {
		cc.Close()
		delete(p.h2Conns, addr)
	}
	return nil
}
