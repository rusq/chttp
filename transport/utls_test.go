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
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

type dialFunc func(context.Context, string, string) (net.Conn, error)

func (f dialFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
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

// connectInfo records what a test proxy received in a CONNECT request.
type connectInfo struct {
	target string
	auth   string
}

// newConnectProxy starts an HTTP proxy that answers CONNECT with status and,
// on 200, tunnels to the requested target.
func newConnectProxy(t *testing.T, status string, gotCh chan<- connectInfo) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCh <- connectInfo{target: r.Host, auth: r.Header.Get("Proxy-Authorization")}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		if !strings.HasPrefix(status, "200 ") {
			fmt.Fprintf(conn, "HTTP/1.1 %s\r\n\r\n", status)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			return
		}
		defer upstream.Close()
		fmt.Fprintf(conn, "HTTP/1.1 %s\r\n\r\n", status)
		go func() {
			_, _ = io.Copy(upstream, conn)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(conn, upstream)
	}))
}

func TestUTLSTransport_Proxy(t *testing.T) {
	const wantAuth = "Basic dXNlcjpwYXNz" // user:pass

	tests := []struct {
		name    string
		status  string
		wantErr string
	}{
		{
			name:   "tunnel",
			status: "200 Connection established",
		},
		{
			name:    "proxy refuses",
			status:  "407 Proxy Authentication Required",
			wantErr: "407 Proxy Authentication Required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("ok"))
			}))
			defer srv.Close()

			gotCh := make(chan connectInfo, 1)
			proxy := newConnectProxy(t, tc.status, gotCh)
			defer proxy.Close()

			proxyURL, err := url.Parse(proxy.URL)
			if err != nil {
				t.Fatalf("parse proxy URL: %v", err)
			}
			proxyURL.User = url.UserPassword("user", "pass")

			tr := NewUTLSTransport(&utls.Config{InsecureSkipVerify: true}).WithProxy(http.ProxyURL(proxyURL))
			defer tr.Close()

			cl := &http.Client{Transport: tr}
			resp, err := cl.Get(srv.URL)
			if tc.wantErr != "" {
				if err == nil {
					resp.Body.Close()
					t.Fatalf("want error containing %q, got none", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("request failed: %v", err)
				}
				b, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				if string(b) != "ok" {
					t.Fatalf("unexpected body: %q", string(b))
				}
			}

			got := <-gotCh
			if want := srv.Listener.Addr().String(); got.target != want {
				t.Fatalf("CONNECT target mismatch: want %q, got %q", want, got.target)
			}
			if got.auth != wantAuth {
				t.Fatalf("Proxy-Authorization mismatch: want %q, got %q", wantAuth, got.auth)
			}
		})
	}
}

func TestUTLSTransport_ProxyRouteReuse(t *testing.T) {
	srv, _ := newTestH2Server(t)
	connects := []chan connectInfo{make(chan connectInfo, 4), make(chan connectInfo, 4)}
	proxies := make([]*url.URL, len(connects))
	for i, ch := range connects {
		proxy := newConnectProxy(t, "200 Connection established", ch)
		t.Cleanup(proxy.Close)
		var err error
		proxies[i], err = url.Parse(proxy.URL)
		if err != nil {
			t.Fatal(err)
		}
	}
	proxies[0].User = url.UserPassword("user", "pass")
	otherCredentials := *proxies[0]
	otherCredentials.User = url.UserPassword("user", "other")

	var selected *url.URL
	var selectionErr error
	var selections int
	tr := NewUTLSTransport(&utls.Config{InsecureSkipVerify: true}).WithProxy(func(*http.Request) (*url.URL, error) {
		selections++
		return selected, selectionErr
	})
	defer tr.Close()
	var dials []string
	d := &net.Dialer{}
	tr.dialer = dialFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials = append(dials, addr)
		return d.DialContext(ctx, network, addr)
	})
	cl := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	steps := []struct {
		name      string
		proxy     *url.URL
		wantDials int
		wantAddr  string
		// A nonnegative index identifies a proxy that must receive a new CONNECT.
		connectProxy int
		wantAuth     string
	}{
		{"direct", nil, 1, srv.Listener.Addr().String(), -1, ""},
		{"first proxy", proxies[0], 2, proxies[0].Host, 0, "Basic dXNlcjpwYXNz"},
		{"second proxy", proxies[1], 3, proxies[1].Host, 1, ""},
		{"different credentials", &otherCredentials, 4, proxies[0].Host, 0, "Basic dXNlcjpvdGhlcg=="},
		{"reuse first proxy", proxies[0], 4, "", -1, ""},
		{"reuse direct", nil, 4, "", -1, ""},
		{"reuse second proxy", proxies[1], 4, "", -1, ""},
		{"reuse different credentials", &otherCredentials, 4, "", -1, ""},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			selected = step.proxy
			before := selections
			resp, err := cl.Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.ProtoMajor != 2 {
				t.Fatalf("want HTTP/2, got %s", resp.Proto)
			}
			if selections != before+1 {
				t.Fatalf("want one proxy selection per request, got %d", selections-before)
			}
			if len(dials) != step.wantDials {
				t.Fatalf("want %d dials, got %d", step.wantDials, len(dials))
			}
			if step.wantAddr != "" && dials[len(dials)-1] != step.wantAddr {
				t.Fatalf("want dial to %s, got %s", step.wantAddr, dials[len(dials)-1])
			}
			for i, ch := range connects {
				select {
				case got := <-ch:
					if i != step.connectProxy || got.target != srv.Listener.Addr().String() || got.auth != step.wantAuth {
						t.Fatalf("unexpected CONNECT at proxy %d: %+v", i, got)
					}
				default:
					if i == step.connectProxy {
						t.Fatalf("missing CONNECT at proxy %d", i)
					}
				}
			}
		})
		if t.Failed() {
			return
		}
	}

	selectorErr := errors.New("proxy selection failed")
	for _, tc := range []struct {
		name    string
		proxy   *url.URL
		err     error
		wantErr error
		wantMsg string
	}{
		{name: "selector error with pooled route", proxy: proxies[0], err: selectorErr, wantErr: selectorErr},
		{name: "unsupported proxy with pooled origin", proxy: &url.URL{Scheme: "https", Host: proxies[0].Host}, wantMsg: `unsupported proxy scheme "https"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, selectionErr = tc.proxy, tc.err
			before := selections
			resp, err := cl.Get(srv.URL)
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) || (tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg)) {
				t.Fatalf("unexpected error: %v", err)
			}
			if selections != before+1 || len(dials) != 4 {
				t.Fatalf("want one selection and no additional dials, got %d selections and %d total dials", selections-before, len(dials))
			}
		})
	}
}

// connectPhaseConn signals when CONNECT reaches the selected I/O operation.
type connectPhaseConn struct {
	net.Conn
	reading bool
	started chan struct{}
	once    sync.Once
}

func (c *connectPhaseConn) Read(p []byte) (int, error) {
	if c.reading {
		c.once.Do(func() { close(c.started) })
	}
	return c.Conn.Read(p)
}

func (c *connectPhaseConn) Write(p []byte) (int, error) {
	if !c.reading {
		c.once.Do(func() { close(c.started) })
	}
	return c.Conn.Write(p)
}

func TestConnectTunnel_ContextErrors(t *testing.T) {
	for _, viaClient := range []bool{false, true} {
		for _, reading := range []bool{false, true} {
			for _, deadline := range []bool{false, true} {
				t.Run(fmt.Sprintf("client=%t/read=%t/deadline=%t", viaClient, reading, deadline), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					wantErr := context.Canceled
					if deadline {
						cancel()
						ctx, cancel = context.WithTimeout(context.Background(), time.Second)
						wantErr = context.DeadlineExceeded
					}
					defer cancel()
					client, peer := net.Pipe()
					defer client.Close()
					defer peer.Close()
					conn := &connectPhaseConn{Conn: client, reading: reading, started: make(chan struct{})}
					peerDone := make(chan error, 1)
					if reading {
						go func() {
							_, err := http.ReadRequest(bufio.NewReader(peer))
							if err == nil {
								_, err = io.Copy(io.Discard, peer)
							}
							peerDone <- err
						}()
					}
					proxyURL := &url.URL{Scheme: "http", Host: "proxy.invalid:80"}
					tr := NewUTLSTransport(nil).WithProxy(http.ProxyURL(proxyURL))
					defer tr.Close()
					tr.dialer = dialFunc(func(context.Context, string, string) (net.Conn, error) {
						return conn, nil
					})
					done := make(chan error, 1)
					go func() {
						if viaClient {
							req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
							if err != nil {
								done <- err
								return
							}
							resp, err := (&http.Client{Transport: tr}).Do(req)
							if resp != nil {
								resp.Body.Close()
							}
							done <- err
							return
						}
						done <- connectTunnel(ctx, conn, proxyURL, "example.com:443")
					}()
					select {
					case <-conn.started:
					case <-time.After(5 * time.Second):
						t.Fatal("CONNECT did not reach the expected I/O operation")
					}
					if !deadline {
						cancel()
					}
					select {
					case err := <-done:
						if !errors.Is(err, wantErr) {
							t.Fatalf("want %v, got %v", wantErr, err)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("CONNECT did not unblock after context cancellation")
					}
					if reading {
						select {
						case err := <-peerDone:
							if err != nil {
								t.Fatalf("proxy read: %v", err)
							}
						case <-time.After(5 * time.Second):
							t.Fatal("proxy connection was not closed")
						}
					} else {
						_ = peer.SetReadDeadline(time.Now().Add(time.Second))
						if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
							t.Fatalf("want closed proxy connection, got %v", err)
						}
					}
				})
			}
		}
	}
}

func TestConnectTunnel_NetworkErrors(t *testing.T) {
	for _, reading := range []bool{false, true} {
		t.Run(fmt.Sprintf("read=%t", reading), func(t *testing.T) {
			client, peer := net.Pipe()
			defer client.Close()
			defer peer.Close()
			wantErr := io.ErrClosedPipe
			if reading {
				wantErr = io.ErrUnexpectedEOF
				go func() {
					_, _ = http.ReadRequest(bufio.NewReader(peer))
					peer.Close()
				}()
			} else {
				peer.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := connectTunnel(ctx, client, &url.URL{Scheme: "http", Host: "proxy.invalid:80"}, "example.com:443")
			if !errors.Is(err, wantErr) || ctx.Err() != nil {
				t.Fatalf("want network error %v with active context, got %v (context: %v)", wantErr, err, ctx.Err())
			}
		})
	}
}

// helloSummary holds the ClientHello fields that identify a client. GREASE
// values are dropped and extensions sorted, because Chrome randomises both.
type helloSummary struct {
	Versions   []uint16
	Ciphers    []uint16
	Extensions []uint16
	Curves     []tls.CurveID
	SigSchemes []tls.SignatureScheme
	Protos     []string
}

func isGREASE(v uint16) bool {
	return v&0x0f0f == 0x0a0a && v>>8 == v&0xff
}

func withoutGREASE[T ~uint16](vs []T) []T {
	var out []T
	for _, v := range vs {
		if !isGREASE(uint16(v)) {
			out = append(out, v)
		}
	}
	return out
}

func summariseHello(chi *tls.ClientHelloInfo) helloSummary {
	exts := withoutGREASE(chi.Extensions)
	slices.Sort(exts)
	return helloSummary{
		Versions:   withoutGREASE(chi.SupportedVersions),
		Ciphers:    withoutGREASE(chi.CipherSuites),
		Extensions: exts,
		Curves:     withoutGREASE(chi.SupportedCurves),
		SigSchemes: withoutGREASE(chi.SignatureSchemes),
		Protos:     chi.SupportedProtos,
	}
}

func TestUTLSTransport_ProxyPreservesClientHello(t *testing.T) {
	hellos := make(chan helloSummary, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			hellos <- summariseHello(chi)
			return nil, nil
		},
	}
	srv.StartTLS()
	defer srv.Close()

	proxy := newConnectProxy(t, "200 Connection established", make(chan connectInfo, 1))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}

	hello := func(rt http.RoundTripper) helloSummary {
		t.Helper()
		resp, err := (&http.Client{Transport: rt}).Get(srv.URL)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
		return <-hellos
	}

	directTr := NewUTLSTransport(&utls.Config{InsecureSkipVerify: true}).WithProxy(nil)
	defer directTr.Close()
	proxiedTr := NewUTLSTransport(&utls.Config{InsecureSkipVerify: true}).WithProxy(http.ProxyURL(proxyURL))
	defer proxiedTr.Close()
	stdlibTr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer stdlibTr.CloseIdleConnections()

	direct := hello(directTr)
	proxied := hello(proxiedTr)
	stdlib := hello(stdlibTr)

	if !reflect.DeepEqual(direct, proxied) {
		t.Fatalf("proxy changed the ClientHello:\ndirect:  %+v\nproxied: %+v", direct, proxied)
	}
	// Guards against a summary too coarse to tell clients apart.
	if reflect.DeepEqual(direct, stdlib) {
		t.Fatalf("uTLS ClientHello matches crypto/tls: %+v", direct)
	}
}
