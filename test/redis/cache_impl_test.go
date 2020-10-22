package redis_test

import (
	"testing"
	"time"

	"github.com/coocood/freecache"
	"github.com/mediocregopher/radix/v3"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	stats "github.com/lyft/gostats"

	"math/rand"

	"github.com/alicebob/miniredis/v2"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_limiter "github.com/envoyproxy/ratelimit/test/mocks/limiter"
	mock_redis "github.com/envoyproxy/ratelimit/test/mocks/redis"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestRedis(t *testing.T) {
	t.Run("WithoutPerSecondRedis", testRedis(false))
	t.Run("WithPerSecondRedis", testRedis(true))
}

func pipeAppend(pipeline redis.Pipeline, rcv interface{}, cmd, key string, args ...interface{}) redis.Pipeline {
	return append(pipeline, radix.FlatCmd(rcv, cmd, key, args...))
}

func testRedis(usePerSecondRedis bool) func(*testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)
		controller := gomock.NewController(t)
		defer controller.Finish()

		client := mock_redis.NewMockClient(controller)
		perSecondClient := mock_redis.NewMockClient(controller)
		timeSource := mock_limiter.NewMockTimeSource(controller)
		var cache limiter.RateLimitCache
		if usePerSecondRedis {
			cache = redis.NewRateLimitCacheImpl(client, perSecondClient, timeSource, rand.New(rand.NewSource(1)), 0, nil)
		} else {
			cache = redis.NewRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil)
		}
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
		var clientUsed *mock_redis.MockClient
		if usePerSecondRedis {
			clientUsed = perSecondClient
		} else {
			clientUsed = client
		}

		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key_value_1234", uint32(1)).SetArg(1, uint32(5)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_1234", int64(1)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
		limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key2_value2_subkey2_subvalue2_1200", uint32(1)).SetArg(1, uint32(11)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
			"EXPIRE", "domain_key2_value2_subkey2_subvalue2_1200", int64(60)).DoAndReturn(pipeAppend)
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
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[1].Limit, timeSource)}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key3_value3_997200", uint32(1)).SetArg(1, uint32(11)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
			"EXPIRE", "domain_key3_value3_997200", int64(3600)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key3_value3_subkey3_subvalue3_950400", uint32(1)).SetArg(1, uint32(13)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
			"EXPIRE", "domain_key3_value3_subkey3_subvalue3_950400", int64(86400)).DoAndReturn(pipeAppend)
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
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[1].Limit, timeSource)}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	}
}

func testLocalCacheStats(localCacheStats stats.StatGenerator, statsStore stats.Store, sink *common.TestStatSink,
	expectedHitCount int, expectedMissCount int, expectedLookUpCount int, expectedExpiredCount int,
	expectedEntryCount int) func(*testing.T) {
	return func(t *testing.T) {
		localCacheStats.GenerateStats()
		statsStore.Flush()

		// Check whether all local_cache related stats are available.
		_, ok := sink.Record["averageAccessTime"]
		assert.Equal(t, true, ok)
		hitCount, ok := sink.Record["hitCount"]
		assert.Equal(t, true, ok)
		missCount, ok := sink.Record["missCount"]
		assert.Equal(t, true, ok)
		lookupCount, ok := sink.Record["lookupCount"]
		assert.Equal(t, true, ok)
		_, ok = sink.Record["overwriteCount"]
		assert.Equal(t, true, ok)
		_, ok = sink.Record["evacuateCount"]
		assert.Equal(t, true, ok)
		expiredCount, ok := sink.Record["expiredCount"]
		assert.Equal(t, true, ok)
		entryCount, ok := sink.Record["entryCount"]
		assert.Equal(t, true, ok)

		// Check the correctness of hitCount, missCount, lookupCount, expiredCount and entryCount
		assert.Equal(t, expectedHitCount, hitCount.(int))
		assert.Equal(t, expectedMissCount, missCount.(int))
		assert.Equal(t, expectedLookUpCount, lookupCount.(int))
		assert.Equal(t, expectedExpiredCount, expiredCount.(int))
		assert.Equal(t, expectedEntryCount, entryCount.(int))

		sink.Clear()
	}
}

func TestOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_limiter.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	cache := redis.NewRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, localCache)
	sink := &common.TestStatSink{}
	statsStore := stats.NewStore(sink, true)
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key4_value4", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 1, 1, 0, 0)

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 2, 0, 0)

	// Test Over limit stats
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(16)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 3, 0, 1)

	// Test Over limit stats with local cache
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).Times(0)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).Times(0)
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(4), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 1, 3, 4, 0, 1)
}

func TestNearLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_limiter.NewMockTimeSource(controller)
	cache := redis.NewRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil)
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key4_value4", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. We went OVER_LIMIT, but the near_limit counter only increases
	// when we are near limit, not after we have passed the limit.
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(16)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Now test hitsAddend that is greater than 1
	// All of it under limit, under near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key5_value5_1234", uint32(3)).SetArg(1, uint32(5)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key5_value5_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key5", "value5"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key5_value5", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 15, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// All of it under limit, some over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key6_value6_1234", uint32(2)).SetArg(1, uint32(7)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key6_value6_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key6", "value6"}}}, 2)
	limits = []*config.RateLimit{config.NewRateLimit(8, pb.RateLimitResponse_RateLimit_SECOND, "key6_value6", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// All of it under limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key7_value7_1234", uint32(3)).SetArg(1, uint32(19)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key7_value7_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key7", "value7"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key7_value7", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.NearLimit.Value())

	// Some of it over limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key8_value8_1234", uint32(3)).SetArg(1, uint32(22)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key8_value8_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key8", "value8"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key8_value8", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Some of it in all three places
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key9_value9_1234", uint32(7)).SetArg(1, uint32(22)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key9_value9_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key9", "value9"}}}, 7)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key9_value9", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(7), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(4), limits[0].Stats.NearLimit.Value())

	// all of it over limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key10_value10_1234", uint32(3)).SetArg(1, uint32(30)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key10_value10_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key10", "value10"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key10_value10", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(3), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
}

func TestRedisWithJitter(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_limiter.NewMockTimeSource(controller)
	jitterSource := mock_limiter.NewMockJitterRandSource(controller)
	cache := redis.NewRateLimitCacheImpl(client, nil, timeSource, rand.New(jitterSource), 3600, nil)
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	jitterSource.EXPECT().Int63().Return(int64(100))
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key_value_1234", uint32(1)).SetArg(1, uint32(5)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_1234", int64(101)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: redis.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
}

func mustNewRedisServer() *miniredis.Miniredis {
	srv, err := miniredis.Run()
	if err != nil {
		panic(err)
	}

	return srv
}

func expectPanicError(t *testing.T, f assert.PanicTestFunc) (result error) {
	t.Helper()
	defer func() {
		panicResult := recover()
		assert.NotNil(t, panicResult, "Expected a panic")
		result = panicResult.(error)
	}()
	f()
	return
}

func testNewClientImpl(t *testing.T, pipelineWindow time.Duration, pipelineLimit int) func(t *testing.T) {
	return func(t *testing.T) {
		redisAuth := "123"
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(auth, addr string) redis.Client {
			return redis.NewClientImpl(statsStore, false, auth, "single", addr, 1, pipelineWindow, pipelineLimit)
		}

		t.Run("connection refused", func(t *testing.T) {
			// It's possible there is a redis server listening on 6379 in ci environment, so
			// use a random port.
			panicErr := expectPanicError(t, func() { mkRedisClient("", "localhost:12345") })
			assert.Contains(t, panicErr.Error(), "connection refused")
		})

		t.Run("ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			var client redis.Client
			assert.NotPanics(t, func() {
				client = mkRedisClient("", redisSrv.Addr())
			})
			assert.NotNil(t, client)
		})

		t.Run("auth fail", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			redisSrv.RequireAuth(redisAuth)

			assert.PanicsWithError(t, "NOAUTH Authentication required.", func() {
				mkRedisClient("", redisSrv.Addr())
			})
		})

		t.Run("auth pass", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			redisSrv.RequireAuth(redisAuth)

			assert.NotPanics(t, func() {
				mkRedisClient(redisAuth, redisSrv.Addr())
			})
		})

		t.Run("ImplicitPipeliningEnabled() return expected value", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient("", redisSrv.Addr())

			if pipelineWindow == 0 && pipelineLimit == 0 {
				assert.False(t, client.ImplicitPipeliningEnabled())
			} else {
				assert.True(t, client.ImplicitPipeliningEnabled())
			}
		})
	}
}

func TestNewClientImpl(t *testing.T) {
	t.Run("ImplicitPipeliningEnabled", testNewClientImpl(t, 2*time.Millisecond, 2))
	t.Run("ImplicitPipeliningDisabled", testNewClientImpl(t, 0, 0))
}

func TestDoCmd(t *testing.T) {
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	mkRedisClient := func(addr string) redis.Client {
		return redis.NewClientImpl(statsStore, false, "", "single", addr, 1, 0, 0)
	}

	t.Run("SETGET ok", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		client := mkRedisClient(redisSrv.Addr())
		var res string

		assert.Nil(t, client.DoCmd(nil, "SET", "foo", "bar"))
		assert.Nil(t, client.DoCmd(&res, "GET", "foo"))
		assert.Equal(t, "bar", res)
	})

	t.Run("INCRBY ok", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		defer redisSrv.Close()

		client := mkRedisClient(redisSrv.Addr())
		var res uint32
		hits := uint32(1)

		assert.Nil(t, client.DoCmd(&res, "INCRBY", "a", hits))
		assert.Equal(t, hits, res)
		assert.Nil(t, client.DoCmd(&res, "INCRBY", "a", hits))
		assert.Equal(t, uint32(2), res)
	})

	t.Run("connection broken", func(t *testing.T) {
		redisSrv := mustNewRedisServer()
		client := mkRedisClient(redisSrv.Addr())

		assert.Nil(t, client.DoCmd(nil, "SET", "foo", "bar"))

		redisSrv.Close()
		assert.EqualError(t, client.DoCmd(nil, "GET", "foo"), "EOF")
	})
}

func testPipeDo(t *testing.T, pipelineWindow time.Duration, pipelineLimit int) func(t *testing.T) {
	return func(t *testing.T) {
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		mkRedisClient := func(addr string) redis.Client {
			return redis.NewClientImpl(statsStore, false, "", "single", addr, 1, pipelineWindow, pipelineLimit)
		}

		t.Run("SETGET ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient(redisSrv.Addr())
			var res string

			pipeline := redis.Pipeline{}
			pipeline = client.PipeAppend(pipeline, nil, "SET", "foo", "bar")
			pipeline = client.PipeAppend(pipeline, &res, "GET", "foo")

			assert.Nil(t, client.PipeDo(pipeline))
			assert.Equal(t, "bar", res)
		})

		t.Run("INCRBY ok", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			defer redisSrv.Close()

			client := mkRedisClient(redisSrv.Addr())
			var res uint32
			hits := uint32(1)

			assert.Nil(t, client.PipeDo(client.PipeAppend(redis.Pipeline{}, &res, "INCRBY", "a", hits)))
			assert.Equal(t, hits, res)

			assert.Nil(t, client.PipeDo(client.PipeAppend(redis.Pipeline{}, &res, "INCRBY", "a", hits)))
			assert.Equal(t, uint32(2), res)
		})

		t.Run("connection broken", func(t *testing.T) {
			redisSrv := mustNewRedisServer()
			client := mkRedisClient(redisSrv.Addr())

			assert.Nil(t, nil, client.PipeDo(client.PipeAppend(redis.Pipeline{}, nil, "SET", "foo", "bar")))

			redisSrv.Close()

			expectErrContainEOF := func(t *testing.T, err error) {
				assert.NotNil(t, err)
				assert.Contains(t, err.Error(), "EOF")
			}

			expectErrContainEOF(t, client.PipeDo(client.PipeAppend(redis.Pipeline{}, nil, "GET", "foo")))
		})
	}
}

func TestPipeDo(t *testing.T) {
	t.Run("ImplicitPipeliningEnabled", testPipeDo(t, 10*time.Millisecond, 2))
	t.Run("ImplicitPipeliningDisabled", testPipeDo(t, 0, 0))
}
