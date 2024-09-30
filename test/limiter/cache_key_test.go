package limiter

import (
	"strconv"
	"strings"
	"testing"
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/golang/mock/gomock"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/test/common"
	mockstats "github.com/envoyproxy/ratelimit/test/mocks/stats"
)

func TestCacheKeyGenerator(t *testing.T) {
	cacheKeyGenerator := limiter.NewCacheKeyGenerator("")
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := stats.NewStore(stats.NewNullSink(), false)
	sm := mockstats.NewMockStatManager(statsStore)
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	timestamp := time.Date(2024, 5, 4, 12, 30, 15, 30, time.UTC)

	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, sm.NewStats("key_value"), false, false, "", nil, false, 1)
	cacheKey := cacheKeyGenerator.GenerateCacheKey("domain", request.Descriptors[0], limit, timestamp.Unix())
	assert.Equal("domain_key_value_1714825815", cacheKey.Key) // Rounded down to the nearest seconds (2024-05-04 12:30:15)
	assert.True(cacheKey.PerSecond)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, "", nil, false, 1)
	cacheKey = cacheKeyGenerator.GenerateCacheKey("domain", request.Descriptors[0], limit, timestamp.Unix())
	assert.Equal("domain_key_value_1714825800", cacheKey.Key) // Rounded down to the nearest minute (2024-05-04 12:30:00)
	assert.False(cacheKey.PerSecond)

	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key_value"), false, false, "", nil, false, 1)
	cacheKey = cacheKeyGenerator.GenerateCacheKey("domain", request.Descriptors[0], limit, timestamp.Unix())
	assert.Equal("domain_key_value_1714824000", cacheKey.Key) // Rounded down to the nearest hour (2024-05-04 12:00:00)
	assert.False(cacheKey.PerSecond)

	timestamp = time.Date(2024, 5, 4, 12, 59, 59, 30, time.UTC)
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key_value"), false, false, "", nil, false, 1)
	cacheKey = cacheKeyGenerator.GenerateCacheKey("domain", request.Descriptors[0], limit, timestamp.Unix())
	assert.Equal("domain_key_value_1714824000", cacheKey.Key) // Also rounded down to 2024-05-04 12:00:00
	assert.False(cacheKey.PerSecond)
}

func TestCacheKeyGeneratorWithUnitMultiplier(t *testing.T) {
	cacheKeyGenerator := limiter.NewCacheKeyGenerator("")
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()

	statsStore := stats.NewStore(stats.NewNullSink(), false)
	sm := mockstats.NewMockStatManager(statsStore)
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)

	// Rounding down to nearest 5 minutes (creating 5-minute long buckets)
	limit := config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_MINUTE, sm.NewStats("key_value"), false, false, "", nil, false, 5)
	testsRoundingDownToNearestFiveMinutes := []struct {
		input             time.Time
		expectedTimestamp time.Time
	}{
		{time.Date(2024, 5, 4, 12, 0, 15, 30, time.UTC), time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)},
		{time.Date(2024, 5, 4, 12, 1, 15, 30, time.UTC), time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)},
		{time.Date(2024, 5, 4, 12, 4, 15, 30, time.UTC), time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)},
		{time.Date(2024, 5, 4, 12, 5, 15, 30, time.UTC), time.Date(2024, 5, 4, 12, 5, 0, 0, time.UTC)},
	}

	for _, testCase := range testsRoundingDownToNearestFiveMinutes {
		cacheKey := cacheKeyGenerator.GenerateCacheKey("domain", request.Descriptors[0], limit, testCase.input.Unix())
		unixTime, _ := strconv.ParseInt(strings.Replace(cacheKey.Key, "domain_key_value_", "", 1), 10, 64)
		assert.Equal(testCase.expectedTimestamp, time.Unix(unixTime, 0).UTC())
	}

	// Rounding down to nearest 2 hours (creating 2-hour long buckets)
	limit = config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_HOUR, sm.NewStats("key_value"), false, false, "", nil, false, 2)
	testsRoundingDownToNearestThreeHours := []struct {
		input             time.Time
		expectedTimestamp time.Time
	}{
		{time.Date(2024, 5, 4, 12, 0, 15, 30, time.UTC), time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)},
		{time.Date(2024, 5, 4, 13, 0, 15, 30, time.UTC), time.Date(2024, 5, 4, 12, 0, 0, 0, time.UTC)},
		{time.Date(2024, 5, 4, 14, 4, 15, 30, time.UTC), time.Date(2024, 5, 4, 14, 0, 0, 0, time.UTC)},
		{time.Date(2024, 5, 4, 15, 5, 15, 30, time.UTC), time.Date(2024, 5, 4, 14, 0, 0, 0, time.UTC)},
	}

	for _, testCase := range testsRoundingDownToNearestThreeHours {
		cacheKey := cacheKeyGenerator.GenerateCacheKey("domain", request.Descriptors[0], limit, testCase.input.Unix())
		unixTime, _ := strconv.ParseInt(strings.Replace(cacheKey.Key, "domain_key_value_", "", 1), 10, 64)
		assert.Equal(testCase.expectedTimestamp, time.Unix(unixTime, 0).UTC())
	}
}
