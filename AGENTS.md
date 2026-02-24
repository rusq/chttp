# AGENTS.md

## Project Snapshot
- Module: `github.com/rusq/chttp`
- Goal: provide a thin wrapper around `net/http.Client` with:
  - cookie jar initialization for a given domain
  - pluggable/custom transport hooks
  - optional per-request `User-Agent` injection
  - optional uTLS transport with Chrome ClientHello emulation

## Current Repository Structure
- `chttp.go`: public API for client construction and helper utilities.
- `Makefile`: convenience targets for regular and integration test runs.
- `transport/transport.go`: transport wrapper implementing pre/post request hooks.
- `transport/utls.go`: uTLS-backed transport with default Chrome hello and optional custom ClientHello signature.
- `chttp_test.go`: table-driven tests for `WithUserAgent`, cookie jar/domain handling, and helper utilities.
- `transport/transport_test.go`: table-driven tests for `FuncTransport` callback behavior.
- `transport/utls_test.go`: table-driven tests for uTLS transport and user-agent behavior.
- `transport/utls_integration_test.go`: opt-in external HTTPS connectivity integration tests, with optional debug HTML output (`TEST_DEBUG=1`).
- `README.md`: usage overview.

## Verified Current State
- `go test ./...` passes.
- `make test` and `make test_all` are available for race+coverage runs.
- Constructor naming is consistent:
  - `chttp.New` uses `transport.NewFuncTransport` by default.
- `CookiesToPtr` now correctly returns the populated slice.
- README usage now matches API (`New` returns `(*http.Client, error)`).
- `WithUTLS` option is available; when enabled, `New` uses `transport.UTLSTransport`.
- `transport.UTLSTransport` defaults to `utls.HelloChrome_Auto` and supports:
  - `WithClientHelloID(...)` for predefined signatures
  - `WithCustomClientHelloSpec(...)` for custom signatures
  - `WithUserAgent(...)` for transport-level UA injection

## Design Learnings
- `NewWithTransport(cookieDomain, cookies, rt)`:
  - creates `cookiejar.Jar` with `publicsuffix.List`
  - parses `cookieDomain` as URL
  - seeds provided cookies into jar for that URL
  - returns an `http.Client` with configured jar + transport
- Options pattern:
  - `Option` mutates internal config
  - `WithUserAgent` injects a static `User-Agent` through `BeforeReq` (default transport path)
  - `WithUTLS` switches to uTLS transport; UA is applied via `UTLSTransport.WithUserAgent`
- `transport.FuncTransport`:
  - wraps an inner `http.RoundTripper` (`http.DefaultTransport` fallback)
  - runs `BeforeReq(req)` before request dispatch
  - runs `AfterReq(resp, req)` only on successful round-trip (no transport error)
- `transport.UTLSTransport`:
  - performs HTTPS handshake via `utls.UClient`
  - emulates Chrome hello by default (`utls.HelloChrome_Auto`)
  - supports HTTP/2 if ALPN negotiates `h2`, otherwise falls back to HTTP/1.1

## Remaining Gaps / Risks
- Core behavior is covered by table-driven tests and opt-in external integration tests, but proxy/TLS corner cases are still untested.
- No benchmarks yet for transport wrapper overhead.

## Practical Engineering Notes
- Recommended regression command:
  - `go test ./...`
- Make targets:
  - `make test` (unit tests with race+coverage)
  - `make test_all` (integration tests enabled with race+coverage)
- To run external connectivity integration tests:
  - `CHTTP_RUN_INTEGRATION_TESTS=1 go test ./transport -run ExternalHTTPSIntegration`
  - optional debug mode: `CHTTP_RUN_INTEGRATION_TESTS=1 TEST_DEBUG=1 go test ./transport -run ExternalHTTPSIntegration`
- Existing table-driven coverage now includes:
  1. cookie jar set/read and outbound cookie emission
  2. before/after transport callback execution
  3. invalid `cookieDomain` handling
  4. helper behavior for `CookiesToPtr` and `Must`
  5. uTLS round-trip (default + custom hello spec)
  6. uTLS transport-level user-agent injection

## Conventions Observed
- Standard-library-first implementation with small API surface.
- Minimal abstraction; behavior composed through transport + options.
- Code style favors concise function docs and straightforward control flow.
