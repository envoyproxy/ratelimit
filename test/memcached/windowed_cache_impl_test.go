package memcached_test

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/memcached"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_memcached "github.com/envoyproxy/ratelimit/test/mocks/memcached/driver"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
	"github.com/golang/mock/gomock"
	"github.com/golang/protobuf/ptypes/duration"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
)

func TestMemcachedWindowed(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewWindowedRateLimitCacheImpl(client, timeSource, nil, 0, nil, 0.8, "")

	// test 1
	// test initial rate limit process

	// periode = 1 second
	// limit = 10 request/second
	// emissionInterval = 0.1 second
	// request = 1
	// increment = emissionInterval*request = 0.1 second

	// arriveAt = 1 second
	// tat = 1 second

	// newTat should be max(arriveAt,tat)+increment = 1.1 second
	// DurationUntilReset should be newTat-arriveat = 0.1 minute
	// expiration should be second(newTat-arriveat)+1 = 0.1 minute

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key_value_0"}).Return(
		getMultiResult(map[string]int{"domain_key_value_0": 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key_value_0",
		Value:      []byte(strconv.FormatInt(int64(1e9+1e8), 10)),
		Expiration: int32(1),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Nanos: 1e8}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// test 2
	// test rate limit with multiple description

	// periode = 1 minute = 60 second
	// limit = 10 request/minute
	// emissionInterval = 6 second
	// request = 1
	// increment = emissionInterval*request = 6 second

	// arriveAt = 1 second
	// tat = 1 second

	// newTat should be max(arriveAt,tat)+increment = 7 second
	// DurationUntilReset should be newTat-arriveat = 6 second
	// expiration should be second(newTat-arriveat)+1 = 7 second

	request = common.NewRateLimitRequest(
		"domain",
		[][][2]string{
			{{"key2", "value2"}},
			{{"key2", "value2"}, {"subkey2", "subvalue2"}},
		}, 1)
	limits = []*config.RateLimit{
		nil,
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key2_value2_subkey2_subvalue2", statsStore)}

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key2_value2_subkey2_subvalue2_0"}).Return(
		getMultiResult(map[string]int{"domain_key2_value2_subkey2_subvalue2_0": 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key2_value2_subkey2_subvalue2_0",
		Value:      []byte(strconv.FormatInt(int64(1e9+6e9), 10)),
		Expiration: int32(7),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Seconds: 6}}},
		cache.DoLimit(nil, request, limits))

	assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[1].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())

	// test 3
	// test rate limit with multiple description and different limit configuration

	// periode = 1 hour = 3600 second
	// limit = 10 request/hour
	// emissionInterval = 6 minute = 360 second
	// request = 1
	// increment = emissionInterval*request =  6 minute = 360 second

	// arriveAt = 1 second
	// tat = 1 second

	// newTat should be max(arriveAt,tat)+increment = 361 second
	// DurationUntilReset should be newTat-arriveat = 360 second
	// expiration should be second(newTat-arriveat)+1 = 361 second

	// periode = 1 day = 86400 second
	// limit = 10 request/day
	// emissionInterval = 8640 second
	// request = 1
	// increment = emissionInterval*request =  8640 second

	// arriveAt = 1 second
	// tat = 1 second

	// newTat should be max(arriveAt,tat)+increment = 8641 second
	// DurationUntilReset should be newTat-arriveat = 8640 second
	// expiration should be second(newTat-arriveat)+1 = 8641 second

	request = common.NewRateLimitRequest(
		"domain",
		[][][2]string{
			{{"key3", "value3"}},
			{{"key3", "value3"}, {"subkey3", "subvalue3"}},
		}, 1)
	limits = []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, "key3_value3", statsStore),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_DAY, "key3_value3_subkey3_subvalue3", statsStore)}

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(2)

	client.EXPECT().GetMulti([]string{"domain_key3_value3_0", "domain_key3_value3_subkey3_subvalue3_0"}).Return(
		getMultiResult(map[string]int{"domain_key3_value3_0": 1e9, "domain_key3_value3_subkey3_subvalue3_0": 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key3_value3_0",
		Value:      []byte(strconv.FormatInt(int64(1e9+360e9), 10)),
		Expiration: int32(361),
	})
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key3_value3_subkey3_subvalue3_0",
		Value:      []byte(strconv.FormatInt(int64(1e9+8640e9), 10)),
		Expiration: int32(8641),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Seconds: 360}},
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[1].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Seconds: 8640}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[1].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[1].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.NearLimit.Value())

	cache.Flush()
}

func TestNearLimitWindowed(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	cache := memcached.NewWindowedRateLimitCacheImpl(client, timeSource, nil, 0, nil, 0.8, "")
	domain := "domain"

	request := common.NewRateLimitRequest(domain, [][][2]string{{{"key4", "value4"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key4_value4", statsStore)}

	// Test Near Limit Stats. Under Near Limit Ratio
	// periode = 1 minute = 60 second
	// limit = 10 request/minute
	// emissionInterval = 6 second
	// request = 1
	// increment = emissionInterval*request = 6 second

	// arriveAt = 01 second
	// tat = 01 second

	// newTat should be max(arriveAt,tat)+increment = 7 second
	// expire should be (newtat-arriveat)+1 = 7 second
	// DurationUntilReset should be newtat-arriveat = 6 second

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key4_value4_0"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_0": 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key4_value4_0",
		Value:      []byte(strconv.FormatInt(int64(1e9+6e9), 10)),
		Expiration: int32(7),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Seconds: 6}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	// periode = 1 minute = 60 second
	// limit = 10 request/minute
	// emissionInterval = 6 second
	// request = 1
	// increment = emissionInterval*request = 6 second

	// arriveAt = 07 second
	// tat = 54 second

	// newTat should be max(arriveAt,tat)+increment = 60 second
	// expire should be (newtat-arriveat)+1 = 54 second
	// DurationUntilReset should be newtat-arriveat = 53 second
	timeSource.EXPECT().UnixNanoNow().Return(int64(7e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key4_value4_0"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_0": 54e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key4_value4_0",
		Value:      []byte(strconv.FormatInt(int64(60e9), 10)),
		Expiration: int32(54),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: &duration.Duration{Seconds: 53}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Test Near Limit Stats. We went OVER_LIMIT, but the near_limit counter only increases
	// when we are near limit, not after we have passed the limit.
	// periode = 1 minute = 60 second
	// limit = 10 request/minute
	// emissionInterval = 6 second
	// request = 1
	// increment = emissionInterval*request = 6 second

	// arriveAt = 04 second
	// tat = 60 second

	// newTat should be max(arriveAt,tat)+increment = 66 second
	// expire should be (tat-arriveat)+1 = 57 second
	// DurationUntilReset should be tat-arriveat = 56 second
	timeSource.EXPECT().UnixNanoNow().Return(int64(4e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key4_value4_0"}).Return(
		getMultiResult(map[string]int{"domain_key4_value4_0": 60e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key4_value4_0",
		Value:      []byte(strconv.FormatInt(int64(60e9), 10)),
		Expiration: int32(57),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: 56}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	cache.Flush()
}

func TestWindowedOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	sink := &common.TestStatSink{}
	statsStore := stats.NewStore(sink, true)
	localCache := freecache.NewCache(100)
	localCacheStats := utils.NewLocalCacheStats(localCache, statsStore.Scope("localcache"))
	cache := memcached.NewWindowedRateLimitCacheImpl(client, timeSource, nil, 0, localCache, 0.8, "")
	domain := "domain"

	request := common.NewRateLimitRequest(domain, [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)}
	// Test Near Limit Stats. Under Near Limit Ratio
	// periode = 60 minute = 3600 second
	// limit = 10 request/hour
	// emissionInterval = 6 minute
	// request = 1
	// increment = emissionInterval*request = 6 minute

	// arriveAt = 1 minute
	// tat = 12 minite

	// newTat should be max(arriveAt,tat)+increment = 18 minute
	// expire should be (newtat-arriveat)+1 second = 17 minute 1 second
	// DurationUntilReset should be newtat-arriveat = 17 minute
	timeSource.EXPECT().UnixNanoNow().Return(int64(1 * 60 * 1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key_value_0"}).Return(
		getMultiResult(map[string]int{"domain_key_value_0": 12 * 60 * 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key_value_0",
		Value:      []byte(strconv.FormatInt(int64(18*60*1e9), 10)),
		Expiration: int32((17 * 60) + 1),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 7, DurationUntilReset: &duration.Duration{Seconds: 17 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 1, 1, 0, 0)

	// Test Near Limit Stats. At Near Limit Ratio, still OK
	// periode = 60 minute = 3600 second
	// limit = 10 request/hour
	// emissionInterval = 6 minute
	// request = 1
	// increment = emissionInterval*request = 6 minute

	// arriveAt = 12 minute
	// tat = 60 minute

	// newTat should be max(arriveAt,tat)+increment = 66 minute
	// expire should be (newtat-arriveat)+1 second = 54 minute 1 second
	// DurationUntilReset should be newtat-arriveat = 54 minute
	timeSource.EXPECT().UnixNanoNow().Return(int64(12 * 60 * 1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key_value_0"}).Return(
		getMultiResult(map[string]int{"domain_key_value_0": 60 * 60 * 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key_value_0",
		Value:      []byte(strconv.FormatInt(int64(66*60*1e9), 10)),
		Expiration: int32((54 * 60) + 1),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 1, DurationUntilReset: &duration.Duration{Seconds: 54 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(2), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 2, 0, 0)

	// Test Over limit stats
	// periode = 60 minute = 3600 second
	// limit = 10 request/hour
	// emissionInterval = 6 minute
	// request = 1
	// increment = emissionInterval*request = 6 minute

	// arriveAt = 2 minute
	// tat = 72 minute

	// newTat should be max(arriveAt,tat)+increment = 78 minute (not used)
	// expire should be (tat-arriveat)+1 second = 70 minute 1 second
	// DurationUntilReset should be tat-arriveat = 70 minute
	timeSource.EXPECT().UnixNanoNow().Return(int64(2 * 60 * 1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key_value_0"}).Return(
		getMultiResult(map[string]int{"domain_key_value_0": 72 * 60 * 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key_value_0",
		Value:      []byte(strconv.FormatInt(int64(72*60*1e9), 10)),
		Expiration: int32((70 * 60) + 1),
	})

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: 70 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(3), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 0, 2, 3, 0, 1)

	// Test Over limit stats with local cache
	// periode = 60 minute = 3600 second
	// limit = 10 request/hour
	// emissionInterval = 6 minute
	// request = 1
	// increment = emissionInterval*request = 6 minute

	// arriveAt = 3 minute
	// tat = 72 minute

	// newTat should be max(arriveAt,tat)+increment = 78 minute (not used)
	// expire should be (tat-arriveat)+1 second = 69 minute 1 second
	// DurationUntilReset should be secondsToReset-(arriveAt%secondsToReset) = 57 minute
	timeSource.EXPECT().UnixNanoNow().Return(int64(3 * 60 * 1e9)).MaxTimes(1)

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: &duration.Duration{Seconds: 57 * 60}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(4), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimitWithLocalCache.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())

	// Check the local cache stats.
	testLocalCacheStats(localCacheStats, statsStore, sink, 1, 3, 4, 0, 1)

	cache.Flush()
}

func TestRedisWindowedWithJitter(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	client := mock_memcached.NewMockClient(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	jitterSource := mock_utils.NewMockJitterRandSource(controller)
	cache := memcached.NewWindowedRateLimitCacheImpl(client, timeSource, rand.New(jitterSource), 3600, nil, 0.8, "")

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	// periode = 1 second
	// limit = 10 request/second
	// emissionInterval = 1/10 second
	// request = 1
	// increment = emissionInterval*request = 1/10 second

	// arriveAt = 1 second
	// tat = 1 second

	// newTat should be max(arriveAt,tat)+increment = 1,1 second
	// expire should be (tat-arriveat)+1 second = 1 second
	// DurationUntilReset should be newTat-arriveat = 0.1 second

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	client.EXPECT().GetMulti([]string{"domain_key_value_0"}).Return(
		getMultiResult(map[string]int{"domain_key_value_0": 1e9}), nil,
	)
	client.EXPECT().Set(&memcache.Item{
		Key:        "domain_key_value_0",
		Value:      []byte(strconv.FormatInt(int64(1e9+1e8), 10)),
		Expiration: int32(100 + 1),
	})

	jitterSource.EXPECT().Int63().Return(int64(100))

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: &duration.Duration{Nanos: 1e8}}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())

	cache.Flush()
}
