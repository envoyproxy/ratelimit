# RequestHeadersToAdd Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `RequestHeadersToAdd` support so the ratelimit service can inject RateLimit-* headers into the forwarded request (upstream direction), mirroring the existing `ResponseHeadersToAdd` feature.

**Architecture:** Four new env vars (`LIMIT_REQUEST_HEADERS_ENABLED`, `LIMIT_REQUEST_LIMIT_HEADER`, `LIMIT_REQUEST_REMAINING_HEADER`, `LIMIT_REQUEST_RESET_HEADER`) control the feature. When enabled, `shouldRateLimitWorker` populates `response.RequestHeadersToAdd` using the same `minimumDescriptor` logic already used for `ResponseHeadersToAdd`. The two features are independently controlled.

**Tech Stack:** Go, `github.com/envoyproxy/go-control-plane` (proto types for `RateLimitResponse` and `HeaderValue`), `github.com/kelseyhightower/envconfig` (settings), `github.com/golang/mock` (test mocks), `github.com/stretchr/testify` (assertions).

---

## File Map

| File | Change |
|---|---|
| `src/settings/settings.go` | Add 4 new env var fields |
| `src/service/ratelimit.go` | Add 4 struct fields, wire in `SetConfig`, populate `RequestHeadersToAdd` in `shouldRateLimitWorker` |
| `test/service/ratelimit_test.go` | Add 4 new test functions |
| `README.md` | Add `Global Rate Limit Request Headers` section after existing `Custom headers` section |

---

## Task 1: Add settings fields

**Files:**
- Modify: `src/settings/settings.go:114-121`

- [ ] **Step 1: Write a failing test that reads the new settings fields**

In `test/service/ratelimit_test.go`, before `TestServiceWithCustomRatelimitHeaders`, add:

```go
func TestRequestHeadersSettingsDefaults(test *testing.T) {
    s := settings.NewSettings()
    assert.False(test, s.RateLimitRequestHeadersEnabled)
    assert.Equal(test, "RateLimit-Limit", s.HeaderRequestRatelimitLimit)
    assert.Equal(test, "RateLimit-Remaining", s.HeaderRequestRatelimitRemaining)
    assert.Equal(test, "RateLimit-Reset", s.HeaderRequestRatelimitReset)
}
```

Add `"github.com/envoyproxy/ratelimit/src/settings"` to the import block if not already present. Check with:

```bash
head -30 /Users/yganji/workplace/ratelimit-fork/test/service/ratelimit_test.go
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -run TestRequestHeadersSettingsDefaults -v
```

Expected: compile error — `s.RateLimitRequestHeadersEnabled` undefined.

- [ ] **Step 3: Add the four new fields to `src/settings/settings.go`**

After line 121 (`HeaderRatelimitReset string ...`), insert:

```go
// Settings for optional returning of custom request headers
RateLimitRequestHeadersEnabled bool `envconfig:"LIMIT_REQUEST_HEADERS_ENABLED" default:"false"`
// value: the current limit
HeaderRequestRatelimitLimit string `envconfig:"LIMIT_REQUEST_LIMIT_HEADER" default:"RateLimit-Limit"`
// value: remaining count
HeaderRequestRatelimitRemaining string `envconfig:"LIMIT_REQUEST_REMAINING_HEADER" default:"RateLimit-Remaining"`
// value: remaining seconds
HeaderRequestRatelimitReset string `envconfig:"LIMIT_REQUEST_RESET_HEADER" default:"RateLimit-Reset"`
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -run TestRequestHeadersSettingsDefaults -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/yganji/workplace/ratelimit-fork && git add src/settings/settings.go test/service/ratelimit_test.go
git commit -m "feat: add RequestHeaders settings fields"
```

---

## Task 2: Wire settings into service struct

**Files:**
- Modify: `src/service/ratelimit.go:42-57` (struct), `src/service/ratelimit.go:89-103` (SetConfig)

- [ ] **Step 1: Write a failing test — request headers disabled by default**

In `test/service/ratelimit_test.go`, add after `TestRequestHeadersSettingsDefaults`:

```go
func TestRequestHeadersDisabledByDefault(test *testing.T) {
    t := commonSetup(test)
    defer t.controller.Finish()
    service := t.setupBasicService()

    barrier := newBarrier()
    t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
        barrier.signal()
        return t.config, nil
    })
    t.configUpdateEventChan <- t.configUpdateEvent
    barrier.wait()

    request := common.NewRateLimitRequest(
        "different-domain", [][][2]string{{{"foo", "bar"}}}, 1)
    limits := []*config.RateLimit{
        config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
    }
    t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
    t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
        []*pb.RateLimitResponse_DescriptorStatus{
            {Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
        })

    response, err := service.ShouldRateLimit(context.Background(), request)
    t.assert.Nil(err)
    t.assert.Nil(response.RequestHeadersToAdd)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -run TestRequestHeadersDisabledByDefault -v
```

Expected: compile error — `response.RequestHeadersToAdd` doesn't exist yet on the struct... actually this test will pass immediately since `RequestHeadersToAdd` already exists on the proto. Confirm by running — if it passes, proceed to step 3.

- [ ] **Step 3: Add four fields to the `service` struct**

In `src/service/ratelimit.go`, after line 53 (`customHeaderClock utils.TimeSource`), add:

```go
requestHeadersEnabled           bool
requestHeaderLimitHeader        string
requestHeaderRemainingHeader    string
requestHeaderResetHeader        string
```

- [ ] **Step 4: Wire the fields in `SetConfig`**

In `src/service/ratelimit.go`, after the closing brace of the `if rlSettings.RateLimitResponseHeadersEnabled` block (after line 102), add:

```go
if rlSettings.RateLimitRequestHeadersEnabled {
    this.requestHeadersEnabled = true
    this.requestHeaderLimitHeader = rlSettings.HeaderRequestRatelimitLimit
    this.requestHeaderRemainingHeader = rlSettings.HeaderRequestRatelimitRemaining
    this.requestHeaderResetHeader = rlSettings.HeaderRequestRatelimitReset
}
```

- [ ] **Step 5: Run tests to verify nothing is broken**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -run "TestRequestHeadersDisabledByDefault|TestRequestHeadersSettingsDefaults" -v
```

Expected: both PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/yganji/workplace/ratelimit-fork && git add src/service/ratelimit.go test/service/ratelimit_test.go
git commit -m "feat: wire request headers settings into service struct"
```

---

## Task 3: Populate RequestHeadersToAdd in shouldRateLimitWorker

**Files:**
- Modify: `src/service/ratelimit.go:258-264`

- [ ] **Step 1: Write failing test — default header names, over limit**

In `test/service/ratelimit_test.go`, add:

```go
func TestServiceWithDefaultRequestHeaders(test *testing.T) {
    os.Setenv("LIMIT_REQUEST_HEADERS_ENABLED", "true")
    defer func() {
        os.Unsetenv("LIMIT_REQUEST_HEADERS_ENABLED")
    }()

    t := commonSetup(test)
    defer t.controller.Finish()
    service := t.setupBasicService()

    barrier := newBarrier()
    t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
        barrier.signal()
        return t.config, nil
    })
    t.configUpdateEventChan <- t.configUpdateEvent
    barrier.wait()

    request := common.NewRateLimitRequest(
        "different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
    limits := []*config.RateLimit{
        config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
        nil,
    }
    t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
    t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
    t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
        []*pb.RateLimitResponse_DescriptorStatus{
            {Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
            {Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
        })

    response, err := service.ShouldRateLimit(context.Background(), request)
    common.AssertProtoEqual(
        t.assert,
        &pb.RateLimitResponse{
            OverallCode: pb.RateLimitResponse_OVER_LIMIT,
            Statuses: []*pb.RateLimitResponse_DescriptorStatus{
                {Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
                {Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
            },
            RequestHeadersToAdd: []*core.HeaderValue{
                {Key: "RateLimit-Limit", Value: "10"},
                {Key: "RateLimit-Remaining", Value: "0"},
                {Key: "RateLimit-Reset", Value: "58"},
            },
        },
        response)
    t.assert.Nil(err)
}
```

Note: `Value: "58"` is the reset seconds computed from `MockClock{now: 2222}` with a MINUTE unit — same value used in the existing response header tests.

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -run TestServiceWithDefaultRequestHeaders -v
```

Expected: FAIL — `RequestHeadersToAdd` is nil.

- [ ] **Step 3: Populate RequestHeadersToAdd in shouldRateLimitWorker**

In `src/service/ratelimit.go`, after the `ResponseHeadersToAdd` block (after line 264, the closing `}`), add:

```go
// Add request headers if requested
if this.requestHeadersEnabled && minimumDescriptor != nil {
    response.RequestHeadersToAdd = []*core.HeaderValue{
        {Key: this.requestHeaderLimitHeader, Value: strconv.FormatUint(uint64(minimumDescriptor.CurrentLimit.RequestsPerUnit), 10)},
        {Key: this.requestHeaderRemainingHeader, Value: strconv.FormatUint(uint64(minimumDescriptor.LimitRemaining), 10)},
        {Key: this.requestHeaderResetHeader, Value: strconv.FormatInt(utils.CalculateReset(&minimumDescriptor.CurrentLimit.Unit, this.customHeaderClock).GetSeconds(), 10)},
    }
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -run TestServiceWithDefaultRequestHeaders -v
```

Expected: PASS.

- [ ] **Step 5: Run all existing tests to verify no regressions**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -v 2>&1 | tail -20
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/yganji/workplace/ratelimit-fork && git add src/service/ratelimit.go test/service/ratelimit_test.go
git commit -m "feat: populate RequestHeadersToAdd in shouldRateLimitWorker"
```

---

## Task 4: Add remaining test cases

**Files:**
- Modify: `test/service/ratelimit_test.go`

- [ ] **Step 1: Add test — custom header names**

In `test/service/ratelimit_test.go`, add after `TestServiceWithDefaultRequestHeaders`:

```go
func TestServiceWithCustomRequestHeaders(test *testing.T) {
    os.Setenv("LIMIT_REQUEST_HEADERS_ENABLED", "true")
    os.Setenv("LIMIT_REQUEST_LIMIT_HEADER", "X-RateLimit-Limit")
    os.Setenv("LIMIT_REQUEST_REMAINING_HEADER", "X-RateLimit-Remaining")
    os.Setenv("LIMIT_REQUEST_RESET_HEADER", "X-RateLimit-Reset")
    defer func() {
        os.Unsetenv("LIMIT_REQUEST_HEADERS_ENABLED")
        os.Unsetenv("LIMIT_REQUEST_LIMIT_HEADER")
        os.Unsetenv("LIMIT_REQUEST_REMAINING_HEADER")
        os.Unsetenv("LIMIT_REQUEST_RESET_HEADER")
    }()

    t := commonSetup(test)
    defer t.controller.Finish()
    service := t.setupBasicService()

    barrier := newBarrier()
    t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
        barrier.signal()
        return t.config, nil
    })
    t.configUpdateEventChan <- t.configUpdateEvent
    barrier.wait()

    request := common.NewRateLimitRequest(
        "different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
    limits := []*config.RateLimit{
        config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
        nil,
    }
    t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
    t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
    t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
        []*pb.RateLimitResponse_DescriptorStatus{
            {Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
            {Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
        })

    response, err := service.ShouldRateLimit(context.Background(), request)
    common.AssertProtoEqual(
        t.assert,
        &pb.RateLimitResponse{
            OverallCode: pb.RateLimitResponse_OVER_LIMIT,
            Statuses: []*pb.RateLimitResponse_DescriptorStatus{
                {Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
                {Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
            },
            RequestHeadersToAdd: []*core.HeaderValue{
                {Key: "X-RateLimit-Limit", Value: "10"},
                {Key: "X-RateLimit-Remaining", Value: "0"},
                {Key: "X-RateLimit-Reset", Value: "58"},
            },
        },
        response)
    t.assert.Nil(err)
}
```

- [ ] **Step 2: Add test — headers present when within limit (not over limit)**

```go
func TestServiceWithRequestHeadersWithinLimit(test *testing.T) {
    os.Setenv("LIMIT_REQUEST_HEADERS_ENABLED", "true")
    defer func() {
        os.Unsetenv("LIMIT_REQUEST_HEADERS_ENABLED")
    }()

    t := commonSetup(test)
    defer t.controller.Finish()
    service := t.setupBasicService()

    barrier := newBarrier()
    t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
        barrier.signal()
        return t.config, nil
    })
    t.configUpdateEventChan <- t.configUpdateEvent
    barrier.wait()

    request := common.NewRateLimitRequest(
        "different-domain", [][][2]string{{{"foo", "bar"}}}, 1)
    limits := []*config.RateLimit{
        config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
    }
    t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
    t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
        []*pb.RateLimitResponse_DescriptorStatus{
            {Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 8},
        })

    response, err := service.ShouldRateLimit(context.Background(), request)
    common.AssertProtoEqual(
        t.assert,
        &pb.RateLimitResponse{
            OverallCode: pb.RateLimitResponse_OK,
            Statuses: []*pb.RateLimitResponse_DescriptorStatus{
                {Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 8},
            },
            RequestHeadersToAdd: []*core.HeaderValue{
                {Key: "RateLimit-Limit", Value: "10"},
                {Key: "RateLimit-Remaining", Value: "8"},
                {Key: "RateLimit-Reset", Value: "58"},
            },
        },
        response)
    t.assert.Nil(err)
}
```

- [ ] **Step 3: Add test — both request and response headers enabled simultaneously with different names**

```go
func TestServiceWithBothRequestAndResponseHeaders(test *testing.T) {
    os.Setenv("LIMIT_REQUEST_HEADERS_ENABLED", "true")
    os.Setenv("LIMIT_REQUEST_LIMIT_HEADER", "X-Upstream-Limit")
    os.Setenv("LIMIT_REQUEST_REMAINING_HEADER", "X-Upstream-Remaining")
    os.Setenv("LIMIT_REQUEST_RESET_HEADER", "X-Upstream-Reset")
    os.Setenv("LIMIT_RESPONSE_HEADERS_ENABLED", "true")
    os.Setenv("LIMIT_LIMIT_HEADER", "X-Downstream-Limit")
    os.Setenv("LIMIT_REMAINING_HEADER", "X-Downstream-Remaining")
    os.Setenv("LIMIT_RESET_HEADER", "X-Downstream-Reset")
    defer func() {
        os.Unsetenv("LIMIT_REQUEST_HEADERS_ENABLED")
        os.Unsetenv("LIMIT_REQUEST_LIMIT_HEADER")
        os.Unsetenv("LIMIT_REQUEST_REMAINING_HEADER")
        os.Unsetenv("LIMIT_REQUEST_RESET_HEADER")
        os.Unsetenv("LIMIT_RESPONSE_HEADERS_ENABLED")
        os.Unsetenv("LIMIT_LIMIT_HEADER")
        os.Unsetenv("LIMIT_REMAINING_HEADER")
        os.Unsetenv("LIMIT_RESET_HEADER")
    }()

    t := commonSetup(test)
    defer t.controller.Finish()
    service := t.setupBasicService()

    barrier := newBarrier()
    t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
        barrier.signal()
        return t.config, nil
    })
    t.configUpdateEventChan <- t.configUpdateEvent
    barrier.wait()

    request := common.NewRateLimitRequest(
        "different-domain", [][][2]string{{{"foo", "bar"}}}, 1)
    limits := []*config.RateLimit{
        config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
    }
    t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
    t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
        []*pb.RateLimitResponse_DescriptorStatus{
            {Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
        })

    response, err := service.ShouldRateLimit(context.Background(), request)
    common.AssertProtoEqual(
        t.assert,
        &pb.RateLimitResponse{
            OverallCode: pb.RateLimitResponse_OVER_LIMIT,
            Statuses: []*pb.RateLimitResponse_DescriptorStatus{
                {Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
            },
            RequestHeadersToAdd: []*core.HeaderValue{
                {Key: "X-Upstream-Limit", Value: "10"},
                {Key: "X-Upstream-Remaining", Value: "0"},
                {Key: "X-Upstream-Reset", Value: "58"},
            },
            ResponseHeadersToAdd: []*core.HeaderValue{
                {Key: "X-Downstream-Limit", Value: "10"},
                {Key: "X-Downstream-Remaining", Value: "0"},
                {Key: "X-Downstream-Reset", Value: "58"},
            },
        },
        response)
    t.assert.Nil(err)
}
```

- [ ] **Step 4: Run all four new tests**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./test/service/... -run "TestServiceWithDefaultRequestHeaders|TestServiceWithCustomRequestHeaders|TestServiceWithRequestHeadersWithinLimit|TestServiceWithBothRequestAndResponseHeaders" -v
```

Expected: all 4 PASS.

- [ ] **Step 5: Run the full test suite**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./... 2>&1 | tail -20
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/yganji/workplace/ratelimit-fork && git add test/service/ratelimit_test.go
git commit -m "test: add RequestHeadersToAdd test cases"
```

---

## Task 5: Update README

**Files:**
- Modify: `README.md:1406-1416`

- [ ] **Step 1: Insert the new section after the existing `Custom headers` section**

In `README.md`, after line 1415 (`1. \`LIMIT_RESET_HEADER\` ...`) and before line 1417 (`# Tracing`), insert:

```markdown

The following environment variables control the custom request header feature (headers injected into the forwarded request sent to upstream services by Envoy):

1. `LIMIT_REQUEST_HEADERS_ENABLED` - Enables the custom request headers. When enabled, Envoy injects these headers into the forwarded request, allowing upstream services to inspect rate limit state and make per-request routing decisions (e.g. skip a hot path when quota is exhausted) without the request being blocked.
1. `LIMIT_REQUEST_LIMIT_HEADER` - The default value is "RateLimit-Limit", setting the environment variable will specify an alternative header name
1. `LIMIT_REQUEST_REMAINING_HEADER` - The default value is "RateLimit-Remaining", setting the environment variable will specify an alternative header name
1. `LIMIT_REQUEST_RESET_HEADER` - The default value is "RateLimit-Reset", setting the environment variable will specify an alternative header name
```

- [ ] **Step 2: Verify the section reads correctly**

```bash
grep -A 10 "LIMIT_REQUEST_HEADERS_ENABLED" /Users/yganji/workplace/ratelimit-fork/README.md
```

Expected: the 4 env vars appear with their descriptions.

- [ ] **Step 3: Commit**

```bash
cd /Users/yganji/workplace/ratelimit-fork && git add README.md
git commit -m "docs: add RequestHeadersToAdd documentation to README"
```

---

## Task 6: Final verification

- [ ] **Step 1: Run the full test suite one more time**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go test ./... 2>&1 | grep -E "FAIL|ok"
```

Expected: all lines show `ok`, no `FAIL`.

- [ ] **Step 2: Check the build**

```bash
cd /Users/yganji/workplace/ratelimit-fork && go build ./...
```

Expected: exits with code 0, no output.

- [ ] **Step 3: Review the diff**

```bash
cd /Users/yganji/workplace/ratelimit-fork && git diff origin/main..HEAD --stat
```

Confirm only these files changed: `src/settings/settings.go`, `src/service/ratelimit.go`, `test/service/ratelimit_test.go`, `README.md`, `docs/`.
