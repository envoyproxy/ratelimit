// Adapted from test/redis/cache_impl_test.go, with most test cases being the same
// basic idea. TestMemcacheAdd() is unique to the memcache tests, since redis can create a new key
// simply by incrementing it but memcached cannot. In memcache new keys need to be explicitly
// added.
package memcached_test

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/coocood/freecache"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/memcached"
	"github.com/envoyproxy/ratelimit/src/utils"
	stats "github.com/lyft/gostats"

	"github.com/envoyproxy/ratelimit/test/common"
	mock_memcached "github.com/envoyproxy/ratelimit/test/mocks/memcached"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestMemcached(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewRateLimitCacheImpl(client, timeSource, nil, 0, nil, statsStore, 0.8, "")

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key_value_1234"}).Return(
		getMultiResult(map[string]int{"domain_key_value_1234": 4}), nil,
	)
	client.EXPECT().Increment("domain_key_value_1234", uint64(1)).Return(uint64(5), nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 5, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key2_value2_subkey2_subvalue2_1200"}).Return(
		getMultiResult(map[string]int{"domain_key2_value2_subkey2_subvalue2_1200": 10}), nil,
	)
	client.EXPECT().Increment("domain_key2_value2_subkey2_subvalue2_1200", uint64(1)).Return(uint64(11), nil)

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

	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)
	client.EXPECT().GetMulti([]string{
		"domain_key3_value3_997200",
		"domain_key3_value3_subkey3_subvalue3_950400",
	}).Return(
		getMultiResult(map[string]int{
			"domain_key3_value3_997200":                   10,
			"domain_key3_value3_subkey3_subvalue3_950400": 12}),
		nil,
	)
	client.EXPECT().Increment("domain_key3_value3_997200", uint64(1)).Return(uint64(11), nil)
	client.EXPECT().Increment("domain_key3_value3_subkey3_subvalue3_950400", uint64(1)).Return(uint64(13), nil)

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

	cache.Flush()
}

func TestMemcachedGetError(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewRateLimitCacheImpl(client, timeSource, nil, 0, nil, statsStore, 0.8, "")

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key_value_1234"}).Return(
		nil, memcache.ErrNoServers,
	)
	client.EXPECT().Increment("domain_key_value_1234", uint64(1)).Return(uint64(5), nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	// No error, but the key is missing
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key_value1_1234"}).Return(
		nil, nil,
	)
	client.EXPECT().Increment("domain_key_value1_1234", uint64(1)).Return(uint64(5), nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value1"}}}, 1)
	limits = []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value1", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	cache.Flush()
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

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	localCache := freecache.NewCache(100)
	sink := &common.TestStatSink{}
	statsStore := stats.NewStore(sink, true)
	cache := memcached.NewRateLimitCacheImpl(client, timeSource, nil, 0, localCache, statsStore, 0.8, "")
	localCacheStats := limiter.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key4_value4_997200"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_997200": 10}), nil,
	)
	client.EXPECT().Increment("domain_key4_value4_997200", uint64(1)).Return(uint64(5), nil)

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
	client.EXPECT().GetMulti([]string{"domain_key4_value4_997200"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_997200": 12}), nil,
	)
	client.EXPECT().Increment("domain_key4_value4_997200", uint64(1)).Return(uint64(13), nil)

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
	client.EXPECT().GetMulti([]string{"domain_key4_value4_997200"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_997200": 15}), nil,
	)
	client.EXPECT().Increment("domain_key4_value4_997200", uint64(1)).Return(uint64(16), nil)

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
	client.EXPECT().GetMulti([]string{"domain_key4_value4_997200"}).Times(0)
	client.EXPECT().Increment("domain_key4_value4_997200", uint64(1)).Times(0)
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

	cache.Flush()
}

func TestNearLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewRateLimitCacheImpl(client, timeSource, nil, 0, nil, statsStore, 0.8, "")

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key4_value4_997200"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_997200": 10}), nil,
	)
	client.EXPECT().Increment("domain_key4_value4_997200", uint64(1)).Return(uint64(11), nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 1)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, "key4_value4", statsStore)}

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
	client.EXPECT().GetMulti([]string{"domain_key4_value4_997200"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_997200": 12}), nil,
	)
	client.EXPECT().Increment("domain_key4_value4_997200", uint64(1)).Return(uint64(13), nil)

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
	client.EXPECT().GetMulti([]string{"domain_key4_value4_997200"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_997200": 15}), nil,
	)
	client.EXPECT().Increment("domain_key4_value4_997200", uint64(1)).Return(uint64(16), nil)

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
	client.EXPECT().GetMulti([]string{"domain_key5_value5_1234"}).Return(
		getMultiResult(map[string]int{"domain_key5_value5_1234": 2}), nil,
	)
	client.EXPECT().Increment("domain_key5_value5_1234", uint64(3)).Return(uint64(5), nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key5", "value5"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key5_value5", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 15, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())

	// All of it under limit, some over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key6_value6_1234"}).Return(
		getMultiResult(map[string]int{"domain_key6_value6_1234": 5}), nil,
	)
	client.EXPECT().Increment("domain_key6_value6_1234", uint64(2)).Return(uint64(7), nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key6", "value6"}}}, 2)
	limits = []*config.RateLimit{config.NewRateLimit(8, pb.RateLimitResponse_RateLimit_SECOND, "key6_value6", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.WithinLimit.Value())

	// All of it under limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key7_value7_1234"}).Return(
		getMultiResult(map[string]int{"domain_key7_value7_1234": 16}), nil,
	)
	client.EXPECT().Increment("domain_key7_value7_1234", uint64(3)).Return(uint64(19), nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key7", "value7"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key7_value7", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(3), limits[0].Stats.WithinLimit.Value())

	// Some of it over limit, all of it over near limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key8_value8_1234"}).Return(
		getMultiResult(map[string]int{"domain_key8_value8_1234": 19}), nil,
	)
	client.EXPECT().Increment("domain_key8_value8_1234", uint64(3)).Return(uint64(22), nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key8", "value8"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key8_value8", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())

	// Some of it in all three places
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key9_value9_1234"}).Return(
		getMultiResult(map[string]int{"domain_key9_value9_1234": 15}), nil,
	)
	client.EXPECT().Increment("domain_key9_value9_1234", uint64(7)).Return(uint64(22), nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key9", "value9"}}}, 7)
	limits = []*config.RateLimit{config.NewRateLimit(20, pb.RateLimitResponse_RateLimit_SECOND, "key9_value9", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(7), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(4), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())

	// all of it over limit
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key10_value10_1234"}).Return(
		getMultiResult(map[string]int{"domain_key10_value10_1234": 27}), nil,
	)
	client.EXPECT().Increment("domain_key10_value10_1234", uint64(3)).Return(uint64(30), nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key10", "value10"}}}, 3)
	limits = []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key10_value10", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(3), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())

	cache.Flush()
}

func TestMemcacheWithJitter(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	jitterSource := mock_utils.NewMockJitterRandSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewRateLimitCacheImpl(client, timeSource, rand.New(jitterSource), 3600, nil, statsStore, 0.8, "")

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	jitterSource.EXPECT().Int63().Return(int64(100))

	// Key is not found in memcache
	client.EXPECT().GetMulti([]string{"domain_key_value_1234"}).Return(nil, nil)
	// First increment attempt will fail
	client.EXPECT().Increment("domain_key_value_1234", uint64(1)).Return(
		uint64(0), memcache.ErrCacheMiss)
	// Add succeeds
	client.EXPECT().Add(
		&memcache.Item{
			Key:   "domain_key_value_1234",
			Value: []byte(strconv.FormatUint(1, 10)),
			// 1 second + 100 seconds of jitter
			Expiration: int32(101),
		},
	).Return(nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	cache.Flush()
}

func TestMemcacheAdd(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewRateLimitCacheImpl(client, timeSource, nil, 0, nil, statsStore, 0.8, "")

	// Test a race condition with the initial add
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)

	client.EXPECT().GetMulti([]string{"domain_key_value_1234"}).Return(nil, nil)
	client.EXPECT().Increment("domain_key_value_1234", uint64(1)).Return(
		uint64(0), memcache.ErrCacheMiss)
	// Add fails, must have been a race condition
	client.EXPECT().Add(
		&memcache.Item{
			Key:        "domain_key_value_1234",
			Value:      []byte(strconv.FormatUint(1, 10)),
			Expiration: int32(1),
		},
	).Return(memcache.ErrNotStored)
	// Should work the second time, since some other client added the key.
	client.EXPECT().Increment("domain_key_value_1234", uint64(1)).Return(
		uint64(2), nil)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	// A rate limit with 1-minute window
	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)
	client.EXPECT().GetMulti([]string{"domain_key2_value2_1200"}).Return(nil, nil)
	client.EXPECT().Increment("domain_key2_value2_1200", uint64(1)).Return(
		uint64(0), memcache.ErrCacheMiss)
	client.EXPECT().Add(
		&memcache.Item{
			Key:        "domain_key2_value2_1200",
			Value:      []byte(strconv.FormatUint(1, 10)),
			Expiration: int32(60),
		},
	).Return(nil)

	request = common.NewRateLimitRequest("domain", [][][2]string{{{"key2", "value2"}}}, 1)
	limits = []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key2_value2", statsStore)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: utils.CalculateReset(limits[0].Limit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	cache.Flush()
}

func getMultiResult(vals map[string]int) map[string]*memcache.Item {
	result := make(map[string]*memcache.Item, len(vals))
	for k, v := range vals {
		result[k] = &memcache.Item{
			Value: []byte(strconv.Itoa(v)),
		}
	}
	return result
}
