package ratelimit_test

import (
	"testing"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/test/common"
)

const softBreachHeader = "x-ratelimit-soft-breached"

func hasHeader(headers []*core.HeaderValue, name string) bool {
	for _, h := range headers {
		if h.Key == name {
			return true
		}
	}
	return false
}

// softLimit builds a limit carrying a soft threshold, mirroring depop-routing's
// per-route rate_limit_soft_count.
func softLimit(t rateLimitServiceTestSuite, requests, soft uint32) *config.RateLimit {
	l := config.NewRateLimit(requests, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false)
	l.SoftRequestsPerUnit = soft
	return l
}

// The soft-breach signal must go UPSTREAM so the backend can degrade, matching
// `proxy_set_header X-RateLimit-Soft-Breached` in service_common.conf. Sending it
// downstream tells the wrong party entirely.
func TestSoftBreachSignalsUpstreamNotDownstream(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{softLimit(t, 10, 7)}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	// 8 used of 10 => inside the soft band (>7), still admitted
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.True(hasHeader(response.RequestHeadersToAdd, softBreachHeader),
		"soft breach must be added to the UPSTREAM request headers")
	t.assert.False(hasHeader(response.ResponseHeadersToAdd, softBreachHeader),
		"soft breach must NOT be returned to the client")
}

// Only descriptors that configure a soft threshold may signal. depop-routing
// passes soft_count from auth_rate_limit only; the ip/user_agent/client_id
// limiters pass nil and can never soft-breach.
func TestNoSoftBreachWhenDescriptorHasNoSoftThreshold(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	// no SoftRequestsPerUnit set — this is the ip/user_agent/client_id shape
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	// 9 of 10 used — deep into what would be a soft band if one existed
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.False(hasHeader(response.RequestHeadersToAdd, softBreachHeader),
		"a descriptor with no soft threshold must never signal a soft breach")
}

// Below the soft threshold: silent.
func TestNoSoftBreachBelowThreshold(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{softLimit(t, 10, 7)}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	// 5 used of 10 => below soft threshold of 7
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.False(hasHeader(response.RequestHeadersToAdd, softBreachHeader))
}

// A hard breach is not a soft breach — depop-routing exits before the soft path.
func TestNoSoftBreachWhenOverLimit(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{softLimit(t, 10, 7)}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OVER_LIMIT, response.OverallCode)
	t.assert.False(hasHeader(response.RequestHeadersToAdd, softBreachHeader))
}

// Landing exactly on the limit is still an admitted request, and depop-routing
// classifies count > soft as soft while count <= hard, so it signals.
func TestSoftBreachOnLastAdmittedRequest(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{softLimit(t, 10, 7)}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.True(hasHeader(response.RequestHeadersToAdd, softBreachHeader))
}

// Config parsing: soft_requests_per_unit must survive YAML -> RateLimit.
func TestSoftRequestsPerUnitParsedFromConfig(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	l := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false)
	t.assert.Equal(uint32(0), l.SoftRequestsPerUnit, "defaults to 0 = feature off, matching the three limiters that pass nil")
}
