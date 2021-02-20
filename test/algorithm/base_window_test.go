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

func TestGetResponseDescriptorStatus(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	// Fixed Window algorithm
	fixedAlgorithm := algorithm.NewFixedWindowAlgorithm(timeSource, nil, 0.8, "")
	baseAlgorithm := algorithm.NewWindow(fixedAlgorithm, "", nil, timeSource)

	key := "key_value"
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	var results int64 = 1
	var hitsAddend int64 = 1
	isOverLimitWithLocalCache := false

	timeSource.EXPECT().UnixNow().Return(int64(1)).MaxTimes(2)

	expectedResult := &pb.RateLimitResponse_DescriptorStatus{
		Code:               pb.RateLimitResponse_OK,
		CurrentLimit:       limit.Limit,
		LimitRemaining:     9,
		DurationUntilReset: utils.CalculateFixedReset(limit.Limit, timeSource)}

	actualResult := baseAlgorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)
	assert.Equal(expectedResult, actualResult)

	// Rolling Window algorithm
	rollingAlgorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")
	baseAlgorithm = algorithm.NewWindow(rollingAlgorithm, "", nil, timeSource)

	key = "key_value"
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	results = 0
	hitsAddend = 1
	isOverLimitWithLocalCache = false

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	expectedResult = &pb.RateLimitResponse_DescriptorStatus{
		Code:               pb.RateLimitResponse_OK,
		CurrentLimit:       limit.Limit,
		LimitRemaining:     9,
		DurationUntilReset: &duration.Duration{Nanos: 1e8}}

	actualResult = baseAlgorithm.GetResponseDescriptorStatus(key, limit, results, isOverLimitWithLocalCache, hitsAddend)
	assert.Equal(expectedResult, actualResult)
}

func TestIsOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	key := "key_value"

	timeSource := mock_utils.NewMockTimeSource(controller)

	// Fixed Window algorithm
	fixedLocalCache := freecache.NewCache(100)

	fixedAlgorithm := algorithm.NewFixedWindowAlgorithm(timeSource, fixedLocalCache, 0.8, "")
	baseAlgorithm := algorithm.NewWindow(fixedAlgorithm, "", fixedLocalCache, timeSource)

	assert.Equal(false, baseAlgorithm.IsOverLimitWithLocalCache(key))

	fixedLocalCache.Set([]byte(key), []byte{}, 1)
	assert.Equal(true, baseAlgorithm.IsOverLimitWithLocalCache(key))

	// Rolling Window algorithm
	rollingLocalCache := freecache.NewCache(100)

	rollingAlgorithm := algorithm.NewRollingWindowAlgorithm(timeSource, rollingLocalCache, 0.8, "")
	baseAlgorithm = algorithm.NewWindow(rollingAlgorithm, "", rollingLocalCache, timeSource)

	assert.Equal(false, baseAlgorithm.IsOverLimitWithLocalCache(key))

	rollingLocalCache.Set([]byte(key), []byte{}, 1)
	assert.Equal(true, baseAlgorithm.IsOverLimitWithLocalCache(key))
}

func TestGenerateCacheKeys(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)

	var hitsAddend int64 = 1
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limit := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}

	// Fixed Window algorithm
	fixedAlgorithm := algorithm.NewFixedWindowAlgorithm(timeSource, nil, 0.8, "")
	baseAlgorithm := algorithm.NewWindow(fixedAlgorithm, "", nil, timeSource)

	timeSource.EXPECT().UnixNow().Return(int64(1)).MaxTimes(1)

	expectedResult := []utils.CacheKey([]utils.CacheKey{{Key: "domain_key_value_1", PerSecond: true}})
	actualResult := baseAlgorithm.GenerateCacheKeys(request, limit, hitsAddend, 1)
	assert.Equal(expectedResult, actualResult)

	// Rolling Window algorithm
	rollingAlgorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")
	baseAlgorithm = algorithm.NewWindow(rollingAlgorithm, "", nil, timeSource)

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	expectedResult = []utils.CacheKey([]utils.CacheKey{{Key: "domain_key_value_0", PerSecond: true}})
	actualResult = baseAlgorithm.GenerateCacheKeys(request, limit, hitsAddend, 0)
	assert.Equal(expectedResult, actualResult)
}

func TestPopulateStats(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := stats.NewStore(stats.NewNullSink(), false)
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

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
