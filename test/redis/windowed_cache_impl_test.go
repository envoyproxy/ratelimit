package redis_test

import (
	"math/rand"
	"testing"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_limiter "github.com/envoyproxy/ratelimit/test/mocks/limiter"
	mock_redis "github.com/envoyproxy/ratelimit/test/mocks/redis"
	"github.com/golang/mock/gomock"
	"github.com/golang/protobuf/ptypes/duration"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
)

func TestRedisWindowed(t *testing.T) {
	t.Run("WithoutPerSecondRedis", testRedisWindowed(false))
	t.Run("WithPerSecondRedis", testRedisWindowed(true))
}

func testRedisWindowed(usePerSecondRedis bool) func(*testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)
		controller := gomock.NewController(t)
		defer controller.Finish()

		client := mock_redis.NewMockClient(controller)
		perSecondClient := mock_redis.NewMockClient(controller)
		timeSource := mock_limiter.NewMockTimeSource(controller)
		var cache limiter.RateLimitCache
		if usePerSecondRedis {
			cache = redis.NewWindowedRateLimitCacheImpl(client, perSecondClient, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8)
		} else {
			cache = redis.NewWindowedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8)
		}
		statsStore := stats.NewStore(stats.NewNullSink(), false)
		timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
		var clientUsed *mock_redis.MockClient
		if usePerSecondRedis {
			clientUsed = perSecondClient
		} else {
			clientUsed = client
		}

		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key_value_0", int64(0)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(1)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key_value_0").SetArg(1, int64(0)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SET", "domain_key_value_0", int64(1e9+1e8)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(1)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
		limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Nanos: 1e8}}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key2_value2_subkey2_subvalue2_0", int64(0)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key2_value2_subkey2_subvalue2_0", int64(60)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key2_value2_subkey2_subvalue2_0").SetArg(1, int64(70e9)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		request = common.NewRateLimitRequest(
			"domain",
			[][][2]string{
				{{"key2", "value2"}},
				{{"key2", "value2"}, {"subkey2", "subvalue2"}},
			}, 1)
		limits = []*config.RateLimit{
			nil,
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key2_value2_subkey2_subvalue2", statsStore)}
		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: 69}}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(5)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key3_value3_0", int64(0)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key3_value3_0", int64(60*60)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key3_value3_0").SetArg(1, int64(60*60*1e9)).DoAndReturn(pipeAppend)

		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key3_value3_subkey3_subvalue3_0", int64(0)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key3_value3_subkey3_subvalue3_0", int64(60*60*24)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key3_value3_subkey3_subvalue3_0").SetArg(1, int64(60*60*24*1e9)).DoAndReturn(pipeAppend)

		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		request = common.NewRateLimitRequest(
			"domain",
			[][][2]string{
				{{"key3", "value3"}},
				{{"key3", "value3"}, {"subkey3", "subvalue3"}},
			}, 1)
		limits = []*config.RateLimit{
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, "key3_value3", statsStore),
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_DAY, "key3_value3_subkey3_subvalue3", statsStore)}
		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: (60 * 60) - 1}},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: (60 * 60 * 24) - 1}}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	}
}

func TestNearLimitWindowed(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_limiter.NewMockTimeSource(controller)
	cache := redis.NewWindowedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key4_value4", statsStore)}

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNanoNow().Return(int64(50e9)).MaxTimes(1)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key4_value4_0", int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key4_value4_0", int64(60)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key4_value4_0").SetArg(1, int64(50e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SET", "domain_key4_value4_0", int64(50e9+6e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key4_value4_0", int64(50+6-50+1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Seconds: 6}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNanoNow().Return(int64(50e9)).MaxTimes(1)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key4_value4_0", int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key4_value4_0", int64(60)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key4_value4_0").SetArg(1, int64(98e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SET", "domain_key4_value4_0", int64(98e9+6e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key4_value4_0", int64(98+6-50+1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: &duration.Duration{Seconds: 54}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. We went OVER_LIMIT, but the near_limit counter only increases
	// when we are near limit, not after we have passed the limit.
	timeSource.EXPECT().UnixNanoNow().Return(int64(50e9)).MaxTimes(1)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key4_value4_0", int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key4_value4_0", int64(60)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key4_value4_0").SetArg(1, int64(110e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: 110 - 50}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
}

func TestWindowedOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_limiter.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	cache := redis.NewWindowedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, localCache, 0.8)
	sink := &common.TestStatSink{}
	statsStore := stats.NewStore(sink, true)
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)}

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNanoNow().Return(int64(60 * 4 * 60 * 1e9)).MaxTimes(1)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key_value_0", int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(60*60)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key_value_0").SetArg(1, int64(71*4*60*1e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SET", "domain_key_value_0", int64(72*4*60*1e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(12*4*60+1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 3, DurationUntilReset: &duration.Duration{Seconds: 12 * 4 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 1, 1, 0, 0)

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNanoNow().Return(int64(60 * 4 * 60 * 1e9)).MaxTimes(1)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key_value_0", int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(60*60)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key_value_0").SetArg(1, int64(72*4*60*1e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SET", "domain_key_value_0", int64(73*4*60*1e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(13*4*60+1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)

	limits = []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: &duration.Duration{Seconds: 13 * 4 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 2, 0, 0)

	// Test Over limit stats
	timeSource.EXPECT().UnixNanoNow().Return(int64(60 * 4 * 60 * 1e9)).MaxTimes(1)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key_value_0", int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(60*60)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key_value_0").SetArg(1, int64(75*4*60*1e9)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)

	limits = []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: 15 * 4 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 3, 0, 1)

	// Test Over limit stats with local cache
	timeSource.EXPECT().UnixNanoNow().Return(int64(60 * 4 * 60 * 1e9)).MaxTimes(1)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)

	limits = []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: 15 * 4 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(4), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 1, 3, 4, 0, 1)
}

func TestRedisWindowedWithJitter(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_limiter.NewMockTimeSource(controller)
	jitterSource := mock_limiter.NewMockJitterRandSource(controller)
	cache := redis.NewWindowedRateLimitCacheImpl(client, nil, timeSource, rand.New(jitterSource), 3600, nil, 0.8)
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	jitterSource.EXPECT().Int63().Return(int64(100))
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SETNX", "domain_key_value_0", int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key_value_0").SetArg(1, int64(0)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "SET", "domain_key_value_0", int64(1e9+1e8)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_0", int64(101)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Nanos: 1e8}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
}
