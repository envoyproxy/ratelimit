package limiter

import (
	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	logger "github.com/sirupsen/logrus"
	"math"
	"math/rand"
)

type BaseRateLimiter struct {
	timeSource                 utils.TimeSource
	JitterRand                 *rand.Rand
	ExpirationJitterMaxSeconds int64
	cacheKeyGenerator          CacheKeyGenerator
	localCache                 *freecache.Cache
	nearLimitRatio             float32
}

type LimitInfo struct {
	limit               *config.RateLimit
	limitBeforeIncrease uint32
	limitAfterIncrease  uint32
	nearLimitThreshold  uint32
	overLimitThreshold  uint32
}

func NewRateLimitInfo(limit *config.RateLimit, limitBeforeIncrease uint32, limitAfterIncrease uint32,
	nearLimitThreshold uint32, overLimitThreshold uint32) *LimitInfo {
	return &LimitInfo{limit: limit, limitBeforeIncrease: limitBeforeIncrease, limitAfterIncrease: limitAfterIncrease,
		nearLimitThreshold: nearLimitThreshold, overLimitThreshold: overLimitThreshold}
}

// Generates cache keys for given rate limit request. Each cache key is represented by a concatenation of
// domain, descriptor and current timestamp.
func (this *BaseRateLimiter) GenerateCacheKeys(request *pb.RateLimitRequest,
	limits []*config.RateLimit, hitsAddend uint32) []CacheKey {
	assert.Assert(len(request.Descriptors) == len(limits))
	cacheKeys := make([]CacheKey, len(request.Descriptors))
	now := this.timeSource.UnixNow()
	for i := 0; i < len(request.Descriptors); i++ {
		// generateCacheKey() returns an empty string in the key if there is no limit
		// so that we can keep the arrays all the same size.
		cacheKeys[i] = this.cacheKeyGenerator.GenerateCacheKey(request.Domain, request.Descriptors[i], limits[i], now)
		// Increase statistics for limits hit by their respective requests.
		if limits[i] != nil {
			limits[i].Stats.TotalHits.Add(uint64(hitsAddend))
		}
	}
	return cacheKeys
}

// Returns `true` in case local cache is enabled and contains value for provided cache key, `false` otherwise.
func (this *BaseRateLimiter) IsOverLimitWithLocalCache(key string) bool {
	if this.localCache != nil {
		// Get returns the value or not found error.
		_, err := this.localCache.Get([]byte(key))
		if err == nil {
			return true
		}
	}
	return false
}

// Generates response descriptor status based on cache key, over the limit with local cache, over the limit and
// near the limit thresholds. Thresholds are checked in order and are mutually exclusive.
func (this *BaseRateLimiter) GetResponseDescriptorStatus(key string, limitInfo *LimitInfo,
	isOverLimitWithLocalCache bool, hitsAddend uint32) *pb.RateLimitResponse_DescriptorStatus {
	if key == "" {
		return this.generateResponseDescriptorStatus(pb.RateLimitResponse_OK,
			nil, 0)
	}
	if isOverLimitWithLocalCache {
		limitInfo.limit.Stats.OverLimit.Add(uint64(hitsAddend))
		limitInfo.limit.Stats.OverLimitWithLocalCache.Add(uint64(hitsAddend))
		return this.generateResponseDescriptorStatus(pb.RateLimitResponse_OVER_LIMIT,
			limitInfo.limit.Limit, 0)
	}
	var responseDescriptorStatus *pb.RateLimitResponse_DescriptorStatus
	limitInfo.overLimitThreshold = limitInfo.limit.Limit.RequestsPerUnit
	// The nearLimitThreshold is the number of requests that can be made before hitting the nearLimitRatio.
	// We need to know it in both the OK and OVER_LIMIT scenarios.
	limitInfo.nearLimitThreshold = uint32(math.Floor(float64(float32(limitInfo.overLimitThreshold) * this.nearLimitRatio)))
	logger.Debugf("cache key: %s current: %d", key, limitInfo.limitAfterIncrease)
	if limitInfo.limitAfterIncrease > limitInfo.overLimitThreshold {
		responseDescriptorStatus = this.generateResponseDescriptorStatus(pb.RateLimitResponse_OVER_LIMIT,
			limitInfo.limit.Limit, 0)

		checkOverLimitThreshold(limitInfo, hitsAddend)

		if this.localCache != nil {
			// Set the TTL of the local_cache to be the entire duration.
			// Since the cache_key gets changed once the time crosses over current time slot, the over-the-limit
			// cache keys in local_cache lose effectiveness.
			// For example, if we have an hour limit on all mongo connections, the cache key would be
			// similar to mongo_1h, mongo_2h, etc. In the hour 1 (0h0m - 0h59m), the cache key is mongo_1h, we start
			// to get ratelimited in the 50th minute, the ttl of local_cache will be set as 1 hour(0h50m-1h49m).
			// In the time of 1h1m, since the cache key becomes different (mongo_2h), it won't get ratelimited.
			err := this.localCache.Set([]byte(key), []byte{}, int(utils.UnitToDivider(limitInfo.limit.Limit.Unit)))
			if err != nil {
				logger.Errorf("Failing to set local cache key: %s", key)
			}
		}
	} else {
		responseDescriptorStatus = this.generateResponseDescriptorStatus(pb.RateLimitResponse_OK,
			limitInfo.limit.Limit, limitInfo.overLimitThreshold-limitInfo.limitAfterIncrease)

		// The limit is OK but we additionally want to know if we are near the limit.
		checkNearLimitThreshold(limitInfo, hitsAddend)
	}
	return responseDescriptorStatus
}

func NewBaseRateLimit(timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64,
	localCache *freecache.Cache, nearLimitRatio float32) *BaseRateLimiter {
	return &BaseRateLimiter{
		timeSource:                 timeSource,
		JitterRand:                 jitterRand,
		ExpirationJitterMaxSeconds: expirationJitterMaxSeconds,
		cacheKeyGenerator:          NewCacheKeyGenerator(),
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
	}
}

func checkOverLimitThreshold(limitInfo *LimitInfo, hitsAddend uint32) {
	// Increase over limit statistics. Because we support += behavior for increasing the limit, we need to
	// assess if the entire hitsAddend were over the limit. That is, if the limit's value before adding the
	// N hits was over the limit, then all the N hits were over limit.
	// Otherwise, only the difference between the current limit value and the over limit threshold
	// were over limit hits.
	if limitInfo.limitBeforeIncrease >= limitInfo.overLimitThreshold {
		limitInfo.limit.Stats.OverLimit.Add(uint64(hitsAddend))
	} else {
		limitInfo.limit.Stats.OverLimit.Add(uint64(limitInfo.limitAfterIncrease - limitInfo.overLimitThreshold))

		// If the limit before increase was below the over limit value, then some of the hits were
		// in the near limit range.
		limitInfo.limit.Stats.NearLimit.Add(uint64(limitInfo.overLimitThreshold -
			utils.Max(limitInfo.nearLimitThreshold, limitInfo.limitBeforeIncrease)))
	}
}

func checkNearLimitThreshold(limitInfo *LimitInfo, hitsAddend uint32) {
	if limitInfo.limitAfterIncrease > limitInfo.nearLimitThreshold {
		// Here we also need to assess which portion of the hitsAddend were in the near limit range.
		// If all the hits were over the nearLimitThreshold, then all hits are near limit. Otherwise,
		// only the difference between the current limit value and the near limit threshold were near
		// limit hits.
		if limitInfo.limitBeforeIncrease >= limitInfo.nearLimitThreshold {
			limitInfo.limit.Stats.NearLimit.Add(uint64(hitsAddend))
		} else {
			limitInfo.limit.Stats.NearLimit.Add(uint64(limitInfo.limitAfterIncrease - limitInfo.nearLimitThreshold))
		}
	}
}

func (this *BaseRateLimiter) generateResponseDescriptorStatus(responseCode pb.RateLimitResponse_Code,
	limit *pb.RateLimitResponse_RateLimit, limitRemaining uint32) *pb.RateLimitResponse_DescriptorStatus {
	if limit != nil {
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               responseCode,
			CurrentLimit:       limit,
			LimitRemaining:     limitRemaining,
			DurationUntilReset: utils.CalculateReset(limit, this.timeSource),
		}
	} else {
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:           responseCode,
			CurrentLimit:   limit,
			LimitRemaining: limitRemaining,
		}
	}
}
