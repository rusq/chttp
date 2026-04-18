// SPDX-License-Identifier: AGPL-3.0-or-later

// Package chttp (Cooked HTTP) provides a wrapper around http.Client with
// cookies, that are added to each request.  It also allows to use custom
// Transport, which wraps the default transport and calls the user-defined
// function before and after the request.
package chttp

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/publicsuffix"

	"github.com/rusq/chttp/v2/transport"
)

const (
	hdrUserAgent = "User-Agent"
)

// NewWithTransport inits the HTTP client with cookies.  It allows to use
// the custom Transport.
func NewWithTransport(cookieDomain string, cookies []*http.Cookie, rt http.RoundTripper) (*http.Client, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	url, err := url.Parse(cookieDomain)
	if err != nil {
		return nil, err
	}
	jar.SetCookies(url, cookies)
	cl := &http.Client{
		Jar:       jar,
		Transport: rt,
	}
	return cl, nil
}

type options struct {
	userAgent string
	utls      *utls.Config
}

type Option func(*options)

// WithUserAgent allows to set the User-Agent on each request.
func WithUserAgent(ua string) Option {
	return func(o *options) {
		o.userAgent = ua
	}
}

// WithUTLS enables uTLS transport. By default it emulates Chrome ClientHello.
func WithUTLS(cfg *utls.Config) Option {
	return func(o *options) {
		if cfg == nil {
			o.utls = &utls.Config{}
			return
		}
		o.utls = cfg.Clone()
	}
}

// New returns the HTTP client with cookies and default transport.
func New(cookieDomain string, cookies []*http.Cookie, opts ...Option) (*http.Client, error) {
	var opt options
	for _, o := range opts {
		o(&opt)
	}

	if opt.utls != nil {
		tr := transport.NewUTLSTransport(opt.utls)
		if opt.userAgent != "" {
			tr.WithUserAgent(opt.userAgent)
		}
		return NewWithTransport(cookieDomain, cookies, tr)
	}

	tr := transport.NewFuncTransport(nil)
	if opt.userAgent != "" {
		tr.BeforeReq = func(req *http.Request) {
			req.Header[hdrUserAgent] = []string{opt.userAgent}
		}
	}

	return NewWithTransport(cookieDomain, cookies, tr)
}

// CookiesToPtr is a convenience function that returns the slice with pointers
// to cookies.
func CookiesToPtr(cookies []http.Cookie) []*http.Cookie {
	var ret = make([]*http.Cookie, len(cookies))
	for i := range cookies {
		ret[i] = &cookies[i]
	}
	return ret
}

// Close releases resources held by the client's transport. It is safe to call
// on clients whose transport does not require cleanup (e.g. FuncTransport) —
// in that case it is a no-op.
func Close(cl *http.Client) error {
	if c, ok := cl.Transport.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Must is a helper function to panic on error.
func Must(cl *http.Client, err error) *http.Client {
	if err != nil {
		panic(err)
	}
	return cl
}
