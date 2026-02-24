package transport

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// UTLSTransport is an http.RoundTripper using uTLS for HTTPS handshakes.
//
// It emulates Chrome's ClientHello by default. A custom TLS signature can be
// provided via CustomClientHelloSpec.
type UTLSTransport struct {
	dialer                *net.Dialer
	tlsConfig             *utls.Config
	clientHelloID         utls.ClientHelloID
	customClientHelloSpec *utls.ClientHelloSpec
	userAgent             string
	h2                    *http2.Transport
	http                  http.RoundTripper
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
		h2:            &http2.Transport{},
		http:          http.DefaultTransport,
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

	conn, err := t.dialer.DialContext(req.Context(), "tcp", addr)
	if err != nil {
		return nil, err
	}

	resp, err := t.roundTripTLS(r, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return resp, nil
}

func (t *UTLSTransport) roundTripTLS(req *http.Request, rawConn net.Conn) (*http.Response, error) {
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
	err1 := c.ReadCloser.Close()
	err2 := c.conn.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
