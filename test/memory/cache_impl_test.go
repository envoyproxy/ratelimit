package redis_test

import (
	"github.com/envoyproxy/ratelimit/src/memory"
	"github.com/envoyproxy/ratelimit/src/settings"
	"math/rand"
	"testing"

	"github.com/envoyproxy/ratelimit/test/mocks/stats"

	"github.com/mediocregopher/radix/v3"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	gostats "github.com/lyft/gostats"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/redis"
	"github.com/envoyproxy/ratelimit/src/utils"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/test/common"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
)

func pipeAppend(pipeline redis.Pipeline, rcv interface{}, cmd, key string, args ...interface{}) redis.Pipeline {
	return append(pipeline, radix.FlatCmd(rcv, cmd, key, args...))
}

func TestMemory(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)

	timeSource := mock_utils.NewMockTimeSource(controller)
	settings := settings.Settings{ExpirationJitterMaxSeconds: 0, NearLimitRatio: 0.8, CacheKeyPrefix: ""}
	cache := memory.NewRateLimiterCacheImplFromSettings(settings, timeSource, rand.New(rand.NewSource(1)), nil, nil, sm)

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"), false, false)}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 9, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)}},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())

	timeSource.EXPECT().UnixNow().Return(int64(1234)).MaxTimes(3)

	request = common.NewRateLimitRequest(
		"domain",
		[][][2]string{
			{{"key2", "value2"}},
			{{"key2", "value2"}, {"subkey2", "subvalue2"}},
		}, 11)
	limits = []*config.RateLimit{
		nil,
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key2_value2_subkey2_subvalue2"), false, false),
	}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
		},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(11), limits[1].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[1].Stats.OverLimit.Value())
	assert.Equal(uint64(2), limits[1].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[1].Stats.WithinLimit.Value())

	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(5)

	request = common.NewRateLimitRequest(
		"domain",
		[][][2]string{
			{{"key3", "value3"}},
			{{"key3", "value3"}, {"subkey3", "subvalue3"}},
		}, 11)
	limits = []*config.RateLimit{
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key3_value3"), false, false),
		config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_DAY, sm.NewStats("key3_value3_subkey3_subvalue3"), false, false),
	}
	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[0].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
			{Code: pb.RateLimitResponse_OVER_LIMIT, CurrentLimit: limits[1].Limit, LimitRemaining: 0, DurationUntilReset: utils.CalculateReset(&limits[1].Limit.Unit, timeSource)},
		},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(11), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())
	assert.Equal(uint64(11), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(1), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.WithinLimit.Value())
}

func TestNearLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := gostats.NewStore(gostats.NewNullSink(), false)
	sm := stats.NewMockStatManager(statsStore)
	settings := settings.Settings{ExpirationJitterMaxSeconds: 0, NearLimitRatio: 0.8, CacheKeyPrefix: ""}
	cache := memory.NewRateLimiterCacheImplFromSettings(settings, timeSource, rand.New(rand.NewSource(1)), nil, nil, sm)

	// Test Near Limit Stats. Under Near Limit Ratio
	timeSource.EXPECT().UnixNow().Return(int64(1000000)).MaxTimes(3)

	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key4", "value4"}}}, 11)

	limits := []*config.RateLimit{
		config.NewRateLimit(15, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key4_value4"), false, false),
	}

	assert.Equal(
		[]*pb.RateLimitResponse_DescriptorStatus{
			{Code: pb.RateLimitResponse_OK, CurrentLimit: limits[0].Limit, LimitRemaining: 4, DurationUntilReset: utils.CalculateReset(&limits[0].Limit.Unit, timeSource)},
		},
		cache.DoLimit(nil, request, limits))
	assert.Equal(uint64(11), limits[0].Stats.TotalHits.Value())
	assert.Equal(uint64(0), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(uint64(11), limits[0].Stats.WithinLimit.Value())
}
