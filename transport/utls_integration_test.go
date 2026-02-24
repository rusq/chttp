// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

var testDebug = os.Getenv("TEST_DEBUG") == "1"

func TestUTLSTransport_ExternalHTTPSIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if os.Getenv("CHTTP_RUN_INTEGRATION_TESTS") == "" {
		t.Skip("set CHTTP_RUN_INTEGRATION_TESTS=1 to run external integration tests")
	}

	tests := []struct {
		name string
		url  string
	}{
		{name: "example", url: "https://example.com"},
		{name: "cloudflare", url: "https://www.cloudflare.com"},
		{name: "browserleaks", url: "https://browserleaks.com/tls"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewUTLSTransport(&utls.Config{}).
				WithClientHelloID(utls.HelloChrome_Auto).
				WithUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

			cl := &http.Client{
				Transport: tr,
				Timeout:   20 * time.Second,
			}

			resp, err := cl.Get(tc.url)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode > 499 {
				t.Fatalf("unexpected status code %d", resp.StatusCode)
			}
			if filename := extractHost(t, tc.url); testDebug && filename != "" {
				bytes, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("error reading the body: %s", err)
				}
				fullname := filename + ".html"
				if err := os.WriteFile(fullname, bytes, 0o666); err != nil {
					t.Errorf("error writing file %q: %s", fullname, err)
				}
			}
		})
	}
}

func extractHost(t *testing.T, uri string) string {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Errorf("error extracting hostname from %s", uri)
		return ""
	}
	return u.Hostname()
}
