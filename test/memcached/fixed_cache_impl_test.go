package memcached_test

import (
	"math/rand"
	"testing"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/memcached"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/envoyproxy/ratelimit/test/common"
	"github.com/envoyproxy/ratelimit/test/mocks/stats"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	mock_strategy "github.com/envoyproxy/ratelimit/test/mocks/storage/strategy"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
	gostats "github.com/lyft/gostats"
)

func TestMemcached(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_strategy.NewMockStorageStrategy(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := memcached.NewFixedRateLimitCacheImpl(client, timeSource, rand.New(rand.NewSource(1)), nil, 0, 0.8, "", sm)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key_value_1234").Return(uint64(4), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key_value_1234", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key_value_1234", uint64(1)).MaxTimes(1)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key2_value2_subkey2_subvalue2_1200").Return(uint64(10), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key2_value2_subkey2_subvalue2_1200", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key2_value2_subkey2_subvalue2_1200", uint64(60)).MaxTimes(1)

	request = common.NewRateLimitRequest(
		"domain",
		[][][2]string{
			{{"key2", "value2"}},
			{{"key2", "value2"}, {"subkey2", "subvalue2"}},
		}, 1)
	limits = []*config.RateLimit{
		nil,
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key2_value2_subkey2_subvalue2"))}
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[1].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.WithinLimit.Value())

	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)
	client.EXPECT().GetValue("domain_key3_value3_997200").Return(uint64(10), nil).MaxTimes(1)
	client.EXPECT().GetValue("domain_key3_value3_subkey3_subvalue3_950400").Return(uint64(12), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key3_value3_997200", uint64(1)).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key3_value3_subkey3_subvalue3_950400", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key3_value3_997200", uint64(3600)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key3_value3_subkey3_subvalue3_950400", uint64(86400)).MaxTimes(1)

	request = common.NewRateLimitRequest(
		"domain",
		[][][2]string{
			{{"key3", "value3"}},
			{{"key3", "value3"}, {"subkey3", "subvalue3"}},
		}, 1)
	limits = []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key3_value3")),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_DAY, sm.NewStats("key3_value3_subkey3_subvalue3"))}
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

	client := mock_strategy.NewMockStorageStrategy(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := memcached.NewFixedRateLimitCacheImpl(client, timeSource, rand.New(rand.NewSource(1)), localCache, 0, 0.8, "", sm)
	sink := &common.TestStatSink{}
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key4_value4_997200").Return(uint64(10), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key4_value4_997200", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key4_value4_997200", uint64(3600)).MaxTimes(1)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key4_value4"))}

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

func TestNearLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_strategy.NewMockStorageStrategy(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := memcached.NewFixedRateLimitCacheImpl(client, timeSource, rand.New(rand.NewSource(1)), nil, 0, 0.8, "", sm)

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key4_value4_997200").Return(uint64(10), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key4_value4_997200", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key4_value4_997200", uint64(3600)).MaxTimes(1)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key4_value4"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

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
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Test Near Limit Stats. We went OVER_LIMIT, but the near_limit counter only increases
	// when we are near limit, not after we have passed the limit.
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
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// Now test hitsAddend that is greater than 1
	// All of it under limit, under near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key5_value5_1234").Return(uint64(2), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key5_value5_1234", uint64(3)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key5_value5_1234", uint64(1)).MaxTimes(1)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key5", "value5"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key5_value5"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 15, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())

	// All of it under limit, some over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key6_value6_1234").Return(uint64(5), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key6_value6_1234", uint64(2)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key6_value6_1234", uint64(1)).MaxTimes(1)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key6", "value6"}}}, 2)
	limits = []*config.RateLimit{config.NewRateLimit(8, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key6_value6"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// All of it under limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key7_value7_1234").Return(uint64(16), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key7_value7_1234", uint64(3)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key7_value7_1234", uint64(1)).MaxTimes(1)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key7", "value7"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key7_value7"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())

	// Some of it over limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key8_value8_1234").Return(uint64(19), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key8_value8_1234", uint64(3)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key8_value8_1234", uint64(1)).MaxTimes(1)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key8", "value8"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key8_value8"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())

	// Some of it in all three places
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key9_value9_1234").Return(uint64(15), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key9_value9_1234", uint64(7)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key9_value9_1234", uint64(1)).MaxTimes(1)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key9", "value9"}}}, 7)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key9_value9"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(7), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(4), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())

	// all of it over limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetValue("domain_key10_value10_1234").Return(uint64(27), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key10_value10_1234", uint64(3)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key10_value10_1234", uint64(1)).MaxTimes(1)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key10", "value10"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key10_value10"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(3), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())
}

func TestMemcachedWithJitter(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	client := mock_strategy.NewMockStorageStrategy(controller)
	timeSource := mock_utils.NewMockTimeSource(controller)
	jitterSource := mock_utils.NewMockJitterRandSource(controller)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	cache := memcached.NewFixedRateLimitCacheImpl(client, timeSource, rand.New(jitterSource), nil, 3600, 0.8, "", sm)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	jitterSource.EXPECT().Int63().Return(int64(100)).MaxTimes(1)
	client.EXPECT().GetValue("domain_key_value_1234").Return(uint64(4), nil).MaxTimes(1)
	client.EXPECT().IncrementValue("domain_key_value_1234", uint64(1)).MaxTimes(1)
	client.EXPECT().SetExpire("domain_key_value_1234", uint64(101)).MaxTimes(1)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"))}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())
}
