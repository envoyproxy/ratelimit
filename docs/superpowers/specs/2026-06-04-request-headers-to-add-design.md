# Design: `RequestHeadersToAdd` Support

**Date:** 2026-06-04  
**Repo:** github.com/envoyproxy/ratelimit  
**Scope:** Open source contribution

## Problem

The ratelimit service currently supports injecting rate limit headers into the **downstream response** via `ResponseHeadersToAdd` (enabled with `LIMIT_RESPONSE_HEADERS_ENABLED`). These headers go back to the caller, not to the upstream service.

The Envoy `RateLimitResponse` proto also has a `RequestHeadersToAdd` field — headers Envoy injects into the **forwarded request** before sending it upstream. The ratelimit service never populates this field today.

Upstream services (e.g. API gateways, sidecars) that sit behind Envoy have no per-request signal from the rate limiter. They cannot make routing decisions based on quota state without this.

## Goal

Add a `RequestHeadersToAdd` feature that mirrors `ResponseHeadersToAdd` exactly: same 3 standard headers (`RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`), same `minimumDescriptor` logic, independently enabled via env vars.

## Non-Goals

- Per-domain or per-descriptor scoping (would require XDS proto changes; deferred)
- Arbitrary custom header injection (out of scope for this change)
- Filtering headers to OVER_LIMIT-only responses (operators can inspect `RateLimit-Remaining: 0`)

## Design

### Settings

Four new env vars added to `src/settings/settings.go`, parallel to the existing response header settings:

| Env var | Default | Purpose |
|---|---|---|
| `LIMIT_REQUEST_HEADERS_ENABLED` | `false` | Master on/off switch |
| `LIMIT_REQUEST_LIMIT_HEADER` | `RateLimit-Limit` | Header name for the limit value |
| `LIMIT_REQUEST_REMAINING_HEADER` | `RateLimit-Remaining` | Header name for remaining count |
| `LIMIT_REQUEST_RESET_HEADER` | `RateLimit-Reset` | Header name for reset seconds |

Default names match the response header defaults. Operators can set different names to distinguish upstream vs downstream headers (e.g. `x-grls-ratelimit-remaining` upstream vs `RateLimit-Remaining` downstream).

The two features (`LIMIT_RESPONSE_HEADERS_ENABLED` and `LIMIT_REQUEST_HEADERS_ENABLED`) are independently controlled — operators can enable one, both, or neither.

### Service struct (`src/service/ratelimit.go`)

Four new fields added to the `service` struct, parallel to `customHeaders*`:

```go
requestHeadersEnabled           bool
requestHeaderLimitHeader        string
requestHeaderRemainingHeader    string
requestHeaderResetHeader        string
```

Wired in `SetConfig()` alongside the existing response header fields (currently lines 94–101).

### Response construction (`shouldRateLimitWorker`)

After the existing `ResponseHeadersToAdd` block (currently lines 258–264), add:

```go
if this.requestHeadersEnabled && minimumDescriptor != nil {
    response.RequestHeadersToAdd = []*core.HeaderValue{
        {Key: this.requestHeaderLimitHeader,      Value: <limit>},
        {Key: this.requestHeaderRemainingHeader,  Value: <remaining>},
        {Key: this.requestHeaderResetHeader,      Value: <reset>},
    }
}
```

The `minimumDescriptor` (the descriptor closest to its limit) is reused as-is. Headers are always populated when enabled, regardless of whether the overall response is `OK` or `OVER_LIMIT` — mirroring response header behavior exactly.

### Tests (`test/service/ratelimit_test.go`)

Parallel test cases for `RequestHeadersToAdd` alongside the existing response header tests:

1. **Headers present when enabled + within limit** — `RequestHeadersToAdd` populated correctly
2. **Headers present when enabled + over limit** — `RequestHeadersToAdd` populated correctly
3. **Headers absent when disabled** — `RequestHeadersToAdd` is nil
4. **Both enabled simultaneously with different header names** — `RequestHeadersToAdd` and `ResponseHeadersToAdd` independently populated with their respective configured names; no interference

### README

Add a `Global Rate Limit Request Headers` section immediately after the existing `Global Rate Limit Response Headers` section, documenting the 4 env vars and the use case: upstream services behind Envoy can read these headers to make per-request routing decisions (e.g. skip hot path on quota exhaustion) without the rate limit service blocking the request.

## Backwards Compatibility

Fully additive. `LIMIT_REQUEST_HEADERS_ENABLED` defaults to `false`. No YAML schema changes. No XDS proto changes. Existing deployments see no behavior change.

## Future Work

Per-domain scoping would require adding a field to `RateLimitConfig` in the XDS proto (`api/ratelimit/config/ratelimit/v3/rls_conf.proto`) and a coordinated change to `go-control-plane`. This is a natural follow-on once the base feature is established.
