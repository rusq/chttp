// SPDX-License-Identifier: AGPL-3.0-or-later

package chttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	utls "github.com/refraction-networking/utls"
	"github.com/rusq/chttp/transport"
)

func TestUserAgent(t *testing.T) {
	tests := []struct {
		name string
		ua   string
	}{
		{name: "simple value", ua: "custom UA"},
		{name: "product style value", ua: "my-agent/1.2.3"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(hdrUserAgent); got != tc.ua {
					t.Errorf("user agent: want: %q != got %q", tc.ua, got)
					http.Error(w, "fail", http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			cl, err := New(srv.URL, nil, WithUserAgent(tc.ua))
			if err != nil {
				t.Fatalf("unexpected init error: %s", err)
			}
			resp, err := cl.Get(srv.URL)
			if err != nil {
				t.Fatalf("unexpected request error: %s", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("invalid status code: %d", resp.StatusCode)
			}
		})
	}
}

func TestNew_WithUTLSAndUserAgent(t *testing.T) {
	tests := []struct {
		name   string
		ua     string
		wantUA string
	}{
		{name: "utls with user-agent", ua: "custom UA", wantUA: "custom UA"},
		{name: "utls without user-agent", ua: "", wantUA: "Go-http-client/1.1"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(hdrUserAgent); got != tc.wantUA {
					t.Errorf("user agent: want: %q != got %q", tc.wantUA, got)
					http.Error(w, "fail", http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			cl, err := New(srv.URL, nil, WithUTLS(&utls.Config{InsecureSkipVerify: true}), WithUserAgent(tc.ua))
			if err != nil {
				t.Fatalf("unexpected init error: %s", err)
			}
			if _, ok := cl.Transport.(*transport.UTLSTransport); !ok {
				t.Fatalf("transport type mismatch: got %T", cl.Transport)
			}

			resp, err := cl.Get(srv.URL)
			if err != nil {
				t.Fatalf("unexpected request error: %s", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("invalid status code: %d", resp.StatusCode)
			}
		})
	}
}

func TestNew_SetsCookiesAndEmitsOnRequests(t *testing.T) {
	tests := []struct {
		name    string
		cookies []*http.Cookie
	}{
		{
			name: "single cookie",
			cookies: []*http.Cookie{{
				Name:  "session",
				Value: "abc",
				Path:  "/",
			}},
		},
		{
			name: "multiple cookies",
			cookies: []*http.Cookie{
				{Name: "session", Value: "abc", Path: "/"},
				{Name: "team", Value: "ops", Path: "/"},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			expected := make(map[string]string, len(tc.cookies))
			for _, c := range tc.cookies {
				expected[c.Name] = c.Value
			}

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for name, value := range expected {
					got, err := r.Cookie(name)
					if err != nil {
						t.Errorf("missing cookie %q: %v", name, err)
						http.Error(w, "missing cookie", http.StatusBadRequest)
						return
					}
					if got.Value != value {
						t.Errorf("cookie %q: want %q, got %q", name, value, got.Value)
						http.Error(w, "bad cookie value", http.StatusBadRequest)
						return
					}
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			cl, err := New(srv.URL, tc.cookies)
			if err != nil {
				t.Fatalf("unexpected init error: %s", err)
			}

			u, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatalf("unexpected parse error: %s", err)
			}
			gotJar := cl.Jar.Cookies(u)
			for name, value := range expected {
				found := false
				for _, c := range gotJar {
					if c.Name == name && c.Value == value {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("cookie jar missing %q=%q", name, value)
				}
			}

			resp, err := cl.Get(srv.URL)
			if err != nil {
				t.Fatalf("unexpected request error: %s", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("invalid status code: %d", resp.StatusCode)
			}
		})
	}
}

func TestNewWithTransport_InvalidCookieDomain(t *testing.T) {
	tests := []struct {
		name   string
		domain string
	}{
		{name: "missing scheme delimiter", domain: "://bad"},
		{name: "malformed ipv6 host", domain: "http://[::1"},
		{name: "invalid url escape", domain: "http://%zz"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cl, err := NewWithTransport(tc.domain, nil, http.DefaultTransport)
			if err == nil {
				t.Fatalf("expected error, got client: %#v", cl)
			}
		})
	}
}

func TestCookiesToPtr(t *testing.T) {
	tests := []struct {
		name    string
		cookies []http.Cookie
	}{
		{name: "empty", cookies: nil},
		{name: "multiple", cookies: []http.Cookie{{Name: "a"}, {Name: "b"}}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := CookiesToPtr(tc.cookies)
			if len(got) != len(tc.cookies) {
				t.Fatalf("invalid length: want %d, got %d", len(tc.cookies), len(got))
			}
			for i := range tc.cookies {
				if got[i] != &tc.cookies[i] {
					t.Fatalf("index %d does not point to original cookie", i)
				}
			}
		})
	}
}

func TestMust(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantPanic bool
	}{
		{name: "no error", err: nil, wantPanic: false},
		{name: "with error", err: errors.New("boom"), wantPanic: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cl := &http.Client{}
			panicked := didPanic(func() {
				_ = Must(cl, tc.err)
			})
			if panicked != tc.wantPanic {
				t.Fatalf("panic: want %t, got %t", tc.wantPanic, panicked)
			}
			if !tc.wantPanic {
				got := Must(cl, nil)
				if got != cl {
					t.Fatal("Must should return original client")
				}
			}
		})
	}
}

func didPanic(fn func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	fn()
	return panicked
}
