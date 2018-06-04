package ratelimit_test

import (
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/lyft/gostats"
	pb_struct "github.com/lyft/ratelimit/proto/envoy/api/v2/ratelimit"
	pb "github.com/lyft/ratelimit/proto/envoy/service/ratelimit/v2"
	pb_legacy "github.com/lyft/ratelimit/proto/ratelimit"
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
	assert                *assert.Assertions
	controller            *gomock.Controller
	runtime               *mock_loader.MockIFace
	snapshot              *mock_snapshot.MockIFace
	cache                 *mock_redis.MockRateLimitCache
	configLoader          *mock_config.MockRateLimitConfigLoader
	config                *mock_config.MockRateLimitConfig
	runtimeUpdateCallback chan<- int
	statStore             stats.Store
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
	return ratelimit.NewService(this.runtime, this.cache, this.configLoader, this.statStore)
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

func TestServiceLegacy(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	// First request, config should be loaded.
	request := common.NewRateLimitRequestLegacy("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	t.config.EXPECT().GetLimit(nil, "test-domain", ratelimit.ConvertLegacyDescriptors(request.Descriptors)[0]).Return(nil)
	t.cache.EXPECT().DoLimit(nil, ratelimit.ConvertLegacyRequest(request), []*config.RateLimit{nil}).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}})

	response, err := service.GetLegacyService().ShouldRateLimit(nil, request)
	t.assert.Equal(
		&pb_legacy.RateLimitResponse{
			OverallCode: pb_legacy.RateLimitResponse_OK,
			Statuses:    []*pb_legacy.RateLimitResponse_DescriptorStatus{{Code: pb_legacy.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}}},
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
	request = common.NewRateLimitRequestLegacy(
		"different-domain", [][][2]string{{{"foo", "bar"}}, {{"hello", "world"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore),
		nil}
	t.config.EXPECT().GetLimit(nil, "different-domain", ratelimit.ConvertLegacyDescriptors(request.Descriptors)[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "different-domain", ratelimit.ConvertLegacyDescriptors(request.Descriptors)[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(nil, ratelimit.ConvertLegacyRequest(request), limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}})
	response, err = service.GetLegacyService().ShouldRateLimit(nil, request)
	t.assert.Equal(
		&pb_legacy.RateLimitResponse{
			OverallCode: pb_legacy.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb_legacy.RateLimitResponse_DescriptorStatus{
				{Code: pb_legacy.RateLimitResponse_OVER_LIMIT, CurrentLimit: ratelimit.ConvertRatelimit(limits[0].Limit), LimitRemaining: 0},
				{Code: pb_legacy.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
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
	t.config.EXPECT().GetLimit(nil, "different-domain", ratelimit.ConvertLegacyDescriptors(request.Descriptors)[0]).Return(limits[0])
	t.config.EXPECT().GetLimit(nil, "different-domain", ratelimit.ConvertLegacyDescriptors(request.Descriptors)[1]).Return(limits[1])
	t.cache.EXPECT().DoLimit(nil, ratelimit.ConvertLegacyRequest(request), limits).Return(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0}})
	response, err = service.GetLegacyService().ShouldRateLimit(nil, request)
	t.assert.Equal(
		&pb_legacy.RateLimitResponse{
			OverallCode: pb_legacy.RateLimitResponse_OVER_LIMIT,
			Statuses: []*pb_legacy.RateLimitResponse_DescriptorStatus{
				{Code: pb_legacy.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
				{Code: pb_legacy.RateLimitResponse_OVER_LIMIT, CurrentLimit: ratelimit.ConvertRatelimit(limits[1].Limit), LimitRemaining: 0},
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

func TestEmptyDomainLegacy(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequestLegacy("", [][][2]string{{{"hello", "world"}}}, 1)
	response, err := service.GetLegacyService().ShouldRateLimit(nil, request)
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

func TestEmptyDescriptorsLegacy(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequestLegacy("test-domain", [][][2]string{}, 1)
	response, err := service.GetLegacyService().ShouldRateLimit(nil, request)
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

func TestCacheErrorLegacy(test *testing.T) {
	t := commonSetup(test)
	defer t.controller.Finish()
	service := t.setupBasicService()

	request := common.NewRateLimitRequestLegacy("different-domain", [][][2]string{{{"foo", "bar"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key", t.statStore)}
	t.config.EXPECT().GetLimit(nil, "different-domain", ratelimit.ConvertLegacyDescriptors(request.Descriptors)[0]).Return(limits[0])
	t.cache.EXPECT().DoLimit(nil, ratelimit.ConvertLegacyRequest(request), limits).Do(
		func(context.Context, *pb.RateLimitRequest, []*config.RateLimit) {
			panic(redis.RedisError("cache error"))
		})

	response, err := service.GetLegacyService().ShouldRateLimit(nil, request)
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
	service := ratelimit.NewService(t.runtime, t.cache, t.configLoader, t.statStore)

	request := common.NewRateLimitRequest("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	response, err := service.ShouldRateLimit(nil, request)
	t.assert.Nil(response)
	t.assert.Equal("no rate limit configuration loaded", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestInitialLoadErrorLegacy(test *testing.T) {
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
	service := ratelimit.NewService(t.runtime, t.cache, t.configLoader, t.statStore)

	request := common.NewRateLimitRequestLegacy("test-domain", [][][2]string{{{"hello", "world"}}}, 1)
	response, err := service.GetLegacyService().ShouldRateLimit(nil, request)
	t.assert.Nil(response)
	t.assert.Equal("no rate limit configuration loaded", err.Error())
	t.assert.EqualValues(1, t.statStore.NewCounter("call.should_rate_limit.service_error").Value())
}

func TestConvertLegacyRequest(test *testing.T) {
	assert.Nil(test, ratelimit.ConvertLegacyRequest(nil))

	{
		request := &pb_legacy.RateLimitRequest{
			Domain:      "test",
			Descriptors: nil,
			HitsAddend:  10,
		}

		expectedRequest := &pb.RateLimitRequest{
			Domain:      "test",
			Descriptors: nil,
			HitsAddend:  10,
		}

		assert.Equal(test, expectedRequest, ratelimit.ConvertLegacyRequest(request))
	}

	{
		request := &pb_legacy.RateLimitRequest{
			Domain:      "test",
			Descriptors: []*pb_legacy.RateLimitDescriptor{},
			HitsAddend:  10,
		}

		expectedRequest := &pb.RateLimitRequest{
			Domain:      "test",
			Descriptors: []*pb_struct.RateLimitDescriptor{},
			HitsAddend:  10,
		}

		assert.Equal(test, expectedRequest, ratelimit.ConvertLegacyRequest(request))
	}

	{
		descriptors := []*pb_legacy.RateLimitDescriptor{
			{
				Entries: []*pb_legacy.RateLimitDescriptor_Entry{
					{
						Key:   "foo",
						Value: "foo_value",
					},
					nil,
				},
			},
			{
				Entries: []*pb_legacy.RateLimitDescriptor_Entry{},
			},
			{
				Entries: nil,
			},
			nil,
		}

		request := &pb_legacy.RateLimitRequest{
			Domain:      "test",
			Descriptors: descriptors,
			HitsAddend:  10,
		}

		expectedRequest := &pb.RateLimitRequest{
			Domain:      "test",
			Descriptors: ratelimit.ConvertLegacyDescriptors(descriptors),
			HitsAddend:  10,
		}

		assert.Equal(test, expectedRequest, ratelimit.ConvertLegacyRequest(request))
	}
}

func TestConvertLegacyDescriptors(test *testing.T) {
	//(legacyDescriptors []*pb_legacy.RateLimitDescriptor) []*pb_struct.RateLimitDescriptor
	assert.Empty(test, ratelimit.ConvertLegacyDescriptors([]*pb_legacy.RateLimitDescriptor{}))

	descriptors := []*pb_legacy.RateLimitDescriptor{
		{
			Entries: []*pb_legacy.RateLimitDescriptor_Entry{
				{
					Key:   "foo",
					Value: "foo_value",
				},
				nil,
			},
		},
		{
			Entries: []*pb_legacy.RateLimitDescriptor_Entry{},
		},
		{
			Entries: nil,
		},
		nil,
	}

	expectedDescriptors := []*pb_struct.RateLimitDescriptor{
		{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{
				{
					Key:   "foo",
					Value: "foo_value",
				},
				nil,
			},
		},
		{
			Entries: []*pb_struct.RateLimitDescriptor_Entry{},
		},
		{
			Entries: nil,
		},
		nil,
	}

	assert.Equal(test, expectedDescriptors, ratelimit.ConvertLegacyDescriptors(descriptors))

}

func TestConvertLegacyEntries(test *testing.T) {
	//(legacyDescriptors []*pb_legacy.RateLimitDescriptor) []*pb_struct.RateLimitDescriptor
	assert.Nil(test, ratelimit.ConvertLegacyEntries(nil))
	assert.Empty(test, ratelimit.ConvertLegacyEntries([]*pb_legacy.RateLimitDescriptor_Entry{}))

	entries := []*pb_legacy.RateLimitDescriptor_Entry{
		{
			Key:   "foo",
			Value: "foo_value",
		},
		nil,
	}

	expectedEntries := []*pb_struct.RateLimitDescriptor_Entry{
		{
			Key:   "foo",
			Value: "foo_value",
		},
		nil,
	}

	assert.Equal(test, expectedEntries, ratelimit.ConvertLegacyEntries(entries))

}

func TestConvertResponse(test *testing.T) {
	//(response *pb.RateLimitResponse) *pb_legacy.RateLimitResponse
	assert.Nil(test, ratelimit.ConvertResponse(nil))

	rl := &pb.RateLimitResponse_RateLimit{
		RequestsPerUnit: 10,
		Unit:            pb.RateLimitResponse_RateLimit_DAY,
	}

	statuses := []*pb.RateLimitResponse_DescriptorStatus{
		{
			Code:           pb.RateLimitResponse_OK,
			CurrentLimit:   nil,
			LimitRemaining: 9,
		},
		nil,
		{
			Code:           pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:   rl,
			LimitRemaining: 0,
		},
	}

	statusesLegacy := ratelimit.ConvertResponseStatuses(statuses)

	response := &pb.RateLimitResponse{
		OverallCode: pb.RateLimitResponse_OVER_LIMIT,
		Statuses:    statuses,
	}

	expectedResponse := &pb_legacy.RateLimitResponse{
		OverallCode: pb_legacy.RateLimitResponse_OVER_LIMIT,
		Statuses:    statusesLegacy,
	}

	assert.Equal(test, expectedResponse, ratelimit.ConvertResponse(response))
}

func TestConvertResponseCode(test *testing.T) {
	//(code pb.RateLimitResponse_Code) pb_legacy.RateLimitResponse_Code
	assert.Equal(test, pb_legacy.RateLimitResponse_OK, ratelimit.ConvertResponseCode(pb.RateLimitResponse_OK))
	assert.Equal(test, pb_legacy.RateLimitResponse_OVER_LIMIT, ratelimit.ConvertResponseCode(pb.RateLimitResponse_OVER_LIMIT))
	assert.Equal(test, pb_legacy.RateLimitResponse_UNKNOWN, ratelimit.ConvertResponseCode(pb.RateLimitResponse_UNKNOWN))
}

func TestConvertResponseStatuses(test *testing.T) {
	//(statuses []*pb.RateLimitResponse_DescriptorStatus) []*pb_legacy.RateLimitResponse_DescriptorStatus
	assert.Nil(test, ratelimit.ConvertResponseStatuses(nil))
	assert.Empty(test, ratelimit.ConvertResponseStatuses([]*pb.RateLimitResponse_DescriptorStatus{}))

	rl := &pb.RateLimitResponse_RateLimit{
		RequestsPerUnit: 10,
		Unit:            pb.RateLimitResponse_RateLimit_DAY,
	}

	rlLegacy := ratelimit.ConvertRatelimit(rl)

	statuses := []*pb.RateLimitResponse_DescriptorStatus{
		{
			Code:           pb.RateLimitResponse_OK,
			CurrentLimit:   nil,
			LimitRemaining: 9,
		},
		nil,
		{
			Code:           pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:   rl,
			LimitRemaining: 0,
		},
	}

	expectedStatuses := []*pb_legacy.RateLimitResponse_DescriptorStatus{
		{
			Code:           pb_legacy.RateLimitResponse_OK,
			CurrentLimit:   nil,
			LimitRemaining: 9,
		},
		nil,
		{
			Code:           pb_legacy.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:   rlLegacy,
			LimitRemaining: 0,
		},
	}

	assert.Equal(test, expectedStatuses, ratelimit.ConvertResponseStatuses(statuses))
}

func TestConvertRatelimit(test *testing.T) {
	//(ratelimit *pb.RateLimitResponse_RateLimit) *pb_legacy.RateLimit
	assert.Nil(test, ratelimit.ConvertRatelimit(nil))

	rl := &pb.RateLimitResponse_RateLimit{
		RequestsPerUnit: 10,
		Unit:            pb.RateLimitResponse_RateLimit_DAY,
	}

	expected := &pb_legacy.RateLimit{
		RequestsPerUnit: 10,
		Unit:            pb_legacy.RateLimit_DAY,
	}

	assert.Equal(test, expected, ratelimit.ConvertRatelimit(rl))
}

func TestConvertRatelimitUnit(test *testing.T) {
	assert.Equal(test, pb_legacy.RateLimit_SECOND, ratelimit.ConvertRatelimitUnit(pb.RateLimitResponse_RateLimit_SECOND))
	assert.Equal(test, pb_legacy.RateLimit_MINUTE, ratelimit.ConvertRatelimitUnit(pb.RateLimitResponse_RateLimit_MINUTE))
	assert.Equal(test, pb_legacy.RateLimit_HOUR, ratelimit.ConvertRatelimitUnit(pb.RateLimitResponse_RateLimit_HOUR))
	assert.Equal(test, pb_legacy.RateLimit_DAY, ratelimit.ConvertRatelimitUnit(pb.RateLimitResponse_RateLimit_DAY))
	assert.Equal(test, pb_legacy.RateLimit_UNKNOWN, ratelimit.ConvertRatelimitUnit(pb.RateLimitResponse_RateLimit_UNKNOWN))
}
