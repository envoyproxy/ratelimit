package redis_test

import (
	"math/rand"
	"testing"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/envoyproxy/ratelimit/test/common"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	mock_strategy "github.com/envoyproxy/ratelimit/test/mocks/storage/strategy"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
	stats "github.com/lyft/gostats"
)

func TestRedis(t *testing.T) {
	t.Run("WithoutPerSecondRedis", testRedis(false))
	t.Run("WithPerSecondRedis", testRedis(true))
}

func testRedis(usePerSecondRedis bool) func(*testing.T) {
	return func(t *testing.T) {
		assert := assert.New(t)
		controller := gomock.NewController(t)
		defer controller.Finish()

		client := mock_strategy.NewMockStorageStrategy(controller)
		perSecondClient := mock_strategy.NewMockStorageStrategy(controller)
		timeSource := mock_utils.NewMockTimeSource(controller)

		var cache limiter.RateLimitCache
		if usePerSecondRedis {
			cache = redis.NewFixedRateLimitCacheImpl(client, perSecondClient, timeSource, rand.New(rand.NewSource(1)), nil, 0, 0.8, "")
		} else {
			cache = redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), nil, 0, 0.8, "")
		}
		statsStore := stats.NewStore(stats.NewNullSink(), false)

		var clientUsed *mock_strategy.MockStorageStrategy
		if usePerSecondRedis {
			clientUsed = perSecondClient
		} else {
			clientUsed = client
		}
		timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
		clientUsed.EXPECT().GetValue("domain_key_value_1234").Return(uint64(4), nil).MaxTimes(1)
		clientUsed.EXPECT().IncrementValue("domain_key_value_1234", uint64(1)).MaxTimes(1)
		clientUsed.EXPECT().SetExpire("domain_key_value_1234", uint64(1)).MaxTimes(1)

		request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
		limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

		assert.Equal(
			[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
		assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
		assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
		clientUsed.EXPECT().GetValue("domain_key2_value2_subkey2_subvalue2_1200").Return(uint64(10), nil).MaxTimes(1)
		clientUsed.EXPECT().IncrementValue("domain_key2_value2_subkey2_subvalue2_1200", uint64(1)).MaxTimes(1)
		clientUsed.EXPECT().SetExpire("domain_key2_value2_subkey2_subvalue2_1200", uint64(60)).MaxTimes(1)

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
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[1].Limit, timeSource)}},
			cache.DoLimit(nil, request, limits))
		assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
		assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())
		assert.Equal(uint64(0), limits[1].Stats.WithinLimit.Value())

		clientUsed = client
		timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)

		clientUsed.EXPECT().GetValue("domain_key3_value3_997200").Return(uint64(10), nil).MaxTimes(1)
		clientUsed.EXPECT().GetValue("domain_key3_value3_subkey3_subvalue3_950400").Return(uint64(12), nil).MaxTimes(1)
		clientUsed.EXPECT().IncrementValue("domain_key3_value3_997200", uint64(1)).MaxTimes(1)
		clientUsed.EXPECT().IncrementValue("domain_key3_value3_subkey3_subvalue3_950400", uint64(1)).MaxTimes(1)
		clientUsed.EXPECT().SetExpire("domain_key3_value3_997200", uint64(3600)).MaxTimes(1)
		clientUsed.EXPECT().SetExpire("domain_key3_value3_subkey3_subvalue3_950400", uint64(86400)).MaxTimes(1)

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
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)},
				{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[1].Limit, timeSource)}},
			cache.DoLimit(nil, request, limits))
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

	client := mock_strategy.NewMockStorageStrategy(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(rand.NewSource(1)), localCache, 0, 0.8, "")
	sink := &common.TestStatSink{}
	statsStore := stats.NewStore(sink, true)
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key4_value4_997200").Return(uint64(10), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key4_value4_997200", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key4_value4_997200", uint64(3600)).MaxTimes(1)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key4_value4", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 1, 1, 0, 0)

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key4_value4_997200").Return(uint64(12), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key4_value4_997200", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key4_value4_997200", uint64(3600)).MaxTimes(1)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 2, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 2, 0, 0)

	// Test Over limit stats
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key4_value4_997200").Return(uint64(15), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key4_value4_997200", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key4_value4_997200", uint64(3600)).MaxTimes(1)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 3, 0, 1)

	// Test Over limit stats with local cache
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().IncrementValue("domain_key4_value4_997200", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key4_value4_997200", uint64(3600)).MaxTimes(1)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(4), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 1, 3, 4, 0, 1)
}

func TestRedisWithJitter(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_strategy.NewMockStorageStrategy(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	jitterSource := mock_utils.NewMockJitterRandSource(controller)
	cache := redis.NewFixedRateLimitCacheImpl(client, nil, timeSource, rand.New(jitterSource), nil, 3600, 0.8, "")
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	jitterSource.EXPECT().Int63().Return(int64(100))
	client.EXPECT().GetValue("domain_key_value_1234").Return(uint64(4), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key_value_1234", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key_value_1234", uint64(101)).MaxTimes(1)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())
}
