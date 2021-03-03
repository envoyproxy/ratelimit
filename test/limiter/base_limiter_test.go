package limiter

import (
	"math/rand"
	"testing"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/test/common"
	mock_utils "github.com/envoyproxy/ratelimit/test/mocks/utils"
	"github.com/golang/mock/gomock"
	stats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
)

func TestGenerateCacheKeys(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	timeSource := mock_utils.NewMockTimeSource(controller)
	jitterSource := mock_utils.NewMockJitterRandSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	timeSource.EXPECT().UnixNow().Return(int64(1234))
	baseRateLimit := limiter.NewBaseRateLimit(timeSource, rand.New(jitterSource), 3600, nil, 0.8, "")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}
	assert.Equal(uint64(0), limits[0].Stats.TotalHits.Value())
	cacheKeys := baseRateLimit.GenerateCacheKeys(request, limits, 1)
	assert.Equal(1, len(cacheKeys))
	assert.Equal("domain_key_value_1234", cacheKeys[0].Key)
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
}

func TestGenerateCacheKeysPrefix(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	timeSource := mock_utils.NewMockTimeSource(controller)
	jitterSource := mock_utils.NewMockJitterRandSource(controller)
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	timeSource.EXPECT().UnixNow().Return(int64(1234))
	baseRateLimit := limiter.NewBaseRateLimit(timeSource, rand.New(jitterSource), 3600, nil, 0.8, "prefix:")
	request := common.NewRateLimitRequest("domain", [][][2]string{{{"key", "value"}}}, 1)
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}
	assert.Equal(uint64(0), limits[0].Stats.TotalHits.Value())
	cacheKeys := baseRateLimit.GenerateCacheKeys(request, limits, 1)
	assert.Equal(1, len(cacheKeys))
	assert.Equal("prefix:domain_key_value_1234", cacheKeys[0].Key)
	assert.Equal(uint64(1), limits[0].Stats.TotalHits.Value())
}

func TestOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	localCache := freecache.NewCache(100)
	localCache.Set([]byte("key"), []byte("value"), 100)
	baseRateLimit := limiter.NewBaseRateLimit(nil, nil, 3600, localCache, 0.8, "")
	// Returns true, as local cache contains over limit value for the key.
	assert.Equal(true, baseRateLimit.IsOverLimitWithLocalCache("key"))
}

func TestNoOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	baseRateLimit := limiter.NewBaseRateLimit(nil, nil, 3600, nil, 0.8, "")
	// Returns false, as local cache is nil.
	assert.Equal(false, baseRateLimit.IsOverLimitWithLocalCache("domain_key_value_1234"))
	localCache := freecache.NewCache(100)
	baseRateLimitWithLocalCache := limiter.NewBaseRateLimit(nil, nil, 3600, localCache, 0.8, "")
	// Returns false, as local cache does not contain value for cache key.
	assert.Equal(false, baseRateLimitWithLocalCache.IsOverLimitWithLocalCache("domain_key_value_1234"))
}

func TestGetResponseStatusEmptyKey(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	baseRateLimit := limiter.NewBaseRateLimit(nil, nil, 3600, nil, 0.8, "")
	responseStatus := baseRateLimit.GetResponseDescriptorStatus("", nil, false, 1)
	assert.Equal(pb.RateLimitResponse_OK, responseStatus.GetCode())
	assert.Equal(uint32(0), responseStatus.GetLimitRemaining())
}

func TestGetResponseStatusOverLimitWithLocalCache(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1234))
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	baseRateLimit := limiter.NewBaseRateLimit(timeSource, nil, 3600, nil, 0.8, "")
	limits := []*config.RateLimit{config.NewRateLimit(5, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}
	limitInfo := limiter.NewRateLimitInfo(limits[0], 2, 6, 4, 5)
	// As `isOverLimitWithLocalCache` is passed as `true`, immediate response is returned with no checks of the limits.
	responseStatus := baseRateLimit.GetResponseDescriptorStatus("key", limitInfo, true, 2)
	assert.Equal(pb.RateLimitResponse_OVER_LIMIT, responseStatus.GetCode())
	assert.Equal(uint32(0), responseStatus.GetLimitRemaining())
	assert.Equal(limits[0].Limit, responseStatus.GetCurrentLimit())
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(2), limits[0].Stats.OverLimitWithLocalCache.Value())
}

func TestGetResponseStatusOverLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1234))
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	localCache := freecache.NewCache(100)
	baseRateLimit := limiter.NewBaseRateLimit(timeSource, nil, 3600, localCache, 0.8, "")
	limits := []*config.RateLimit{config.NewRateLimit(5, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}
	limitInfo := limiter.NewRateLimitInfo(limits[0], 2, 7, 4, 5)
	responseStatus := baseRateLimit.GetResponseDescriptorStatus("key", limitInfo, false, 1)
	assert.Equal(pb.RateLimitResponse_OVER_LIMIT, responseStatus.GetCode())
	assert.Equal(uint32(0), responseStatus.GetLimitRemaining())
	assert.Equal(limits[0].Limit, responseStatus.GetCurrentLimit())
	result, _ := localCache.Get([]byte("key"))
	// Local cache should have been populated with over the limit key.
	assert.Equal("", string(result))
	assert.Equal(uint64(2), limits[0].Stats.OverLimit.Value())
	assert.Equal(uint64(1), limits[0].Stats.NearLimit.Value())
}

func TestGetResponseStatusBelowLimit(t *testing.T) {
	assert := assert.New(t)
	controller := gomock.NewController(t)
	defer controller.Finish()
	timeSource := mock_utils.NewMockTimeSource(controller)
	timeSource.EXPECT().UnixNow().Return(int64(1234))
	statsStore := stats.NewStore(stats.NewNullSink(), false)
	baseRateLimit := limiter.NewBaseRateLimit(timeSource, nil, 3600, nil, 0.8, "")
	limits := []*config.RateLimit{config.NewRateLimit(10, pb.RateLimitResponse_RateLimit_SECOND, "key_value", statsStore)}
	limitInfo := limiter.NewRateLimitInfo(limits[0], 2, 6, 9, 10)
	responseStatus := baseRateLimit.GetResponseDescriptorStatus("key", limitInfo, false, 1)
	assert.Equal(pb.RateLimitResponse_OK, responseStatus.GetCode())
	assert.Equal(uint32(4), responseStatus.GetLimitRemaining())
	assert.Equal(uint64(0), limits[0].Stats.NearLimit.Value())
	assert.Equal(limits[0].Limit, responseStatus.GetCurrentLimit())
	assert.Equal(uint64(1), limits[0].Stats.WithinLimit.Value())
}
