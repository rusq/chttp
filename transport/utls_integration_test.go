package transport

import (
	"net/http"
	"os"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

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
	}

	for _, tc := range tests {
		tc := tc
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
		})
	}
}
