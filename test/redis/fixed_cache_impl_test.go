package redis_test

import (
	"context"
	"math/rand"
	"testing"

	"github.com/envoyproxy/ratelimit/test/mocks/stats"

	"github.com/coocood/freecache"
	"github.com/mediocregopher/radix/v3"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	gostats "github.com/lyft/gostats"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/trace"
	"github.com/envoyproxy/ratelimit/src/utils"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/test/common"
	mock_redis "github.com/envoyproxy/ratelimit/test/mocks/redis"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
)

var testSpanExporter = trace.GetTestSpanExporter()

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
		statsStore := gostats.NewStore(gostats.NewNullSink(), false)
		sm := stats.NewMockStatManager(statsStore)

		client := mock_redis.NewMockClient(controller)
		perSecondClient := mock_redis.NewMockClient(controller)
		timeSource := mock_utils.NewMockTimeSource(controller)
		var cache limiter.RateLimitCache
		if usePerSecondRedis {
			cache = redis.NewFixedRateLimitCacheImpl(client, perSecondClient, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm, false)
		} else {
			cache = redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm, false)
		}

		timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
		var clientUsed *mock_redis.MockClient
		if usePerSecondRedis {
			clientUsed = perSecondClient
		} else {
			clientUsed = client
		}

		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key_value_1234", uint64(1)).SetArg(1, uint64(5)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_1234", int64(1)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
		limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"), false, false, "", nil, false)}

		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
			cache.DoLimit(context.Background(), request, limits))
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
		assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key2_value2_subkey2_subvalue2_1200", uint64(1)).SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
			"EXPIRE", "domain_key2_value2_subkey2_subvalue2_1200", int64(60)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		request = common.NewRateLimitRequestWithPerDescriptorHitsAddend(
			"domain",
			[][][2]string{
				{{"key2", "value2"}},
				{{"key2", "value2"}, {"subkey2", "subvalue2"}},
			}, []uint64{0, 1})
		limits = []*config.RateLimit{
			nil,
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key2_value2_subkey2_subvalue2"), false, false, "", nil, false),
		}
		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
			},
			cache.DoLimit(context.Background(), request, limits))
		assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.WithinLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key3_value3_997200", uint64(0)).SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
			"EXPIRE", "domain_key3_value3_997200", int64(3600)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key3_value3_subkey3_subvalue3_950400", uint64(1)).SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
			"EXPIRE", "domain_key3_value3_subkey3_subvalue3_950400", int64(86400)).DoAndReturn(pipeAppend)
		clientUsed.EXPECT().PipeDo(gomock.Any()).Return(nil)

		request = common.NewRateLimitRequestWithPerDescriptorHitsAddend(
			"domain",
			[][][2]string{
				{{"key3", "value3"}},
				{{"key3", "value3"}, {"subkey3", "subvalue3"}},
			}, []uint64{0, 1})
		limits = []*config.RateLimit{
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key3_value3"), false, false, "", nil, false),
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_DAY, sm.NewStats("key3_value3_subkey3_subvalue3"), false, false, "", nil, false),
		}
		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
			},
			cache.DoLimit(context.Background(), request, limits))
		assert.Equal(uint64(0), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())
		assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.WithinLimit.Value())
	}
}

func testLocalCacheStats(localCacheScopeName string, localCacheStats gostats.StatGenerator, statsStore gostats.Store, sink *common.TestStatSink,
	expectedHitCount uint64, expectedMissCount uint64, expectedLookUpCount uint64, expectedExpiredCount uint64,
	expectedEntryCount uint64,
) func(*testing.T) {
	return func(t *testing.T) {
		localCacheStats.GenerateStats()
		statsStore.Flush()

		prefix := localCacheScopeName + "."
		// Check whether all local_cache related stats are available.
		_, ok := sink.Record[prefix+"averageAccessTime"]
		assert.Equal(t, true, ok)
		hitCount, ok := sink.Record[prefix+"hitCount"]
		assert.Equal(t, true, ok)
		missCount, ok := sink.Record[prefix+"missCount"]
		assert.Equal(t, true, ok)
		lookupCount, ok := sink.Record[prefix+"lookupCount"]
		assert.Equal(t, true, ok)
		_, ok = sink.Record[prefix+"overwriteCount"]
		assert.Equal(t, true, ok)
		_, ok = sink.Record[prefix+"evacuateCount"]
		assert.Equal(t, true, ok)
		expiredCount, ok := sink.Record[prefix+"expiredCount"]
		assert.Equal(t, true, ok)
		entryCount, ok := sink.Record[prefix+"entryCount"]
		assert.Equal(t, true, ok)

		// Check the correctness of hitCount, missCount, lookupCount, expiredCount and entryCount
		assert.Equal(t, expectedHitCount, hitCount.(uint64))
		assert.Equal(t, expectedMissCount, missCount.(uint64))
		assert.Equal(t, expectedLookUpCount, lookupCount.(uint64))
		assert.Equal(t, expectedExpiredCount, expiredCount.(uint64))
		assert.Equal(t, expectedEntryCount, entryCount.(uint64))

		sink.Clear()
	}
}

func TestOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	sink := common.NewTestStatSink()
	statsStore := gostats.NewStore(sink, false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, localCache, 0.8, "", sm, false)

	localCacheScopeName := "localcache"
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope(localCacheScopeName))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key4_value4"), false, false, "", nil, false),
	}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 1, 1, 0, 0))

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats_1", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 2, 2, 0, 0))

	// Test Over limit stats
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(16)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats_2", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 3, 3, 0, 1))

	// Test Over limit stats with local cache
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).Times(0)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).Times(0)
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(4), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats_3", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 1, 3, 4, 0, 1))
}

func TestNearLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm, false)

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key4_value4"), false, false, "", nil, false),
	}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Test Near Limit Stats. We went OVER_LIMIT, but the near_limit counter only increases
	// when we are near limit, not after we have passed the limit.
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(16)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Now test hitsAddend that is greater than 1
	// All of it under limit, under near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key5_value5_1234", uint64(3)).SetArg(1, uint64(5)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key5_value5_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key5", "value5"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key5_value5"), false, false, "", nil, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 15, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())

	// All of it under limit, some over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key6_value6_1234", uint64(2)).SetArg(1, uint64(7)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key6_value6_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key6", "value6"}}}, 2)
	limits = []*config.RateLimit{config.NewRateLimit(8, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key6_value6"), false, false, "", nil, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// All of it under limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key7_value7_1234", uint64(3)).SetArg(1, uint64(19)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key7_value7_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key7", "value7"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key7_value7"), false, false, "", nil, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())

	// Some of it over limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key8_value8_1234", uint64(3)).SetArg(1, uint64(22)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key8_value8_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key8", "value8"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key8_value8"), false, false, "", nil, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())

	// Some of it in all three places
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key9_value9_1234", uint64(7)).SetArg(1, uint64(22)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key9_value9_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key9", "value9"}}}, 7)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key9_value9"), false, false, "", nil, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(7), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(4), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())

	// all of it over limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key10_value10_1234", uint64(3)).SetArg(1, uint64(30)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key10_value10_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key10", "value10"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key10_value10"), false, false, "", nil, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(3), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())
}

func TestRedisWithJitter(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	jitterSource := mock_utils.NewMockJitterRandSource(controller)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(jitterSource), 3600, nil, 0.8, "", sm, false)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	jitterSource.EXPECT().Int63().Return(int64(100))
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key_value_1234", uint64(1)).SetArg(1, uint64(5)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_1234", int64(101)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"), false, false, "", nil, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())
}

func TestOverLimitWithLocalCacheShadowRule(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	sink := common.NewTestStatSink()
	statsStore := gostats.NewStore(sink, false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, localCache, 0.8, "", sm, false)

	localCacheScopeName := "localcache"
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope(localCacheScopeName))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key4_value4"), false, true, "", nil, false),
	}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 1, 1, 0, 0))

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats_1", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 2, 2, 0, 0))

	// Test Over limit stats
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(16)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	// The result should be OK since limit is in ShadowMode
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.ShadowMode.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats_2", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 3, 3, 0, 1))

	// Test Over limit stats with local cache
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).Times(0)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).Times(0)

	// The result should be OK since limit is in ShadowMode
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))

	// Even if you hit the local cache, other metrics should increase normally.
	assert.Equal(uint64(4), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(2), limits[0].Stats.ShadowMode.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	t.Run("TestLocalCacheStats_3", testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 1, 3, 4, 0, 1))
}

func TestRedisTracer(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	testSpanExporter.Reset()

	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)

	client := mock_redis.NewMockClient(controller)

	timeSource := mock_utils.NewMockTimeSource(controller)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm, false)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key_value_1234", uint64(1)).SetArg(1, uint64(5)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "EXPIRE", "domain_key_value_1234", int64(1)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"), false, false, "", nil, false)}
	cache.DoLimit(context.Background(), request, limits)

	spanStubs := testSpanExporter.GetSpans()
	assert.NotNil(spanStubs)
	assert.Len(spanStubs, 1)
	assert.Equal(spanStubs[0].Name, "Redis Pipeline Execution")
}

func TestOverLimitWithStopCacheKeyIncrementWhenOverlimitConfig(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, localCache, 0.8, "", sm, true)
	sink := &common.TestStatSink{}

	localCacheScopeName := "localcache"
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope(localCacheScopeName))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key4_value4_997200").SetArg(1, uint64(10)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key5_value5_997200").SetArg(1, uint64(10)).DoAndReturn(pipeAppend)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(1)).SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key5_value5_997200", uint64(1)).SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key5_value5_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil).Times(2)

	request := common.NewRateLimitRequestWithPerDescriptorHitsAddend("domain", [][][2]string{{{"key4", "value4"}}, {{"key5", "value5"}}}, []uint64{1, 1})

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key4_value4"), false, false, "", nil, false),
		config.NewRateLimit(14, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key5_value5"), false, false, "", nil, false),
	}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 3, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())
	assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[1].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[1].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 1, 1, 0, 0)

	// Test Near Limit Stats. Some hits at Near Limit Ratio, but still OK.
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key4_value4_997200").SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key5_value5_997200").SetArg(1, uint64(11)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(2)).SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key5_value5_997200", uint64(2)).SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key5_value5_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil).Times(2)

	request = common.NewRateLimitRequestWithPerDescriptorHitsAddend("domain", [][][2]string{{{"key4", "value4"}}, {{"key5", "value5"}}}, []uint64{2, 2})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 1, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())
	assert.Equal(uint64(3), limits[1].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[1].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(2), limits[1].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[1].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 2, 2, 0, 0)

	// Test one key is reaching to the Overlimit threshold
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key4_value4_997200").SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "GET", "domain_key5_value5_997200").SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint64(0)).SetArg(1, uint64(13)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key5_value5_997200", uint64(2)).SetArg(1, uint64(15)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key5_value5_997200", int64(3600)).DoAndReturn(pipeAppend)
	client.EXPECT().PipeDo(gomock.Any()).Return(nil).Times(2)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	assert.Equal(uint64(5), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(2), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(5), limits[0].Stats.WithinLimit.Value())
	assert.Equal(uint64(5), limits[1].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(3), limits[1].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[1].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheScopeName, localCacheStats, statsStore, sink, 0, 2, 3, 0, 1)
}
