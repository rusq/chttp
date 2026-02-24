# chttp

[![Go Reference](https://pkg.go.dev/badge/github.com/rusq/chttp.svg)][1]

Cooked HTTP — a standard golang HTTP Client wrapper that adds a cookie jar
with user-defined cookies, and a customised transport.

Features:
- Cookie jar initialization and seeding for a target domain.
- Option-based request customization (`WithUserAgent`).
- Optional uTLS transport (`WithUTLS`) with Chrome ClientHello emulation by default.
- Transport hooks via `transport.FuncTransport` (`BeforeReq` / `AfterReq`).

Simple usage:
```go
import "github.com/rusq/chttp"

func getSomething() error {
	cookies := readFromFile()
	cl, err := chttp.New("https://slack.com", cookies)
	if err != nil {
		return err
	}

	resp, err := cl.Get("url") // executes with cookies from the jar
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// do something with resp
	return nil
}
```

uTLS usage:
```go
import (
	"github.com/rusq/chttp"
	utls "github.com/refraction-networking/utls"
)

func getWithUTLS() error {
	cl, err := chttp.New(
		"https://example.com",
		nil,
		chttp.WithUTLS(&utls.Config{}),
		chttp.WithUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)
	if err != nil {
		return err
	}

	resp, err := cl.Get("https://example.com")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
```

Integration tests:
- Unit tests: `go test ./...`
- External HTTPS integration tests (opt-in):
  - `CHTTP_RUN_INTEGRATION_TESTS=1 go test ./transport -run ExternalHTTPSIntegration`

See [package documentation][1] if you'd like to read more. 

[1]: https://pkg.go.dev/github.com/rusq/chttp
