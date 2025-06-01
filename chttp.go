// Package chttp (Cooked HTTP) provides a wrapper around http.Client with
// cookies, that are added to each request.  It also allows to use custom
// Transport, which wraps the default transport and calls the user-defined
// function before and after the request.
package chttp

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"

	"golang.org/x/net/publicsuffix"
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
}

type Option func(*options)

// WithUserAgent allows to set the User-Agent on each request.
func WithUserAgent(ua string) Option {
	return func(o *options) {
		o.userAgent = ua
	}
}

// New returns the HTTP client with cookies and default transport.
func New(cookieDomain string, cookies []*http.Cookie, opts ...Option) (*http.Client, error) {
	var opt options
	for _, o := range opts {
		o(&opt)
	}

	tr := NewTransport(nil)
	if opt.userAgent != "" {
		tr = NewTransport(http.DefaultTransport)
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
	return nil
}

// Must is a helper function to panic on error.
func Must(cl *http.Client, err error) *http.Client {
	if err != nil {
		panic(err)
	}
	return cl
}
