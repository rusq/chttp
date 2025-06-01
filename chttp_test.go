package chttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserAgent(t *testing.T) {
	t.Run("check that User Agent is properly set", func(t *testing.T) {
		const customUA = "custom UA"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get(hdrUserAgent); got != customUA {
				t.Errorf("user agent: want: %q != got %q", customUA, got)
				http.Error(w, "fail", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		cl, err := New(".slack.com", []*http.Cookie{}, WithUserAgent(customUA))
		if err != nil {
			t.Fatalf("unexpected init error: %s", err)
		}
		resp, err := cl.Get(srv.URL + "/")
		if err != nil {
			t.Fatalf("unexpected request error: %s", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("invalid status code: %d", resp.StatusCode)
		}
	})
}
