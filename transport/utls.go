// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// dialer is the interface used by UTLSTransport to establish TCP connections.
type dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// UTLSTransport is an http.RoundTripper using uTLS for HTTPS handshakes.
//
// It emulates Chrome's ClientHello by default. A custom TLS signature can be
// provided via CustomClientHelloSpec.
type UTLSTransport struct {
	dialer                dialer
	tlsConfig             *utls.Config
	clientHelloID         utls.ClientHelloID
	customClientHelloSpec *utls.ClientHelloSpec
	userAgent             string
	h2                    *http2.Transport
	http                  http.RoundTripper
	pool                  *connPool
}

// NewUTLSTransport returns a new uTLS transport.
//
// The default fingerprint is utls.HelloChrome_Auto.
func NewUTLSTransport(tlsConfig *utls.Config) *UTLSTransport {
	if tlsConfig == nil {
		tlsConfig = &utls.Config{}
	}

	return &UTLSTransport{
		dialer: &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		tlsConfig:     tlsConfig,
		clientHelloID: utls.HelloChrome_Auto,
		h2: &http2.Transport{
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return nil, errors.New("should not be called") // thanks @korotovsky
			},
		},
		http: http.DefaultTransport,
		pool: newConnPool(),
	}
}

// WithClientHelloID sets a predefined uTLS client hello fingerprint.
func (t *UTLSTransport) WithClientHelloID(id utls.ClientHelloID) *UTLSTransport {
	t.clientHelloID = id
	t.customClientHelloSpec = nil
	return t
}

// WithCustomClientHelloSpec sets a custom uTLS client hello signature.
func (t *UTLSTransport) WithCustomClientHelloSpec(spec *utls.ClientHelloSpec) *UTLSTransport {
	t.customClientHelloSpec = spec
	return t
}

// WithUserAgent sets the User-Agent header for requests sent by this transport.
func (t *UTLSTransport) WithUserAgent(ua string) *UTLSTransport {
	t.userAgent = ua
	return t
}

// RoundTrip implements http.RoundTripper.
func (t *UTLSTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil {
		return nil, fmt.Errorf("request URL is nil")
	}

	r := req.Clone(req.Context())
	if t.userAgent != "" {
		r.Header.Set("User-Agent", t.userAgent)
	}

	if !strings.EqualFold(r.URL.Scheme, "https") {
		return t.http.RoundTrip(r)
	}

	addr := r.URL.Host
	if r.URL.Port() == "" {
		addr += ":443"
	}

	// Try reusing a pooled HTTP/2 connection.
	if cc, ok := t.pool.getH2(addr); ok {
		resp, err := cc.RoundTrip(r)
		if err == nil {
			return resp, nil
		}
		// Connection went bad; remove only if it's still the same entry
		// (another goroutine may have already replaced it with a fresh one).
		t.pool.removeH2(addr, cc)
	}

	conn, err := t.dialer.DialContext(req.Context(), "tcp", addr)
	if err != nil {
		return nil, err
	}

	resp, err := t.roundTripTLS(r, conn, addr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return resp, nil
}

// Close closes idle connections held by this transport.
func (t *UTLSTransport) Close() error {
	return t.pool.Close()
}

func (t *UTLSTransport) roundTripTLS(req *http.Request, rawConn net.Conn, addr string) (*http.Response, error) {
	tlsCfg := t.tlsConfig.Clone()
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = req.URL.Hostname()
	}

	helloID := t.clientHelloID
	if t.customClientHelloSpec != nil {
		helloID = utls.HelloCustom
	}

	uConn := utls.UClient(rawConn, tlsCfg, helloID)
	if t.customClientHelloSpec != nil {
		if err := uConn.ApplyPreset(t.customClientHelloSpec); err != nil {
			return nil, err
		}
	}

	if err := uConn.HandshakeContext(req.Context()); err != nil {
		return nil, err
	}

	if uConn.ConnectionState().NegotiatedProtocol == "h2" {
		cc, err := t.h2.NewClientConn(uConn)
		if err != nil {
			return nil, err
		}
		// Pool the connection. If another goroutine raced and already
		// stored one, use that instead and close our new connection.
		if existing, stored := t.pool.putH2(addr, cc); !stored && existing != nil {
			_ = uConn.Close()
			return existing.RoundTrip(req)
		}
		return cc.RoundTrip(req)
	}

	if err := req.Write(uConn); err != nil {
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(uConn), req)
	if err != nil {
		return nil, err
	}

	resp.Body = &closeConnReadCloser{ReadCloser: resp.Body, conn: uConn}
	return resp, nil
}

type closeConnReadCloser struct {
	io.ReadCloser
	conn net.Conn
}

func (c *closeConnReadCloser) Close() error {
	return errors.Join(c.ReadCloser.Close(), c.conn.Close())
}
