# AGENTS.md

## Project Snapshot
- Module: `github.com/rusq/chttp`
- Goal: provide a thin wrapper around `net/http.Client` with:
  - cookie jar initialization for a given domain
  - pluggable/custom transport hooks
  - optional per-request `User-Agent` injection

## Current Repository Structure
- `chttp.go`: public API for client construction and helper utilities.
- `transport/transport.go`: transport wrapper implementing pre/post request hooks.
- `chttp_test.go`: table-driven tests for `WithUserAgent`, cookie jar/domain handling, and helper utilities.
- `transport/transport_test.go`: table-driven tests for `FuncTransport` callback behavior.
- `README.md`: usage overview.

## Verified Current State
- `go test ./...` passes.
- Constructor naming is consistent:
  - `chttp.New` now calls `transport.NewFuncTransport`.
- `CookiesToPtr` now correctly returns the populated slice.
- README usage now matches API (`New` returns `(*http.Client, error)`).

## Design Learnings
- `NewWithTransport(cookieDomain, cookies, rt)`:
  - creates `cookiejar.Jar` with `publicsuffix.List`
  - parses `cookieDomain` as URL
  - seeds provided cookies into jar for that URL
  - returns an `http.Client` with configured jar + transport
- Options pattern:
  - `Option` mutates internal config
  - `WithUserAgent` injects a static `User-Agent` through `BeforeReq`
- `transport.FuncTransport`:
  - wraps an inner `http.RoundTripper` (`http.DefaultTransport` fallback)
  - runs `BeforeReq(req)` before request dispatch
  - runs `AfterReq(resp, req)` only on successful round-trip (no transport error)

## Remaining Gaps / Risks
- Core behavior is now covered by table-driven tests, but TLS/cross-domain edge cases are still untested.
- No benchmarks yet for transport wrapper overhead.

## Practical Engineering Notes
- Recommended regression command:
  - `go test ./...`
- Existing table-driven coverage now includes:
  1. cookie jar set/read and outbound cookie emission
  2. before/after transport callback execution
  3. invalid `cookieDomain` handling
  4. helper behavior for `CookiesToPtr` and `Must`

## Conventions Observed
- Standard-library-first implementation with small API surface.
- Minimal abstraction; behavior composed through transport + options.
- Code style favors concise function docs and straightforward control flow.
