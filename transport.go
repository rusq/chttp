package chttp

import "net/http"

// Transport is a simple wrapper for http.RoundTripper to do something before
// and after RoundTrip.
type Transport struct {
	tr http.RoundTripper
	// BeforeReq is called before the request.
	BeforeReq func(req *http.Request)
	// AfterReq is called after the request.
	AfterReq func(resp *http.Response, req *http.Request)
}

// NewTransport returns a new Transport.
func NewTransport(tr http.RoundTripper) *Transport {
	t := &Transport{}
	if tr == nil {
		tr = http.DefaultTransport
	}
	t.tr = tr
	return t
}

// RoundTrip implements http.RoundTripper.  It calls BeforeReq before the
// request and AfterReq after the request.
func (t *Transport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
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
