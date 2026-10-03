package ratelimit_test

import (
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ratelimit/src/trace"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/redis"
	server "github.com/envoyproxy/ratelimit/src/server"
	ratelimit "github.com/envoyproxy/ratelimit/src/service"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_config "github.com/envoyproxy/ratelimit/test/mocks/config"
	mock_limiter "github.com/envoyproxy/ratelimit/test/mocks/limiter"
	mock_provider "github.com/envoyproxy/ratelimit/test/mocks/provider"
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
	cache                 *mock_limiter.MockRateLimitCache
	configProvider        *mock_provider.MockRateLimitConfigProvider
	configUpdateEventChan chan provider.ConfigUpdateEvent
	configUpdateEvent     *mock_provider.MockConfigUpdateEvent
	config                *mock_config.MockRateLimitConfig
	health                *server.HealthChecker
	statsManager          stats.Manager
	statStore             gostats.Store
	mockClock             utils.TimeSource
	enableNegativeHits    bool
}

type MockClock struct {
	now int64
}

func (c MockClock) UnixNow() int64 { return c.now }

func commonSetup(t *testing.T) rateLimitServiceTestSuite {
	ret := rateLimitServiceTestSuite{}
	ret.assert = assert.New(t)
	ret.controller = gomock.NewController(t)
	ret.cache = mock_limiter.NewMockRateLimitCache(ret.controller)
	ret.configProvider = mock_provider.NewMockRateLimitConfigProvider(ret.controller)
	ret.configUpdateEventChan = make(chan provider.ConfigUpdateEvent)
	ret.configUpdateEvent = mock_provider.NewMockConfigUpdateEvent(ret.controller)
	// ret.configLoader = mock_config.NewMockRateLimitConfigLoader(ret.controller)
	ret.config = mock_config.NewMockRateLimitConfig(ret.controller)
	ret.statStore = gostats.NewStore(gostats.NewNullSink(), false)
	ret.statsManager = mock_stats.NewMockStatManager(ret.statStore)
	ret.health = server.NewHealthChecker(health.NewServer(), "ratelimit", false)
	// Tests use a mocked cache, so simulate a successful Redis connection.
	_ = ret.health.Ok(server.RedisHealthComponentName)
	return ret
}

func (this *rateLimitServiceTestSuite) setupBasicService() ratelimit.RateLimitServiceServer {
	barrier := newBarrier()
	this.configProvider.EXPECT().ConfigUpdateEvent().Return(this.configUpdateEventChan).Times(1)
	this.config.EXPECT().IsEmptyDomains().Return(false).AnyTimes()
	this.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return this.config, nil
	})
	go func() { this.configUpdateEventChan <- this.configUpdateEvent }() // initial config update from provider

	testSpanExporter.Reset()

	svc := ratelimit.NewService(this.cache, this.configProvider, this.statsManager, this.health, MockClock{now: int64(2222)}, false, false, false, this.enableNegativeHits)
	barrier.wait() // wait for initial config load
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
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "key_name", nil, false),
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

	// Config should still be valid. Also make sure order does not affect results.
	limits = []*config.RateLimit{
		nil,
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
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

	// Make a request.
	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)

	// Global Shadow mode
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
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, true, false, "", nil, false),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, true, false, "", nil, false),
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
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, true, false, "", nil, false),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
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

func TestRequestHeadersSettingsDefaults(test *testing.T) {
	s := settings.NewSettings()
	assert.False(test, s.RateLimitRequestHeadersEnabled)
	assert.Equal(test, "RateLimit-Limit", s.HeaderRequestRatelimitLimit)
	assert.Equal(test, "RateLimit-Remaining", s.HeaderRequestRatelimitRemaining)
	assert.Equal(test, "RateLimit-Reset", s.HeaderRequestRatelimitReset)
}

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

// TestRequestHeadersDisabledOnHotReload verifies that requestHeadersEnabled is reset to
// false on config hot-reload when LIMIT_REQUEST_HEADERS_ENABLED is turned off. Without
// the fix, the flag is only ever set to true and never cleared, so headers persist after
// the env var is removed.
func TestRequestHeadersDisabledOnHotReload(test *testing.T) {
	os.Setenv("LIMIT_REQUEST_HEADERS_ENABLED", "true")

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService() // service starts with requestHeadersEnabled = true

	// Now turn off the env var and trigger a config reload.
	os.Unsetenv("LIMIT_REQUEST_HEADERS_ENABLED")
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	// After the reload with LIMIT_REQUEST_HEADERS_ENABLED unset, request headers must be absent.
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

	// Make request
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

	// Make request
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
			ResponseHeadersToAdd: []*core.HeaderValue{
				{Key: "RateLimit-Limit", Value: "10"},
				{Key: "RateLimit-Remaining", Value: "0"},
				{Key: "RateLimit-Reset", Value: "58"},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceWithPerUnitRatelimitHeaders(test *testing.T) {
	test.Setenv("LIMIT_PER_UNIT_HEADERS_ENABLED", "true")
	test.Setenv("LIMIT_RESPONSE_HEADERS_ENABLED", "true")

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{
			{{"second", "limit"}},
			{{"minute", "limit"}},
			{{"unlimited", "descriptor"}},
		}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, t.statsManager.NewStats("second"), false, false, false, "", nil, false),
		config.NewRateLimit(1000, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("minute"), false, false, false, "", nil, false),
		nil,
	}
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 8},
			{Code: pb.RateLimitResponse_OK},
		})

	response, err := service.ShouldRateLimit(context.Background(), request)
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 8},
				{Code: pb.RateLimitResponse_OK},
			},
			ResponseHeadersToAdd: []*core.HeaderValue{
				{Key: "RateLimit-Limit-Seconds", Value: "10"},
				{Key: "RateLimit-Remaining-Seconds", Value: "9"},
				{Key: "RateLimit-Limit-Minutes", Value: "1000"},
				{Key: "RateLimit-Remaining-Minutes", Value: "8"},
				{Key: "RateLimit-Limit", Value: "1000"},
				{Key: "RateLimit-Remaining", Value: "8"},
				{Key: "RateLimit-Reset", Value: "58"},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceWithPerUnitRatelimitHeadersSameUnit(test *testing.T) {
	test.Setenv("LIMIT_PER_UNIT_HEADERS_ENABLED", "true")

	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"different-domain", [][][2]string{
			{{"ip", "192.0.2.1"}},
			{{"api_key", "example-key"}},
			{{"organization", "example-org"}},
			{{"project", "example-project"}},
		}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, t.statsManager.NewStats("ip"), false, false, false, "", nil, false),
		config.NewRateLimit(5, pb.RateLimitResponse_RateLimit_SECOND, t.statsManager.NewStats("api-key"), false, false, false, "", nil, false),
		config.NewRateLimit(100, pb.RateLimitResponse_RateLimit_DAY, t.statsManager.NewStats("organization"), false, false, false, "", nil, false),
		config.NewRateLimit(50, pb.RateLimitResponse_RateLimit_DAY, t.statsManager.NewStats("project"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[2]).Return(limits[2])
	t.config.EXPECT().GetLimit(context.Background(), "different-domain", request.Descriptors[3]).Return(limits[3])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 4},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 75},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[3].Limit, LimitRemaining: 10},
		})

	response, err := service.ShouldRateLimit(context.Background(), request)
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 4},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 75},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[3].Limit, LimitRemaining: 10},
			},
			ResponseHeadersToAdd: []*core.HeaderValue{
				{Key: "RateLimit-Limit-Seconds", Value: "5"},
				{Key: "RateLimit-Remaining-Seconds", Value: "4"},
				{Key: "RateLimit-Limit-Days", Value: "50"},
				{Key: "RateLimit-Remaining-Days", Value: "10"},
			},
		},
		response)
	t.assert.Nil(err)
}

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
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false)}
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

	t.configProvider.EXPECT().ConfigUpdateEvent().Return(t.configUpdateEventChan).Times(1)
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return nil, config.RateLimitConfigError("load error")
	})
	go func() { t.configUpdateEventChan <- t.configUpdateEvent }() // initial config update from provider
	service := ratelimit.NewService(t.cache, t.configProvider, t.statsManager, t.health, t.mockClock, false, false, false, false)
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
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("foo_bar"), false, false, false, "", nil, false),
		nil,
		config.NewRateLimit(55, pb.RateLimitResponse_RateLimit_SECOND, t.statsManager.NewStats("baz_qux"), true, false, false, "", nil, false),
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

func TestServiceHealthStatus(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	defer signal.Reset(syscall.SIGTERM)

	healthyWithAtLeastOneConfigLoaded := false
	grpcHealthServer := health.NewServer()
	hc := server.NewHealthChecker(grpcHealthServer, "ratelimit", healthyWithAtLeastOneConfigLoaded)
	// Tests use a mocked cache, so simulate a successful Redis connection.
	_ = hc.Ok(server.RedisHealthComponentName)
	healthpb.RegisterHealthServer(grpc.NewServer(), grpcHealthServer)

	// Set up the service
	t.configProvider.EXPECT().ConfigUpdateEvent().Return(t.configUpdateEventChan).Times(1)
	_ = ratelimit.NewService(t.cache, t.configProvider, t.statsManager, hc, MockClock{now: int64(2222)}, false, true, healthyWithAtLeastOneConfigLoaded, false)

	// Health check request
	req := &healthpb.HealthCheckRequest{
		Service: "ratelimit",
	}

	// Service should report healthy at start.
	res, _ := grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_SERVING != res.Status {
		test.Errorf("expected status SERVING actual %v", res.Status)
	}
}

func TestServiceHealthStatusAtLeastOneConfigLoaded(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	defer signal.Reset(syscall.SIGTERM)

	healthyWithAtLeastOneConfigLoaded := true
	grpcHealthServer := health.NewServer()
	hc := server.NewHealthChecker(grpcHealthServer, "ratelimit", healthyWithAtLeastOneConfigLoaded)
	// Tests use a mocked cache, so simulate a successful Redis connection.
	_ = hc.Ok(server.RedisHealthComponentName)
	healthpb.RegisterHealthServer(grpc.NewServer(), grpcHealthServer)

	// Set up the service
	t.configProvider.EXPECT().ConfigUpdateEvent().Return(t.configUpdateEventChan).Times(1)
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		return t.config, nil
	}).Times(2)
	service := ratelimit.NewService(t.cache, t.configProvider, t.statsManager, hc, MockClock{now: int64(2222)}, false, true, healthyWithAtLeastOneConfigLoaded, false)
	// Health check request
	req := &healthpb.HealthCheckRequest{
		Service: "ratelimit",
	}

	// Service should report unhealthy since no config loaded at start
	res, _ := grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_NOT_SERVING != res.Status {
		test.Errorf("expected status NOT_SERVING actual %v", res.Status)
	}

	// Force a config load - config event from config provider.
	t.config.EXPECT().IsEmptyDomains().DoAndReturn(func() bool {
		return false
	}).Times(1)
	service.SetConfig(t.configUpdateEvent, healthyWithAtLeastOneConfigLoaded)

	// Service should report healthy since config loaded
	res, _ = grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_SERVING != res.Status {
		test.Errorf("expected status SERVING actual %v", res.Status)
	}

	// Force reload of an invalid config with no domains - config event from config provider.
	t.config.EXPECT().IsEmptyDomains().DoAndReturn(func() bool {
		return true
	}).Times(1)
	service.SetConfig(t.configUpdateEvent, healthyWithAtLeastOneConfigLoaded)

	// Service should report unhealthy since no config loaded at start
	res, _ = grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_NOT_SERVING != res.Status {
		test.Errorf("expected status NOT_SERVING actual %v", res.Status)
	}
}

func TestServiceGlobalQuotaMode(test *testing.T) {
	os.Setenv("QUOTA_MODE", "true")
	defer func() {
		os.Unsetenv("QUOTA_MODE")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	// No global quota_mode, this should be picked-up from environment variables during re-load of config
	service := t.setupBasicService()

	// Force a config reload.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	// Make a request.
	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)

	// Global Quota mode
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
		config.NewRateLimit(5, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key2"), false, false, false, "", nil, false),
	}
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// OVER_LIMIT overall code since all quota limits were OVER_LIMIT
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestMetadataReturnedForPassedDescriptors(test *testing.T) {
	os.Setenv("QUOTA_MODE", "true")
	os.Setenv("RESPONSE_DYNAMIC_METADATA", "true")
	defer func() {
		os.Unsetenv("QUOTA_MODE")
		os.Unsetenv("RESPONSE_DYNAMIC_METADATA")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	// Force a config reload to pick up environment variables.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	// Make a request.
	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, false, "", nil, false),
		config.NewRateLimit(5, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key2"), false, false, true, "", nil, false),
	}
	limits[0].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("service_1")}}
	limits[1].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("service_2")}}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)
	test.Logf("DynamicMetadata: %+v", response.DynamicMetadata)

	// Verify response includes metadata about quota violations
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.NotNil(response.DynamicMetadata)

	// Verify metadata for passed limits
	passedMetadataVal, ok := response.DynamicMetadata.GetFields()["metadata"]
	t.assert.True(ok)
	passedMetadata := passedMetadataVal.GetStructValue()
	t.assert.NotNil(passedMetadata)

	fields := passedMetadata.GetFields()
	nameVal, ok := fields["name"]
	t.assert.True(ok)
	// Since descriptor 1 has passed and 2 had failed, metadata from the first descriptors should be returned
	t.assert.Equal("service_1", nameVal.GetStringValue())
}

func TestMetadataReturnedForAllPassedDescriptors(test *testing.T) {
	os.Setenv("QUOTA_MODE", "true")
	os.Setenv("RESPONSE_DYNAMIC_METADATA", "true")
	defer func() {
		os.Unsetenv("QUOTA_MODE")
		os.Unsetenv("RESPONSE_DYNAMIC_METADATA")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	// Force a config reload to pick up environment variables.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	// Make a request.
	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, true, "", nil, false),
		config.NewRateLimit(5, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key2"), false, false, true, "", nil, false),
	}
	limits[0].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("service_1")}}
	limits[1].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"some_other_name": structpb.NewStringValue("service_2")}}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 6},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)
	test.Logf("DynamicMetadata: %+v", response.DynamicMetadata)

	// Verify response includes metadata about quota violations
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.NotNil(response.DynamicMetadata)

	// Verify metadata for passed limits
	passedMetadataVal, ok := response.DynamicMetadata.GetFields()["metadata"]
	t.assert.True(ok)
	passedMetadata := passedMetadataVal.GetStructValue()
	t.assert.NotNil(passedMetadata)

	fields := passedMetadata.GetFields()
	nameVal, ok := fields["name"]
	t.assert.True(ok)
	// Both descriptors have passed metadata should contain values from both descriptors
	t.assert.Equal("service_1", nameVal.GetStringValue())
	nameVal, ok = fields["some_other_name"]
	t.assert.True(ok)
	t.assert.Equal("service_2", nameVal.GetStringValue())
}

func TestOverlappingMetadataReturnsTheFirstValue(test *testing.T) {
	os.Setenv("QUOTA_MODE", "true")
	os.Setenv("RESPONSE_DYNAMIC_METADATA", "true")
	defer func() {
		os.Unsetenv("QUOTA_MODE")
		os.Unsetenv("RESPONSE_DYNAMIC_METADATA")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	// Force a config reload to pick up environment variables.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	// Make a request.
	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key"), false, false, true, "", nil, false),
		config.NewRateLimit(5, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("key2"), false, false, true, "", nil, false),
	}
	limits[0].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("service_1")}}
	limits[1].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("service_2")}}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 6},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)
	test.Logf("DynamicMetadata: %+v", response.DynamicMetadata)

	// Verify response includes metadata about quota violations
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.NotNil(response.DynamicMetadata)

	// Verify metadata for passed limits
	passedMetadataVal, ok := response.DynamicMetadata.GetFields()["metadata"]
	t.assert.True(ok)
	passedMetadata := passedMetadataVal.GetStructValue()
	t.assert.NotNil(passedMetadata)

	fields := passedMetadata.GetFields()
	nameVal, ok := fields["name"]
	t.assert.True(ok)
	// Metadata from the first descriptor takes precendence
	t.assert.Equal("service_1", nameVal.GetStringValue())
}

func TestServicePerDescriptorQuotaMode(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	// No Global Quota mode
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	// Create limits with one having quota mode enabled per-descriptor
	limits := []*config.RateLimit{
		// Regular limit - should reject when exceeded
		{
			FullKey:    "regular_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 5, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  false,
			ShadowMode: false,
		},
		// Quota mode limit - should not reject when exceeded
		{
			FullKey:    "quota_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 3, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Regular limit should cause OVER_LIMIT overall, even though quota mode is under the limit
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceMixedPerDescriptorModes(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	// No Global Quota mode
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	// Create limits with one having quota mode enabled per-descriptor
	// In this configuration the limits will be evaluated as rate limits.
	limits := []*config.RateLimit{
		// Regular limit
		{
			FullKey:    "regular_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 5, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  false,
			ShadowMode: false,
		},
		// Quota mode limit
		{
			FullKey:    "quota_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 3, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Overall result is OVER_LIMIT, since all quota limits were exceeded
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceMixedPerDescriptorModesUnderLimit(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	// No Global Quota mode
	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	// Create limits with one having quota mode enabled per-descriptor
	// In this configuration the limits will be evaluated as rate limits.
	limits := []*config.RateLimit{
		// Regular limit
		{
			FullKey:    "regular_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 5, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  false,
			ShadowMode: false,
		},
		// Quota mode limit
		{
			FullKey:    "quota_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 3, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Overall result is OVER_LIMIT, since all quota limits were exceeded
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceQuotaModeOnlyAllOverTheLimit(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"quota1", "limit"}}, {{"quota2", "limit"}}}, 1)

	// Both limits are in quota mode
	limits := []*config.RateLimit{
		{
			FullKey:    "quota_limit_1",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 5, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
		{
			FullKey:    "quota_limit_2",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 3, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Since quota limits were exceeded overall result in OVER_LIMIT
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)
}

func TestServiceQuotaModeOnlySomeOverTheLimit(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"quota1", "limit"}}, {{"quota2", "limit"}}}, 1)

	// Both limits are in quota mode
	limits := []*config.RateLimit{
		{
			FullKey:    "quota_limit_1",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 5, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
		{
			FullKey:    "quota_limit_2",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 3, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Since only some quota limits were exceeded overall result is OK
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)
}

// quotaGroupDescriptors returns a two-level model descriptor (backend_name +
// model_name_override) with a trailing bucket entry, used to exercise quota
// grouping where several buckets share one model group.
func quotaGroupDescriptor(backend, model, bucketKey string) [][2]string {
	return [][2]string{
		{"backend_name", backend},
		{"model_name_override", model},
		{bucketKey, bucketKey},
	}
}

// TestServiceQuotaModeSameGroupTenantOverDefaultOk verifies
// within a single model group, if the per-tenant bucket is over the limit the
// whole group (and therefore the request) is OVER_LIMIT even though the model's
// default bucket still has quota.
func TestServiceQuotaModeSameGroupTenantOverDefaultOk(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	request := common.NewRateLimitRequest("quota-domain", [][][2]string{
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-0-tenant-match-0"),
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-1-match--1"),
	}, 1)

	limits := []*config.RateLimit{
		{
			FullKey:    "tenant_bucket",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 100, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
		{
			FullKey:    "default_bucket",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 150, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 90},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 90},
			},
		},
		response)
	t.assert.Nil(err)
}

// TestServiceQuotaModeSameGroupDefaultOverTenantOk verifies that
// the model's default (ceiling) bucket being over the limit
// makes the group OVER_LIMIT even though the per-tenant bucket still has quota.
func TestServiceQuotaModeSameGroupDefaultOverTenantOk(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	request := common.NewRateLimitRequest("quota-domain", [][][2]string{
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-0-tenant-match-0"),
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-1-match--1"),
	}, 1)

	limits := []*config.RateLimit{
		{
			FullKey:    "tenant_bucket",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 100, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
		{
			FullKey:    "default_bucket",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 150, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 40},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 40},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)
}

// TestServiceQuotaModeMultiModelFailover verifies that with two model groups on a
// route, one model being fully exhausted does not reject the request while
// another model still has quota (AND across groups → failover preserved).
func TestServiceQuotaModeMultiModelFailover(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	request := common.NewRateLimitRequest("quota-domain", [][][2]string{
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-0-tenant-match-0"),
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-1-match--1"),
		quotaGroupDescriptor("ns/be", "gpt-5-mini", "rule-0-match--1"),
	}, 1)

	limits := []*config.RateLimit{
		{FullKey: "a_tenant", Limit: &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 100, Unit: pb.RateLimitResponse_RateLimit_MINUTE}, QuotaMode: true},
		{FullKey: "a_default", Limit: &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 150, Unit: pb.RateLimitResponse_RateLimit_MINUTE}, QuotaMode: true},
		{FullKey: "b_default", Limit: &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 250, Unit: pb.RateLimitResponse_RateLimit_MINUTE}, QuotaMode: true},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 200},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Model A group is fully exhausted, but model B still has quota → overall OK.
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.Nil(err)
}

// TestServiceQuotaModeMultiModelAllOver verifies that when every model group is
// exhausted the request is OVER_LIMIT.
func TestServiceQuotaModeMultiModelAllOver(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	request := common.NewRateLimitRequest("quota-domain", [][][2]string{
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-0-tenant-match-0"),
		quotaGroupDescriptor("ns/be", "gpt-4o-mini", "rule-1-match--1"),
		quotaGroupDescriptor("ns/be", "gpt-5-mini", "rule-0-match--1"),
	}, 1)

	limits := []*config.RateLimit{
		{FullKey: "a_tenant", Limit: &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 100, Unit: pb.RateLimitResponse_RateLimit_MINUTE}, QuotaMode: true},
		{FullKey: "a_default", Limit: &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 150, Unit: pb.RateLimitResponse_RateLimit_MINUTE}, QuotaMode: true},
		{FullKey: "b_default", Limit: &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 250, Unit: pb.RateLimitResponse_RateLimit_MINUTE}, QuotaMode: true},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[2].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	t.assert.Equal(pb.RateLimitResponse_OVER_LIMIT, response.OverallCode)
	t.assert.Nil(err)
}

// TestQuotaMetadataExcludesExhaustedGroup verifies a passed descriptor
// whose model group is exhausted is not advertised in the response dynamic
// metadata, while an available model group still is.
func TestQuotaMetadataExcludesExhaustedGroup(test *testing.T) {
	os.Setenv("QUOTA_MODE", "true")
	os.Setenv("RESPONSE_DYNAMIC_METADATA", "true")
	defer func() {
		os.Unsetenv("QUOTA_MODE")
		os.Unsetenv("RESPONSE_DYNAMIC_METADATA")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	// Force a config reload to pick up environment variables.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	request := common.NewRateLimitRequest("quota-domain", [][][2]string{
		quotaGroupDescriptor("ns/be", "model-a", "rule-0-tenant-match-0"),
		quotaGroupDescriptor("ns/be", "model-a", "rule-1-match--1"),
		quotaGroupDescriptor("ns/be", "model-b", "rule-0-match--1"),
	}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(100, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("a_tenant"), false, false, true, "", nil, false),
		config.NewRateLimit(150, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("a_default"), false, false, true, "", nil, false),
		config.NewRateLimit(250, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("b_default"), false, false, true, "", nil, false),
	}
	limits[0].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("model_a_tenant")}}
	limits[1].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("model_a_default")}}
	limits[2].Metadata = &structpb.Struct{Fields: map[string]*structpb.Value{"name": structpb.NewStringValue("model_b")}}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[2]).Return(limits[2])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			// model-a tenant bucket exhausted → model-a group is over.
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			// model-a default bucket passed, but its group is exhausted.
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 90},
			// model-b default bucket passed and its group still has quota.
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[2].Limit, LimitRemaining: 200},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)

	// model-b still has quota, so overall is OK.
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.NotNil(response.DynamicMetadata)

	passedMetadataVal, ok := response.DynamicMetadata.GetFields()["metadata"]
	t.assert.True(ok)
	fields := passedMetadataVal.GetStructValue().GetFields()
	nameVal, ok := fields["name"]
	t.assert.True(ok)
	// Only model-b (the group that still has quota) is advertised; model-a's
	// passed default bucket is excluded because model-a's group is exhausted.
	t.assert.Equal("model_b", nameVal.GetStringValue())

	// The passedBackends list advertises only the non-exhausted (backend, model)
	// pair. model-a is excluded entirely (its group is exhausted) even though its
	// default bucket individually passed, and its two buckets never produce more
	// than one entry.
	t.assert.Equal([][2]string{
		{"ns/be", "model-b"},
	}, passedBackendsPairs(response.DynamicMetadata))
}

// passedBackendsPairs extracts the (backend_name, model_name_override) pairs from
// the passedBackends field of the response dynamic metadata, preserving order.
func passedBackendsPairs(metadata *structpb.Struct) [][2]string {
	val, ok := metadata.GetFields()["passedBackends"]
	if !ok {
		return nil
	}
	var pairs [][2]string
	for _, entry := range val.GetListValue().GetValues() {
		fields := entry.GetStructValue().GetFields()
		pairs = append(pairs, [2]string{
			fields["backend_name"].GetStringValue(),
			fields["model_name_override"].GetStringValue(),
		})
	}
	return pairs
}

// TestQuotaMetadataSameModelDifferentBackends verifies that backend_name is part
// of the quota group identity. If one backend is exhausted and another backend
// serving the same model remains available, the request stays OK and only the
// live backend/model pair is advertised.
func TestQuotaMetadataSameModelDifferentBackends(test *testing.T) {
	os.Setenv("QUOTA_MODE", "true")
	os.Setenv("RESPONSE_DYNAMIC_METADATA", "true")
	defer func() {
		os.Unsetenv("QUOTA_MODE")
		os.Unsetenv("RESPONSE_DYNAMIC_METADATA")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	// Force a config reload to pick up environment variables.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	request := common.NewRateLimitRequest("quota-domain", [][][2]string{
		quotaGroupDescriptor("ns/backend-a", "gpt-4o-mini", "rule-0-match--1"),
		quotaGroupDescriptor("ns/backend-b", "gpt-4o-mini", "rule-0-match--1"),
	}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(100, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("backend_a"), false, false, true, "", nil, false),
		config.NewRateLimit(100, pb.RateLimitResponse_RateLimit_MINUTE, t.statsManager.NewStats("backend_b"), false, false, true, "", nil, false),
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			// backend-a's group is exhausted.
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			// backend-b serves the same model and still has quota.
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 99},
		})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(err)
	t.assert.Equal(pb.RateLimitResponse_OK, response.OverallCode)
	t.assert.NotNil(response.DynamicMetadata)
	t.assert.Equal([][2]string{
		{"ns/backend-b", "gpt-4o-mini"},
	}, passedBackendsPairs(response.DynamicMetadata))
}

func TestServiceQuotaModeWithShadowMode(test *testing.T) {
	os.Setenv("SHADOW_MODE", "true")
	defer func() {
		os.Unsetenv("SHADOW_MODE")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	// Force a config reload to pick up environment variables.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	// Mix of regular and quota mode limits with global shadow mode
	limits := []*config.RateLimit{
		{
			FullKey:    "regular_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 5, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
		{
			FullKey:    "quota_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 3, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Global shadow mode should override everything and result in OK
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)

	// Verify global shadow mode counter is incremented
	t.assert.EqualValues(1, t.statStore.NewCounter("global_shadow_mode").Value())
}

func TestServiceMixedModeWithShadowMode(test *testing.T) {
	os.Setenv("SHADOW_MODE", "true")
	defer func() {
		os.Unsetenv("SHADOW_MODE")
	}()

	t := commonSetup(test)
	defer t.controller.Finish()

	service := t.setupBasicService()

	// Force a config reload to pick up environment variables.
	barrier := newBarrier()
	t.configUpdateEvent.EXPECT().GetConfig().DoAndReturn(func() (config.RateLimitConfig, any) {
		barrier.signal()
		return t.config, nil
	})
	t.configUpdateEventChan <- t.configUpdateEvent
	barrier.wait()

	request := common.NewRateLimitRequest(
		"quota-domain", [][][2]string{{{"regular", "limit"}}, {{"quota", "limit"}}}, 1)

	// Mix of regular and quota mode limits with global shadow mode
	limits := []*config.RateLimit{
		{
			FullKey:    "regular_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 5, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  false,
			ShadowMode: false,
		},
		{
			FullKey:    "quota_limit",
			Limit:      &pb.RateLimitResponse_RateLimit{RequestsPerUnit: 3, Unit: pb.RateLimitResponse_RateLimit_MINUTE},
			QuotaMode:  true,
			ShadowMode: false,
		},
	}

	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(context.Background(), "quota-domain", request.Descriptors[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(context.Background(), request, limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
		})
	response, err := service.ShouldRateLimit(context.Background(), request)

	// Global shadow mode should override everything and result in OK
	common.AssertProtoEqual(
		t.assert,
		&pb.RateLimitResponse{
			OverallCode: pb.RateLimitResponse_OK,
			Statuses: []*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0},
			},
		},
		response)
	t.assert.Nil(err)

	// Verify global shadow mode counter is incremented
	t.assert.EqualValues(1, t.statStore.NewCounter("global_shadow_mode").Value())
}

func TestNegativeHitsRejectedWhenFlagOff(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	// The rejection happens before the config lookup and the cache call, so
	// neither the config nor the cache mock expects any call here.
	request := common.NewRateLimitRequestWithNegativeHits(
		"test-domain", [][][2]string{{{"hello", "world"}}}, []uint64{3}, []bool{true})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(response)
	t.assert.Equal(codes.Unimplemented, status.Code(err))
	t.assert.EqualValues(1, t.statStore.NewCounter("negative_hits_rejected").Value())
	t.assert.EqualValues(0, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestNegativeHitsRejectedWhenFlagOffMixedBatch(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	// A single negative-hit descriptor fails the whole batch, including the
	// positive descriptor in it.
	request := common.NewRateLimitRequestWithNegativeHits(
		"test-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, []uint64{1, 3}, []bool{false, true})

	response, err := service.ShouldRateLimit(context.Background(), request)
	t.assert.Nil(response)
	t.assert.Equal(codes.Unimplemented, status.Code(err))
	t.assert.EqualValues(1, t.statStore.NewCounter("negative_hits_rejected").Value())
}

func TestNegativeHitsAllowedWhenFlagOn(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	t.enableNegativeHits = true
	service := t.setupBasicService()

	request := common.NewRateLimitRequestWithNegativeHits(
		"test-domain", [][][2]string{{{"hello", "world"}}}, []uint64{3}, []bool{true})
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
	t.assert.EqualValues(0, t.statStore.NewCounter("negative_hits_rejected").Value())
}
