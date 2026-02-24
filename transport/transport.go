// SPDX-License-Identifier: AGPL-3.0-or-later

// Package transport provides various types of transport.
package transport

import "net/http"

// FuncTransport is a simple wrapper for http.RoundTripper to do something before
// and after RoundTrip.
type FuncTransport struct {
	tr http.RoundTripper
	// BeforeReq is called before the request.
	BeforeReq func(req *http.Request)
	// AfterReq is called after the request.
	AfterReq func(resp *http.Response, req *http.Request)
}

// NewFuncTransport returns a new Transport.
func NewFuncTransport(tr http.RoundTripper) *FuncTransport {
	t := &FuncTransport{}
	if tr == nil {
		tr = http.DefaultTransport
	}
	t.tr = tr
	return t
}

// RoundTrip implements http.RoundTripper.  It calls BeforeReq before the
// request and AfterReq after the request.
func (t *FuncTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	if t.BeforeReq != nil {
		t.BeforeReq(req)
	}
	resp, err = t.tr.RoundTrip(req)
	if err != nil {
		return
	}
	if t.AfterReq != nil {
		t.AfterReq(resp, req)
	}
	return
}
