package algorithm

import (
	"testing"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/algorithm"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
	"github.com/golang/mock/gomock"
	"github.com/golang/protobuf/ptypes/duration"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
)

func TestRollingIsOverLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	var result int64 = 1e9
	var hitsAddend int64 = 1
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	actualIsOverLimit, actualLimitRemaining, actualDurationUntilReset := algorithm.IsOverLimit(limit, result, hitsAddend)

	assert.Equal(false, actualIsOverLimit)
	assert.Equal(int64(9), actualLimitRemaining)
	assert.Equal(0, actualDurationUntilReset)

	result = 0
	hitsAddend = 1
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1e9)
	actualIsOverLimit, actualLimitRemaining, actualDurationUntilReset = algorithm.IsOverLimit(limit, result, hitsAddend)

	assert.Equal(false, actualIsOverLimit)
	assert.Equal(int64(9), actualLimitRemaining)
	assert.Equal(0, actualDurationUntilReset)

	result = 3600e9
	hitsAddend = 1
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(4e9)
	actualIsOverLimit, actualLimitRemaining, actualDurationUntilReset = algorithm.IsOverLimit(limit, result, hitsAddend)

	assert.Equal(true, actualIsOverLimit)
	assert.Equal(int64(0), actualLimitRemaining)
	assert.Equal(359, actualDurationUntilReset)
}

func TestRollingIsOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	key := "key_value"

	timeSource := mock_utils.NewMockTimeSource(controller)
	localCache := freecache.NewCache(100)

	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, localCache, 0.8, "")
	assert.Equal(false, algorithm.IsOverLimitWithLocalCache(key))

	localCache.Set([]byte(key), []byte{}, 1)
	assert.Equal(true, algorithm.IsOverLimitWithLocalCache(key))
}

func TestRollingGenerateCacheKeys(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	var hitsAddend int64 = 1
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limit := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	expectedResult := []utils.CacheKey([]utils.CacheKey{{Key: "domain_key_value_0", PerSecond: true}})
	actualResult := algorithm.GenerateCacheKeys(request, limit, hitsAddend)
	assert.Equal(expectedResult, actualResult)
}

func TestRollingPopulateStats(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	algorithm.PopulateStats(limit, 1, 0, 0)
	assert.Equal(uint64(1), limit.Stats.NearLimit.Value())
	assert.Equal(uint64(0), limit.Stats.OverLimit.Value())
	assert.Equal(uint64(0), limit.Stats.OverLimitWithLocalCache.Value())

	algorithm.PopulateStats(limit, 0, 1, 0)
	assert.Equal(uint64(1), limit.Stats.NearLimit.Value())
	assert.Equal(uint64(1), limit.Stats.OverLimit.Value())
	assert.Equal(uint64(0), limit.Stats.OverLimitWithLocalCache.Value())

	algorithm.PopulateStats(limit, 0, 0, 1)
	assert.Equal(uint64(1), limit.Stats.NearLimit.Value())
	assert.Equal(uint64(1), limit.Stats.OverLimit.Value())
	assert.Equal(uint64(1), limit.Stats.OverLimitWithLocalCache.Value())
}

func TestRollingGetResponseDescriptorStatus(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	key := "key_value"
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	var results int64 = 0
	var hitsAddend int64 = 1
	isOverLimitWithLocalCache := false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	expectedResult := &pb.RateLimitResponse_DescriptorStatus{
		Code:               pb.RateLimitResponse_OK,
		CurrentLimit:       limit.Limit,
		LimitRemaining:     9,
		DurationUntilReset: &duration.Duration{Nanos: 1e8}}

	actualResult := algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)
	assert.Equal(expectedResult, actualResult)
}

func TestRollingGetExpirationSeconds(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	key := "key_value"
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	var results int64 = 0
	var hitsAddend int64 = 1
	isOverLimitWithLocalCache := false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult := int64(1)
	actualResult := algorithm.GetExpirationSeconds()
	assert.Equal(expectedResult, actualResult)

	key = "key_value"
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	results = 2e9
	hitsAddend = 1
	isOverLimitWithLocalCache = false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9 + 4e6)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult = int64(1)
	actualResult = algorithm.GetExpirationSeconds()
	assert.Equal(expectedResult, actualResult)

	key = "key_value"
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 0
	hitsAddend = 1
	isOverLimitWithLocalCache = false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult = int64(7)
	actualResult = algorithm.GetExpirationSeconds()
	assert.Equal(expectedResult, actualResult)

	key = "key_value"
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 60e9
	hitsAddend = 1
	isOverLimitWithLocalCache = false

	timeSource.EXPECT().UnixNanoNow().Return(int64(4e9)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult = int64(57)
	actualResult = algorithm.GetExpirationSeconds()
	assert.Equal(expectedResult, actualResult)
}

func TestRollingGetResultsAfterIncrease(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	key := "key_value"
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	var results int64 = 0
	var hitsAddend int64 = 1
	isOverLimitWithLocalCache := false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult := int64(1e9 + 1e8)
	actualResult := algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)

	key = "key_value"
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	results = 2e9
	hitsAddend = 1
	isOverLimitWithLocalCache = false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9 + 4e6)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult = int64(2e9)
	actualResult = algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)

	key = "key_value"
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 0
	hitsAddend = 1
	isOverLimitWithLocalCache = false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult = int64(7e9)
	actualResult = algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)

	key = "key_value"
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 60e9
	hitsAddend = 1
	isOverLimitWithLocalCache = false

	timeSource.EXPECT().UnixNanoNow().Return(int64(4e9)).MaxTimes(1)
	algorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)

	expectedResult = int64(60e9)
	actualResult = algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)
}
