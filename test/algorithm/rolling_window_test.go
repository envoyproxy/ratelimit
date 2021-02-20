package algorithm

import (
	"testing"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/algorithm"
	"github.com/envoyproxy/ratelimit/src/config"
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

func TestRollingCalculateSimpleReset(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	actualResetDuration := algorithm.CalculateSimpleReset(limit, timeSource)
	expectedResetDuration := &duration.Duration{Seconds: 1}
	assert.Equal(expectedResetDuration, actualResetDuration)

	timeSource.EXPECT().UnixNanoNow().Return(int64(30 * 1e9)).MaxTimes(1)
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)

	actualResetDuration = algorithm.CalculateSimpleReset(limit, timeSource)
	expectedResetDuration = &duration.Duration{Seconds: 30}
	assert.Equal(expectedResetDuration, actualResetDuration)

	timeSource.EXPECT().UnixNanoNow().Return(int64(60 * 1e9)).MaxTimes(1)
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)

	actualResetDuration = algorithm.CalculateSimpleReset(limit, timeSource)
	expectedResetDuration = &duration.Duration{Seconds: 59 * 60}
	assert.Equal(expectedResetDuration, actualResetDuration)
}

func TestRollingCalculateReset(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)

	var results int64 = 0
	var hitsAddend int64 = 1
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)

	// populating tat, newTat, arriveAt
	// that is required to execute CalculateAt

	// periode = 1 minute = 60 second
	// limit = 10 request/minute
	// emissionInterval = 6 second
	// request = 1
	// increment = emissionInterval*request = 6 second

	// arriveAt = 1 second
	// tat = 0 second

	// newTat should be max(arriveAt,tat)+increment = 7 second
	// DurationUntilReset should be newtat-arriveat = 6 second

	algorithm.IsOverLimit(limit, results, hitsAddend)
	isOverLimit := false

	actualResetDuration := algorithm.CalculateReset(isOverLimit, limit, timeSource)
	expectedResetDuration := &duration.Duration{Seconds: 6}
	assert.Equal(expectedResetDuration, actualResetDuration)

	timeSource.EXPECT().UnixNanoNow().Return(int64(3 * 60 * 1e9)).MaxTimes(1)

	results = 72 * 60 * 1e9
	hitsAddend = 1
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)

	// populating tat, newTat, arriveAt
	// that is required to execute CalculateAt

	// periode = 1 hour = 3600 second
	// limit = 10 request/hour
	// emissionInterval = 6 minute = 360 second
	// request = 1
	// increment = emissionInterval*request = 6 minute = 360 second

	// arriveAt = 3 minute
	// tat =  72 minute

	// newTat should be max(arriveAt,tat)+increment = 78 minute (not used)
	// DurationUntilReset should be tat-arriveat = 6 second

	algorithm.IsOverLimit(limit, results, hitsAddend)
	isOverLimit = true

	actualResetDuration = algorithm.CalculateReset(isOverLimit, limit, timeSource)
	expectedResetDuration = &duration.Duration{Seconds: 69 * 60}
	assert.Equal(expectedResetDuration, actualResetDuration)
}

func TestRollingGetExpirationSeconds(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewRollingWindowAlgorithm(timeSource, nil, 0.8, "")

	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	var results int64 = 0
	var hitsAddend int64 = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

	expectedResult := int64(1)
	actualResult := algorithm.GetExpirationSeconds()
	assert.Equal(expectedResult, actualResult)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	results = 2e9
	hitsAddend = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9 + 4e6)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

	expectedResult = int64(1)
	actualResult = algorithm.GetExpirationSeconds()
	assert.Equal(expectedResult, actualResult)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 0
	hitsAddend = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

	expectedResult = int64(7)
	actualResult = algorithm.GetExpirationSeconds()
	assert.Equal(expectedResult, actualResult)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 60e9
	hitsAddend = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(4e9)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

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

	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	var results int64 = 0
	var hitsAddend int64 = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

	expectedResult := int64(1e9 + 1e8)
	actualResult := algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)
	results = 2e9
	hitsAddend = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9 + 4e6)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

	expectedResult = int64(2e9)
	actualResult = algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 0
	hitsAddend = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(1e9)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

	expectedResult = int64(7e9)
	actualResult = algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)
	results = 60e9
	hitsAddend = 1

	timeSource.EXPECT().UnixNanoNow().Return(int64(4e9)).MaxTimes(1)
	algorithm.IsOverLimit(limit, results, hitsAddend)

	expectedResult = int64(60e9)
	actualResult = algorithm.GetResultsAfterIncrease()
	assert.Equal(expectedResult, actualResult)
}
