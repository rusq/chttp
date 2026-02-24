// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFuncTransport_CallbackExecution(t *testing.T) {
	tests := []struct {
		name      string
		rtErr     error
		wantAfter bool
	}{
		{name: "success calls after", rtErr: nil, wantAfter: true},
		{name: "error skips after", rtErr: errors.New("transport fail"), wantAfter: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var gotReq *http.Request
			baseResp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ok")),
			}

			tr := NewFuncTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				gotReq = req
				if tc.rtErr != nil {
					return nil, tc.rtErr
				}
				return baseResp, nil
			}))

			beforeCalled := false
			afterCalled := false
			var afterReq *http.Request
			var afterResp *http.Response
			tr.BeforeReq = func(req *http.Request) {
				beforeCalled = true
				req.Header.Set("X-Before", "1")
			}
			tr.AfterReq = func(resp *http.Response, req *http.Request) {
				afterCalled = true
				afterResp = resp
				afterReq = req
			}

			req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
			if err != nil {
				t.Fatalf("unexpected request creation error: %v", err)
			}

			resp, err := tr.RoundTrip(req)
			if tc.rtErr != nil {
				if !errors.Is(err, tc.rtErr) {
					t.Fatalf("expected transport error %v, got %v", tc.rtErr, err)
				}
				if resp != nil {
					t.Fatalf("expected nil response on error, got %#v", resp)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected transport error: %v", err)
				}
				defer resp.Body.Close()
				if resp != baseResp {
					t.Fatalf("response mismatch")
				}
			}

			if !beforeCalled {
				t.Fatal("BeforeReq should be called")
			}
			if gotReq == nil || gotReq.Header.Get("X-Before") != "1" {
				t.Fatal("BeforeReq mutation should be visible to wrapped RoundTripper")
			}
			if afterCalled != tc.wantAfter {
				t.Fatalf("AfterReq call mismatch: want %t, got %t", tc.wantAfter, afterCalled)
			}
			if tc.wantAfter {
				if afterReq != req {
					t.Fatal("AfterReq should receive original request")
				}
				if afterResp != baseResp {
					t.Fatal("AfterReq should receive response from wrapped RoundTripper")
				}
			}
		})
	}
}
