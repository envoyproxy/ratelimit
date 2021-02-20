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

func TestFixedIsOverLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewFixedWindowAlgorithm(timeSource, nil, 0.8, "")

	var result int64 = 1
	var hitsAddend int64 = 1
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	actualIsOverLimit, actualLimitRemaining, actualDurationUntilReset := algorithm.IsOverLimit(limit, result, hitsAddend)

	assert.Equal(false, actualIsOverLimit)
	assert.Equal(int64(9), actualLimitRemaining)
	assert.Equal(1, actualDurationUntilReset)

	result = 10
	hitsAddend = 1
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	actualIsOverLimit, actualLimitRemaining, actualDurationUntilReset = algorithm.IsOverLimit(limit, result, hitsAddend)

	assert.Equal(false, actualIsOverLimit)
	assert.Equal(int64(0), actualLimitRemaining)
	assert.Equal(1, actualDurationUntilReset)

	result = 11
	hitsAddend = 1
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	actualIsOverLimit, actualLimitRemaining, actualDurationUntilReset = algorithm.IsOverLimit(limit, result, hitsAddend)

	assert.Equal(true, actualIsOverLimit)
	assert.Equal(int64(0), actualLimitRemaining)
	assert.Equal(1, actualDurationUntilReset)
}

func TestFixedCalculateSimpleReset(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewFixedWindowAlgorithm(timeSource, nil, 0.8, "")

	timeSource.EXPECT().UnixNow().Return(int64(1)).MaxTimes(1)
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)

	actualResetDuration := algorithm.CalculateSimpleReset(limit, timeSource)
	expectedResetDuration := &duration.Duration{Seconds: 1}
	assert.Equal(expectedResetDuration, actualResetDuration)

	timeSource.EXPECT().UnixNow().Return(int64(30)).MaxTimes(1)
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)

	actualResetDuration = algorithm.CalculateSimpleReset(limit, timeSource)
	expectedResetDuration = &duration.Duration{Seconds: 30}
	assert.Equal(expectedResetDuration, actualResetDuration)

	timeSource.EXPECT().UnixNow().Return(int64(60)).MaxTimes(1)
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, "key_value", statsStore)

	actualResetDuration = algorithm.CalculateSimpleReset(limit, timeSource)
	expectedResetDuration = &duration.Duration{Seconds: 59 * 60}
	assert.Equal(expectedResetDuration, actualResetDuration)
}

func TestFixedCalculateReset(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	timeSource := mock_utils.NewMockTimeSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	algorithm := algorithm.NewFixedWindowAlgorithm(timeSource, nil, 0.8, "")

	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, "key_value", statsStore)

	timeSource.EXPECT().UnixNow().Return(int64(45)).MaxTimes(1)

	actualResetDuration := algorithm.CalculateReset(true, limit, timeSource)
	expectedResetDuration := &duration.Duration{Seconds: 15}
	assert.Equal(expectedResetDuration, actualResetDuration)

	timeSource.EXPECT().UnixNow().Return(int64(45)).MaxTimes(1)

	actualResetDuration = algorithm.CalculateReset(false, limit, timeSource)
	expectedResetDuration = &duration.Duration{Seconds: 15}
	assert.Equal(expectedResetDuration, actualResetDuration)
}
