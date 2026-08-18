package ratelimit_test

import (
	"testing"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/config"
	ratelimit "github.com/envoyproxy/ratelimit/src/service"
	"github.com/envoyproxy/ratelimit/test/common"
)

func hasSoftBreachHeader(headers []*core.HeaderValue) bool {
	for _, h := range headers {
		if h.Key == "x-ratelimit-soft-breach" {
			return true
		}
	}
	return false
}

// One admitted descriptor inside the soft band (remaining <= (1-ratio)*limit)
// must add the soft-breach header.
func TestSoftBreachHeaderEmittedInSoftBand(test *testing.T) {
	ratelimit.SetSoftBreachHeaderRatio(0.7)
	defer ratelimit.SetSoftBreachHeaderRatio(0)

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 3}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.True(hasSoftBreachHeader(response.ResponseHeadersToAdd), "remaining 3 of 10 at ratio 0.7 is inside the soft band")
}

// Plenty of budget left: no header.
func TestSoftBreachHeaderAbsentBelowThreshold(test *testing.T) {
	ratelimit.SetSoftBreachHeaderRatio(0.7)
	defer ratelimit.SetSoftBreachHeaderRatio(0)

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.False(hasSoftBreachHeader(response.ResponseHeadersToAdd), "remaining 5 of 10 at ratio 0.7 is outside the soft band")
}

// A hard breach is not a soft breach: no header on OVER_LIMIT responses.
func TestSoftBreachHeaderAbsentWhenOverLimit(test *testing.T) {
	ratelimit.SetSoftBreachHeaderRatio(0.7)
	defer ratelimit.SetSoftBreachHeaderRatio(0)

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OVER_LIMIT, response.OverallCode)
	t.assert.False(hasSoftBreachHeader(response.ResponseHeadersToAdd))
}

// Feature off (ratio 0): never emit the header.
func TestSoftBreachHeaderAbsentWhenDisabled(test *testing.T) {
	ratelimit.SetSoftBreachHeaderRatio(0)

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.False(hasSoftBreachHeader(response.ResponseHeadersToAdd))
}

// Lua parity: the LAST admitted request (remaining 0, still OK) is a soft
// breach — depop-routing flags count > soft while count <= limit, which
// includes the request that lands exactly on the limit.
func TestSoftBreachHeaderOnLastAdmittedRequest(test *testing.T) {
	ratelimit.SetSoftBreachHeaderRatio(0.7)
	defer ratelimit.SetSoftBreachHeaderRatio(0)

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.True(hasSoftBreachHeader(response.ResponseHeadersToAdd), "an admitted request with 0 remaining is a soft breach (Lua parity)")
}
