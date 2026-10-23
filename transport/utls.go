// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	proxy                 func(*http.Request) (*url.URL, error)
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
		proxy:         http.ProxyFromEnvironment,
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

// WithProxy sets the function that picks the proxy for an HTTPS request, as in
// http.Transport.Proxy. The default is http.ProxyFromEnvironment. Requests go
// through an HTTP CONNECT tunnel, so the uTLS ClientHello reaches the server
// unchanged. A nil function disables the proxy. Plain HTTP requests use
// http.DefaultTransport and its own proxy settings.
func (t *UTLSTransport) WithProxy(proxy func(*http.Request) (*url.URL, error)) *UTLSTransport {
	t.proxy = proxy
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

	var proxyURL *url.URL
	if t.proxy != nil {
		var err error
		if proxyURL, err = t.proxy(r); err != nil {
			return nil, err
		}
	}
	key := h2PoolKey{addr: addr}
	if proxyURL != nil {
		if proxyURL.Scheme != "http" {
			return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
		}
		key.proxy = proxyURL.String()
	}

	// Try reusing a pooled HTTP/2 connection.
	if cc, ok := t.pool.getH2(key); ok {
		resp, err := cc.RoundTrip(r)
		if err == nil {
			return resp, nil
		}
		// Connection went bad; remove only if it's still the same entry
		// (another goroutine may have already replaced it with a fresh one).
		t.pool.removeH2(key, cc)
	}

	conn, err := t.dial(r, addr, proxyURL)
	if err != nil {
		return nil, err
	}

	resp, err := t.roundTripTLS(r, conn, key)
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

// dial connects to addr, through a CONNECT tunnel if proxyURL is non-nil.
// The proxy has already been selected and validated before pool lookup.
func (t *UTLSTransport) dial(req *http.Request, addr string, proxyURL *url.URL) (net.Conn, error) {
	ctx := req.Context()
	if proxyURL == nil {
		return t.dialer.DialContext(ctx, "tcp", addr)
	}

	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "80")
	}
	conn, err := t.dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if err := connectTunnel(ctx, conn, proxyURL, addr); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// connectTunnel asks the proxy at the other end of conn to open a tunnel to
// addr.
func connectTunnel(ctx context.Context, conn net.Conn, proxyURL *url.URL, addr string) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	connectReq := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if u := proxyURL.User; u != nil {
		password, _ := u.Password()
		creds := base64.StdEncoding.EncodeToString([]byte(u.Username() + ":" + password))
		connectReq.Header.Set("Proxy-Authorization", "Basic "+creds)
	}
	if err := connectReq.Write(conn); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	// Discarding the buffered reader is safe: the TLS server sends nothing
	// until it receives the ClientHello.
	resp, err := http.ReadResponse(bufio.NewReader(conn), connectReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy CONNECT to %s: %s", addr, resp.Status)
	}
	return nil
}

func (t *UTLSTransport) roundTripTLS(req *http.Request, rawConn net.Conn, key h2PoolKey) (*http.Response, error) {
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
		if existing, stored := t.pool.putH2(key, cc); !stored && existing != nil {
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
