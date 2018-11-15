package ratelimit_test

import (
	"sync"
	"testing"

	"github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	"github.com/golang/mock/gomock"
	"github.com/lyft/gostats"
	pb "github.com/lyft/ratelimit/proto/envoy/service/ratelimit/v2"
	"github.com/lyft/ratelimit/src/config"
	"github.com/lyft/ratelimit/src/redis"
	"github.com/lyft/ratelimit/src/service"
	"github.com/lyft/ratelimit/test/common"
	"github.com/lyft/ratelimit/test/mocks/config"
	"github.com/lyft/ratelimit/test/mocks/redis"
	"github.com/lyft/ratelimit/test/mocks/runtime/loader"
	"github.com/lyft/ratelimit/test/mocks/runtime/snapshot"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
)

type barrier struct {
	ready bool
	event *sync.Cond
}

func (this *barrier) signal() {
	this.event.L.Lock()
	defer this.event.L.Unlock()
	this.ready = true
	this.event.Signal()
}

func (this *barrier) wait() {
	this.event.L.Lock()
	defer this.event.L.Unlock()
	if !this.ready {
		this.event.Wait()
	}
	this.ready = false
}

func newBarrier() barrier {
	ret := barrier{}
	ret.event = sync.NewCond(&sync.Mutex{})
	return ret
}

type rateLimitServiceTestSuite struct {
	assert                 *assert.Assertions
	controller             *gomock.Controller
	runtime                *mock_loader.MockIFace
	snapshot               *mock_snapshot.MockIFace
	cache                  *mock_redis.MockRateLimitCache
	configLoader           *mock_config.MockRateLimitConfigLoader
	config                 *mock_config.MockRateLimitConfig
	runtimeUpdateCallback  chan<- int
	statStore              stats.Store
	responseHeadersEnabled bool
	clock                  ratelimit.Clock
}

func commonSetup(t *testing.T) rateLimitServiceTestSuite {
	ret := rateLimitServiceTestSuite{}
	ret.assert = assert.New(t)
	ret.controller = gomock.NewController(t)
	ret.runtime = mock_loader.NewMockIFace(ret.controller)
	ret.snapshot = mock_snapshot.NewMockIFace(ret.controller)
	ret.cache = mock_redis.NewMockRateLimitCache(ret.controller)
	ret.configLoader = mock_config.NewMockRateLimitConfigLoader(ret.controller)
	ret.config = mock_config.NewMockRateLimitConfig(ret.controller)
	ret.statStore = stats.NewStore(stats.NewNullSink(), false)
	return ret
}

func (this *rateLimitServiceTestSuite) setupBasicService() ratelimit.RateLimitServiceServer {
	this.runtime.EXPECT().AddUpdateCallback(gomock.Any()).Do(
		func(callback chan<- int) {
			this.runtimeUpdateCallback = callback
		})
	this.runtime.EXPECT().Snapshot().Return(this.snapshot).MinTimes(1)
	this.snapshot.EXPECT().Keys().Return([]string{"foo", "config.basic_config"}).MinTimes(1)
	this.snapshot.EXPECT().Get("config.basic_config").Return("fake_yaml").MinTimes(1)
	this.configLoader.EXPECT().Load(
		[]config.RateLimitConfigToLoad{{"config.basic_config", "fake_yaml"}},
		gomock.Any()).Return(this.config)
	return ratelimit.NewService(this.runtime, this.cache, this.configLoader, this.statStore, this.responseHeadersEnabled, this.clock)
}

func TestService(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	// First request, config should be loaded.
	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(nil)
	t.cache.EXPECT().DoLimit(nil, request, []*config.RateLimit{nil}).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}})

	response, err := service.ShouldRateLimit(nil, request)
	t.assert.Equal(
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}}},
		response)
	t.assert.Nil(err)

	// Force a config reload.
	barrier := newBarrier()
	t.configLoader.EXPECT().Load(
		[]config.RateLimitConfigToLoad{{"config.basic_config", "fake_yaml"}}, gomock.Any()).Do(
		func([]config.RateLimitConfigToLoad, stats.Scope) { barrier.signal() }).Return(t.config)
	t.runtimeUpdateCallback <- 1
	barrier.wait()

	// Different request.
	request = common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore),
		nil}
	t.config.EXPECT().GetLimit(nil, "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "different-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal(
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			}},
		response)
	t.assert.Nil(err)

	// Config load failure.
	t.configLoader.EXPECT().Load(
		[]config.RateLimitConfigToLoad{{"config.basic_config", "fake_yaml"}}, gomock.Any()).Do(
		func([]config.RateLimitConfigToLoad, stats.Scope) {
			barrier.signal()
			panic(config.RateLimitConfigError("load error"))
		})
	t.runtimeUpdateCallback <- 1
	barrier.wait()

	// Config should still be valid. Also make sure order does not affect results.
	limits = []*config.RateLimit{
		nil,
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore)}
	t.config.EXPECT().GetLimit(nil, "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "different-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0}})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal(
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			}},
		response)
	t.assert.Nil(err)

	t.assert.EqualValues(2, t.statStore.NewCounter("config_load_success").Value())
	t.assert.EqualValues(1, t.statStore.NewCounter("config_load_error").Value())
}

func TestEmptyDomain(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("", [][][2]string{{{"hello", "world"}}}, 1)
	response, err := service.ShouldRateLimit(nil, request)
	t.assert.Nil(response)
	t.assert.Equal("rate limit domain must not be empty", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestEmptyDescriptors(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{}, 1)
	response, err := service.ShouldRateLimit(nil, request)
	t.assert.Nil(response)
	t.assert.Equal("rate limit descriptor list must not be empty", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestCacheError(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("different-domain", [][][2]string{{{"foo", "bar"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore)}
	t.config.EXPECT().GetLimit(nil, "different-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, request, limits).Do(
		func(context.Context, *pb.RateLimitRequest, []*config.RateLimit) {
			panic(redis.RedisError("cache error"))
		})

	response, err := service.ShouldRateLimit(nil, request)
	t.assert.Nil(response)
	t.assert.Equal("cache error", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.redis_error").Value())
}

func TestInitialLoadError(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	t.runtime.EXPECT().AddUpdateCallback(gomock.Any()).Do(
		func(callback chan<- int) { t.runtimeUpdateCallback = callback })
	t.runtime.EXPECT().Snapshot().Return(t.snapshot).MinTimes(1)
	t.snapshot.EXPECT().Keys().Return([]string{"foo", "config.basic_config"}).MinTimes(1)
	t.snapshot.EXPECT().Get("config.basic_config").Return("fake_yaml").MinTimes(1)
	t.configLoader.EXPECT().Load(
		[]config.RateLimitConfigToLoad{{"config.basic_config", "fake_yaml"}}, gomock.Any()).Do(
		func([]config.RateLimitConfigToLoad, stats.Scope) {
			panic(config.RateLimitConfigError("load error"))
		})
	service := ratelimit.NewService(t.runtime, t.cache, t.configLoader, t.statStore,
		t.responseHeadersEnabled, t.clock)

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	response, err := service.ShouldRateLimit(nil, request)
	t.assert.Nil(response)
	t.assert.Equal("no rate limit configuration loaded", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestHeaders(test *testing.T) {
	t := commonSetup(test)
	currentTime := 123
	t.responseHeadersEnabled = true
	t.clock = ratelimit.Clock{UnixSeconds: func() int64 { return int64(currentTime) }}
	defer t.controller.Finish()

	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore),
	}

	// Under limit
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK,
			CurrentLimit: limits[0].Limit, LimitRemaining: 6},
		})
	response, err := service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Limit", Value: "10"},
		{Key: "X-RateLimit-Remaining", Value: "6"},
		{Key: "X-RateLimit-Reset", Value: "57"},
	},
		response.Headers)
	t.assert.Nil(err)

	// Last request under limit
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK,
			CurrentLimit: limits[0].Limit, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Limit", Value: "10"},
		{Key: "X-RateLimit-Remaining", Value: "0"},
		{Key: "X-RateLimit-Reset", Value: "57"},
	},
		response.Headers)
	t.assert.Nil(err)

	// Over limit
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit: limits[0].Limit, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Limit", Value: "10"},
		{Key: "X-RateLimit-Remaining", Value: "0"},
		{Key: "X-RateLimit-Reset", Value: "57"},
	},
		response.Headers)
	t.assert.Nil(err)

	// After time passes, the reset header should decrement
	currentTime = 124
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit: limits[0].Limit, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Limit", Value: "10"},
		{Key: "X-RateLimit-Remaining", Value: "0"},
		{Key: "X-RateLimit-Reset", Value: "56"},
	}, response.Headers)
	t.assert.Nil(err)

	// Last second before reset
	currentTime = 179
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit: limits[0].Limit, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Limit", Value: "10"},
		{Key: "X-RateLimit-Remaining", Value: "0"},
		{Key: "X-RateLimit-Reset", Value: "1"},
	}, response.Headers)
	t.assert.Nil(err)

	// Exact second when reset occurs
	currentTime = 180
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK,
			CurrentLimit: limits[0].Limit, LimitRemaining: 9},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Limit", Value: "10"},
		{Key: "X-RateLimit-Remaining", Value: "9"},
		{Key: "X-RateLimit-Reset", Value: "60"},
	}, response.Headers)
	t.assert.Nil(err)

	// Multiple descriptors
	// (X-RateLimit-Limit omitted because choosing the limit of one descriptor would be arbitrary)

	currentTime = 200
	request = common.NewRateLimitRequest("test-domain", [][][2]string{
		{{"a", "b"}}, {{"c", "d"}}, {{"e", "f"}},
	}, 1)
	limits = []*config.RateLimit{
		config.NewRateLimit(1000, pb.RateLimitResponse_RateLimit_HOUR, "key", t.statStore),
		config.NewRateLimit(75, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore),
		config.NewRateLimit(50, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore),
	}

	// First descriptor is limiting factor
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 3},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 4},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 5},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Remaining", Value: "3"},
		{Key: "X-RateLimit-Reset", Value: "3400"},
	}, response.Headers)
	t.assert.Nil(err)

	// Second descriptor is limiting factor
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 6},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 4},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 5},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Remaining", Value: "4"},
		{Key: "X-RateLimit-Reset", Value: "40"},
	}, response.Headers)
	t.assert.Nil(err)

	// Third descriptor is limiting factor
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 6},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 7},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 5},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Remaining", Value: "5"},
		{Key: "X-RateLimit-Reset", Value: "40"},
	}, response.Headers)
	t.assert.Nil(err)

	// If there's a LimitRemaining tie, the highest Reset is returned
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 6},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 6},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 7},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Remaining", Value: "6"},
		{Key: "X-RateLimit-Reset", Value: "3400"},
	}, response.Headers)
	t.assert.Nil(err)

	// Same test with same expected result, but inverse descriptor order from cache
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(nil, request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 7},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 6},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 6},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Equal([]*core.HeaderValue{
		{Key: "X-RateLimit-Remaining", Value: "6"},
		{Key: "X-RateLimit-Reset", Value: "3400"},
	}, response.Headers)
	t.assert.Nil(err)

	// No headers if no limit, one descriptor
	request = common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(nil)
	t.cache.EXPECT().DoLimit(nil, request, []*config.RateLimit{nil}).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Nil(response.Headers)
	t.assert.Nil(err)

	// No headers if no limit, two descriptors
	request = common.NewRateLimitRequest("test-domain", [][][2]string{
		{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[0]).Return(nil)
	t.config.EXPECT().GetLimit(nil, "test-domain", request.Descriptors[1]).Return(nil)
	t.cache.EXPECT().DoLimit(nil, request, []*config.RateLimit{nil, nil}).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(nil, request)
	t.assert.Nil(response.Headers)
	t.assert.Nil(err)
}
