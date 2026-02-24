package transport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	utls "github.com/refraction-networking/utls"
)

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
