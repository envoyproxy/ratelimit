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
			cache = redis.NewFixedRateLimitCacheImpl(client, perSecondClient, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm)
		} else {
			cache = redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm)
		}

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
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key3_value3"), false, false, "", nil, false),
			config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_DAY, sm.NewStats("key3_value3_subkey3_subvalue3"), false, false, "", nil, false),
		}
		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
			},
			cache.DoLimit(context.Background(), request, limits))
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())
	}
}

func testLocalCacheStats(localCacheStats gostats.StatGenerator, statsStore gostats.Store, sink *common.TestStatSink,
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
	timeSource := mock_utils.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, localCache, 0.8, "", sm)
	sink := &common.TestStatSink{}
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(11)).DoAndReturn(pipeAppend)
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
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 1, 1, 0, 0)

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(13)).DoAndReturn(pipeAppend)
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
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 2, 0, 0)

	// Test Over limit stats
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(16)).DoAndReturn(pipeAppend)
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
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 3, 0, 1)

	// Test Over limit stats with local cache
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).Times(0)
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
	testLocalCacheStats(localCacheStats, statsStore, sink, 1, 3, 4, 0, 1)
}

func TestNearLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_redis.NewMockClient(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm)

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(11)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(13)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(16)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key5_value5_1234", uint32(3)).SetArg(1, uint32(5)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key6_value6_1234", uint32(2)).SetArg(1, uint32(7)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key7_value7_1234", uint32(3)).SetArg(1, uint32(19)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key8_value8_1234", uint32(3)).SetArg(1, uint32(22)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key9_value9_1234", uint32(7)).SetArg(1, uint32(22)).DoAndReturn(pipeAppend)
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
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key10_value10_1234", uint32(3)).SetArg(1, uint32(30)).DoAndReturn(pipeAppend)
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
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(jitterSource), 3600, nil, 0.8, "", sm)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	jitterSource.EXPECT().Int63().Return(int64(100))
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key_value_1234", uint32(1)).SetArg(1, uint32(5)).DoAndReturn(pipeAppend)
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
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, localCache, 0.8, "", sm)
	sink := &common.TestStatSink{}
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(11)).DoAndReturn(pipeAppend)
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
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 1, 1, 0, 0)

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(13)).DoAndReturn(pipeAppend)
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
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 2, 0, 0)

	// Test Over limit stats
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).SetArg(1, uint32(16)).DoAndReturn(pipeAppend)
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
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 3, 0, 1)

	// Test Over limit stats with local cache
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key4_value4_997200", uint32(1)).Times(0)
	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(),
		"EXPIRE", "domain_key4_value4_997200", int64(3600)).Times(0)

	// The result should be OK since limit is in ShadowMode
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 15, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(context.Background(), request, limits))
	// TODO: How should we handle statistics? Should there be a separate ShadowMode statistics?  Should the other Stats remain as if they were unaffected by shadowmode?
	assert.Equal(uint64(4), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 1, 3, 4, 0, 1)
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
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), 0, nil, 0.8, "", sm)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)

	client.EXPECT().PipeAppend(gomock.Any(), gomock.Any(), "INCRBY", "domain_key_value_1234", uint32(1)).SetArg(1, uint32(5)).DoAndReturn(pipeAppend)
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
