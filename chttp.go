// Package chttp (Cooked HTTP) provides a wrapper around http.Client with
// cookies.  It also allows to use custom Transport, which wraps the default
// transport and calls the user-defined function before and after the request.
package chttp

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"

	"golang.org/x/net/publicsuffix"
)

// NewWithTransport inits the HTTP client with cookies.  It allows to use
// the custom Transport.
func NewWithTransport(cookieDomain string, cookies []*http.Cookie, rt http.RoundTripper) *http.Client {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	url, err := url.Parse(cookieDomain)
	if err != nil {
		panic(err) //shouldn't happen
	}
	jar.SetCookies(url, cookies)
	cl := http.Client{
		Jar:       jar,
		Transport: rt,
	}
	return &cl
}

// New returns the HTTP client with cookies and default transport.
func New(cookieDomain string, cookies []*http.Cookie) *http.Client {
	return NewWithTransport(cookieDomain, cookies, NewTransport(nil))
}
