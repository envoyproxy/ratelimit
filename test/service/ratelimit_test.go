package ratelimit_test

import (
	"math"
	"os"
	"sync"
	"testing"

	"github.com/envoyproxy/ratelimit/src/provider"
	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/envoyproxy/ratelimit/src/utils"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/golang/mock/gomock"
	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/trace"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/redis"
	ratelimit "github.com/envoyproxy/ratelimit/src/service"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_config "github.com/envoyproxy/ratelimit/test/mocks/config"
	mock_limiter "github.com/envoyproxy/ratelimit/test/mocks/limiter"
	mock_provider "github.com/envoyproxy/ratelimit/test/mocks/provider"
	mock_loader "github.com/envoyproxy/ratelimit/test/mocks/runtime/loader"
	mock_snapshot "github.com/envoyproxy/ratelimit/test/mocks/runtime/snapshot"
	mock_stats "github.com/envoyproxy/ratelimit/test/mocks/stats"
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
	assert                *assert.Assertions
	controller            *gomock.Controller
	runtime               *mock_loader.MockIFace
	snapshot              *mock_snapshot.MockIFace
	cache                 *mock_limiter.MockRateLimitCache
	configProvider        *mock_provider.MockRateLimitConfigProvider
	configUpdateEventChan chan provider.ConfigUpdateEvent
	configUpdateEvent     *mock_provider.MockConfigUpdateEvent
	configLoader          *mock_config.MockRateLimitConfigLoader
	config                *mock_config.MockRateLimitConfig
	runtimeUpdateCallback chan<- int
	statsManager          stats.Manager
	statStore             gostats.Store
	mockClock             utils.TimeSource
}

type MockClock struct {
	now int64
}

func (c MockClock) UnixNow() int64 { return c.now }

func commonSetup(t *testing.T) rateLimitServiceTestSuite {
	ret := rateLimitServiceTestSuite{}
	ret.assert = assert.New(t)
	ret.controller = gomock.NewController(t)
	// ret.runtime = mock_loader.NewMockIFace(ret.controller)
	// ret.snapshot = mock_snapshot.NewMockIFace(ret.controller)
	ret.cache = mock_limiter.NewMockRateLimitCache(ret.controller)
	ret.configProvider = mock_provider.NewMockRateLimitConfigProvider(ret.controller)
	ret.configUpdateEventChan = make(chan provider.ConfigUpdateEvent)
	ret.configUpdateEvent = mock_provider.NewMockConfigUpdateEvent(ret.controller)
	// ret.configLoader = mock_config.NewMockRateLimitConfigLoader(ret.controller)
	ret.config = mock_config.NewMockRateLimitConfig(ret.controller)
	ret.statStore = gostats.NewStore(gostats.NewNullSink(), false)
	ret.statsManager = mock_stats.NewMockStatManager(ret.statStore)
	return ret
}

func (this *rateLimitServiceTestSuite) setupBasicService() ratelimit.RateLimitServiceServer {
	barrier := newBarrier()
	// this.runtime.EXPECT().AddUpdateCallback(gomock.Any()).Do(
	// 	func(callback chan<- int) {
	// 		this.runtimeUpdateCallback = callback
	// 	})
	// this.runtime.EXPECT().Snapshot().Return(this.snapshot).MinTimes(1)
	// this.snapshot.EXPECT().Keys().Return([]string{"foo", "config.basic_config"}).MinTimes(1)
	// this.snapshot.EXPECT().Get("config.basic_config").Return("fake_yaml").MinTimes(1)
	this.configProvider.EXPECT().ConfigUpdateEvent().Return(this.configUpdateEventChan).Times(1)
	this.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return this.config, nil
	})
	go func() { this.configUpdateEventChan <- this.configUpdateEvent }() // initial config update from provider

	// this.configLoader.EXPECT().Load(
	// 	[]config.RateLimitConfigToLoad{{Name: "config.basic_config", FileBytes: "fake_yaml"}},
	// 	gomock.Any(), gomock.Any()).Return(this.config)

	// reset exporter before using
	testSpanExporter.Reset()

	svc := ratelimit.NewService(this.runtime, this.cache, this.configProvider, this.configLoader, this.statsManager, true, MockClock{now: int64(2222)}, false)
	barrier.wait()
	return svc
}

// once a ratelimit service is initiated, the package always fetches a default tracer from otel runtime and it can't be change until a new round of test is run. It is necessary to keep a package level exporter in this test package in order to correctly run the tests.
var testSpanExporter = trace.GetTestSpanExporter()

func TestService(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()
	barrier := newBarrier()

	// First request, config should be loaded.
	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(nil)
	t.cache.EXPECT().DoLimit(context.Background(), request, []*config.RateLimit{nil}).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}},
		},
		response)
	t.assert.Nil(err)

	// Force a config reload - config event from config provider.
	// t.configLoader.EXPECT().Load(
	// 	[]config.RateLimitConfigToLoad{{Name: "config.basic_config", FileBytes: "fake_yaml"}}, gomock.Any(), gomock.Any()).Do(
	// 	func([]config.RateLimitConfigToLoad, stats.Manager, bool) { barrier.signal() }).Return(t.config)
	// t.runtimeUpdateCallback <- 1
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	// Different request.
	request = common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, "", nil),
		nil,
	}
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(context.Background(), request)
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)

	// Config load failure.
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return nil, config.RateLimitConfigError("load error")
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()
	// t.configLoader.EXPECT().Load(
	// 	[]config.RateLimitConfigToLoad{{Name: "config.basic_config", FileBytes: "fake_yaml"}}, gomock.Any(), gomock.Any()).Do(
	// 	func([]config.RateLimitConfigToLoad, stats.Manager, bool) {
	// 		defer barrier.signal()
	// 		panic(config.RateLimitConfigError("load error"))
	// 	})
	// t.runtimeUpdateCallback <- 1

	// Config should still be valid. Also make sure order does not affect results.
	limits = []*config.RateLimit{
		nil,
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, "", nil),
	}
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err = service.ShouldRateLimit(context.Background(), request)
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)

	t.assert.EqualValues(2, t.statStore.NewCounter("config_load_success").Value())
	t.assert.EqualValues(1, t.statStore.NewCounter("config_load_error").Value())
	t.assert.EqualValues(0, t.statStore.NewCounter("global_shadow_mode").Value())
}

func TestServiceGlobalShadowMode(test *testing.T) {
	os.Setenv("SHADOW_MODE", "true")
	defer func() {
		os.Unsetenv("SHADOW_MODE")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	// No global shadow_mode, this should be picked-up from environment variables during re-load of config
	service := t.setupBasicService()

	// Force a config reload.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()
	// t.configLoader.EXPECT().Load(
	// 	[]config.RateLimitConfigToLoad{{Name: "config.basic_config", FileBytes: "fake_yaml"}}, gomock.Any(), gomock.Any()).Do(
	// 	func([]config.RateLimitConfigToLoad, stats.Manager, bool) { barrier.signal() }).Return(t.config)
	// t.runtimeUpdateCallback <- 1
	// barrier.wait()

	// Make a request.
	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)

	// Global Shadow mode
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, "", nil),
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

	// OK overall code even if limit response was OVER_LIMIT
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)

	t.assert.EqualValues(1, t.statStore.NewCounter("global_shadow_mode").Value())
	t.assert.EqualValues(2, t.statStore.NewCounter("config_load_success").Value())
	t.assert.EqualValues(0, t.statStore.NewCounter("config_load_error").Value())
}

func TestRuleShadowMode(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	// No Global Shadowmode
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, true, "", nil),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, true, "", nil),
	}
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Equal(
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)

	t.assert.EqualValues(0, t.statStore.NewCounter("global_shadow_mode").Value())
}

func TestMixedRuleShadowMode(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, true, "", nil),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, "", nil),
	}
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
	testResults := []pb.RateLimitResponse_Code{pb.RateLimitResponse_OVER_LIMIT, pb.RateLimitResponse_OVER_LIMIT}
	for i := 0; i < len(limits); i++ {
		if limits[i].ShadowMode {
			testResults[i] = pb.RateLimitResponse_OK
		}
	}
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: testResults[0], CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: testResults[1], CurrentLimit: nil, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Equal(
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: nil, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)

	t.assert.EqualValues(0, t.statStore.NewCounter("global_shadow_mode").Value())
}

func TestServiceWithCustomRatelimitHeaders(test *testing.T) {
	os.Setenv("LIMIT_RESPONSE_HEADERS_ENABLED", "true")
	os.Setenv("LIMIT_LIMIT_HEADER", "A-Ratelimit-Limit")
	os.Setenv("LIMIT_REMAINING_HEADER", "A-Ratelimit-Remaining")
	os.Setenv("LIMIT_RESET_HEADER", "A-Ratelimit-Reset")
	defer func() {
		os.Unsetenv("LIMIT_RESPONSE_HEADERS_ENABLED")
		os.Unsetenv("LIMIT_LIMIT_HEADER")
		os.Unsetenv("LIMIT_REMAINING_HEADER")
		os.Unsetenv("LIMIT_RESET_HEADER")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	// Config reload.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()
	// t.configLoader.EXPECT().Load(
	// 	[]config.RateLimitConfigToLoad{{Name: "config.basic_config", FileBytes: "fake_yaml"}}, gomock.Any(), gomock.Any()).Do(
	// 	func([]config.RateLimitConfigToLoad, stats.Manager, bool) { barrier.signal() }).Return(t.config)
	// t.runtimeUpdateCallback <- 1
	// barrier.wait()

	// Make request
	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, "", nil),
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
			ResponseHeadersToAdd: []*core.HeaderValue{
				{Key: "A-Ratelimit-Limit", Value: "10"},
				{Key: "A-Ratelimit-Remaining", Value: "0"},
				{Key: "A-Ratelimit-Reset", Value: "58"},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceWithDefaultRatelimitHeaders(test *testing.T) {
	os.Setenv("LIMIT_RESPONSE_HEADERS_ENABLED", "true")
	defer func() {
		os.Unsetenv("LIMIT_RESPONSE_HEADERS_ENABLED")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	// Config reload.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()
	// t.configLoader.EXPECT().Load(
	// 	[]config.RateLimitConfigToLoad{{Name: "config.basic_config", FileBytes: "fake_yaml"}}, gomock.Any(), gomock.Any()).Do(
	// 	func([]config.RateLimitConfigToLoad, stats.Manager, bool) { barrier.signal() }).Return(t.config)
	// t.runtimeUpdateCallback <- 1
	// barrier.wait()

	// Make request
	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, "", nil),
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
			ResponseHeadersToAdd: []*core.HeaderValue{
				{Key: "RateLimit-Limit", Value: "10"},
				{Key: "RateLimit-Remaining", Value: "0"},
				{Key: "RateLimit-Reset", Value: "58"},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestEmptyDomain(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("", [][][2]string{{{"hello", "world"}}}, 1)
	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(response)
	t.assert.Equal("rate limit domain must not be empty", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestEmptyDescriptors(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{}, 1)
	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(response)
	t.assert.Equal("rate limit descriptor list must not be empty", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestCacheError(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest("different-domain", [][][2]string{{{"foo", "bar"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, "", nil)}
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Do(
		func(context.Context, *pb.RateLimitRequest, []*config.RateLimit) {
			panic(redis.RedisError("cache error"))
		})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(response)
	t.assert.Equal("cache error", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.redis_error").Value())
}

func TestInitialLoadError(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	// t.runtime.EXPECT().AddUpdateCallback(gomock.Any()).Do(
	// 	func(callback chan<- int) { t.runtimeUpdateCallback = callback })
	// t.runtime.EXPECT().Snapshot().Return(t.snapshot).MinTimes(1)
	// t.snapshot.EXPECT().Keys().Return([]string{"foo", "config.basic_config"}).MinTimes(1)
	// t.snapshot.EXPECT().Get("config.basic_config").Return("fake_yaml").MinTimes(1)
	// t.configLoader.EXPECT().Load(
	// 	[]config.RateLimitConfigToLoad{{Name: "config.basic_config", FileBytes: "fake_yaml"}}, gomock.Any(), gomock.Any()).Do(
	// 	func([]config.RateLimitConfigToLoad, stats.Manager, bool) {
	// 		panic(config.RateLimitConfigError("load error"))
	// 	})
	t.configProvider.EXPECT().ConfigUpdateEvent().Return(t.configUpdateEventChan).Times(1)
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return nil, config.RateLimitConfigError("load error")
	})
	go func() { t.configUpdateEventChan <- t.configUpdateEvent }() // initial config update from provider
	service := ratelimit.NewService(t.runtime, t.cache, t.configProvider, t.configLoader, t.statsManager, true, t.mockClock, false)
	barrier.wait()

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(response)
	t.assert.Equal("no rate limit configuration loaded", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestUnlimited(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"some-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}, {{"baz", "qux"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("foo_bar"), false, false, "", nil),
		nil,
		config.NewRateLimit(55, pb.RateLimitResponse_RateLimit_SECOND, t.statsManager.NewStats("baz_qux"), true, false, "", nil),
	}
	t.config.EXPECT().GetLimit(context.Background(), "some-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "some-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(context.Background(), "some-domain", request.Descriptors[2]).Return(limits[2])

	// Unlimited descriptors should not hit the cache
	expectedCacheLimits := []*config.RateLimit{limits[0], nil, nil}

	t.cache.EXPECT().DoLimit(context.Background(), request, expectedCacheLimits).Return([]*pb.RateLimitResponse_DescriptorStatus{
		{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9},
		{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
		{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
	})

	response, err := service.ShouldRateLimit(context.Background(), request)
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: math.MaxUint32},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceTracer(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	// First request, config should be loaded.
	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	t.config.EXPECT().GetLimit(context.Background(), "test-domain", request.Descriptors[0]).Return(nil)
	t.cache.EXPECT().DoLimit(context.Background(), request, []*config.RateLimit{nil}).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}})

	response, err := service.ShouldRateLimit(context.Background(), request)
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}},
		},
		response)
	t.assert.Nil(err)

	spanStubs := testSpanExporter.GetSpans()
	t.assert.NotNil(spanStubs)
	t.assert.Len(spanStubs, 1)
	t.assert.Equal(spanStubs[0].Name, "ShouldRateLimit Execution")
}
