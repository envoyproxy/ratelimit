// The memcached limiter uses GetMulti() to check keys in parallel and then does
// increments asynchronously in the backend, since the memcache interface doesn't
// support multi-increment and it seems worthwhile to minimize the number of
// concurrent or sequential RPCs in the critical path.
//
// Another difference from redis is that memcache doesn't create a key implicitly by
// incrementing a missing entry. Instead, when increment fails an explicit "add" needs
// to be called. The process of increment becomes a bit of a dance since we try to
// limit the number of RPCs. First we call increment, then add if the increment
// failed, then increment again if the add failed (which could happen if there was
// a race to call "add").
//
// Note that max memcache key length is 250 characters. Attempting to get or increment
// a longer key will return memcache.ErrMalformedKey

package memcached

import (
	"context"
	"math/rand"
	"sync"

	"github.com/coocood/freecache"
	stats "github.com/lyft/gostats"

	"github.com/bradfitz/gomemcache/memcache"

	logger "github.com/sirupsen/logrus"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/utils"

	storage_strategy "github.com/envoyproxy/ratelimit/src/storage/strategy"
)

type rateLimitMemcacheImpl struct {
	client                     storage_strategy.StorageStrategy
	timeSource                 utils.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	localCache                 *freecache.Cache
	waitGroup                  sync.WaitGroup
	nearLimitRatio             float32
	baseRateLimiter            *limiter.BaseRateLimiter
}

var AutoFlushForIntegrationTests bool = false

var _ limiter.RateLimitCache = (*rateLimitMemcacheImpl)(nil)

func (this *rateLimitMemcacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the RateLimit request.
	hitsAddend := utils.Max(1, request.HitsAddend)

	// First build a list of all cache keys that we are actually going to hit.
	cacheKeys := this.baseRateLimiter.GenerateCacheKeys(request, limits, hitsAddend)

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	results := make([]uint64, len(request.Descriptors))

	// Now, actually setup the pipeline, skipping empty cache keys.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" {
			continue
		}

		// Check if key is over the limit in local cache.
		if this.baseRateLimiter.IsOverLimitWithLocalCache(cacheKey.Key) {
			isOverLimitWithLocalCache[i] = true
			logger.Debugf("cache key is over the limit: %s", cacheKey.Key)
			continue
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
		if this.baseRateLimiter.ExpirationJitterMaxSeconds > 0 {
			expirationSeconds += this.baseRateLimiter.JitterRand.Int63n(this.baseRateLimiter.ExpirationJitterMaxSeconds)
		}

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		value, err := this.client.GetValue(cacheKey.Key)
		if err != nil {
			logger.Error(err)
		}
		results[i] = value

	}

	// Now fetch the pipeline.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))
	for i, cacheKey := range cacheKeys {

		limitBeforeIncrease := uint32(results[i])
		limitAfterIncrease := limitBeforeIncrease + hitsAddend

		limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

		responseDescriptorStatuses[i] = this.baseRateLimiter.GetResponseDescriptorStatus(cacheKey.Key,
			limitInfo, isOverLimitWithLocalCache[i], hitsAddend)
	}

	this.waitGroup.Add(1)
	go this.increaseAsync(cacheKeys, isOverLimitWithLocalCache, limits, uint64(hitsAddend))

	return responseDescriptorStatuses
}

func (this *rateLimitMemcacheImpl) increaseAsync(cacheKeys []limiter.CacheKey, isOverLimitWithLocalCache []bool,
	limits []*config.RateLimit, hitsAddend uint64) {
	defer this.waitGroup.Done()
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || isOverLimitWithLocalCache[i] {
			continue
		}

		err := this.client.IncrementValue(cacheKey.Key, hitsAddend)
		if err == memcache.ErrCacheMiss {
			expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
			if this.expirationJitterMaxSeconds > 0 {
				expirationSeconds += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
			}

			// Need to add instead of increment.
			err = this.client.SetValue(cacheKey.Key, hitsAddend, uint64(expirationSeconds))
			if err == memcache.ErrNotStored {
				// There was a race condition to do this add. We should be able to increment
				// now instead.
				err := this.client.IncrementValue(cacheKey.Key, hitsAddend)
				if err != nil {
					logger.Errorf("Failed to increment key %s after failing to add: %s", cacheKey.Key, err)
					continue
				}
			} else if err != nil {
				logger.Errorf("Failed to add key %s: %s", cacheKey.Key, err)
				continue
			}
		} else if err != nil {
			logger.Errorf("Failed to increment key %s: %s", cacheKey.Key, err)
			continue
		}
	}
}

func (this *rateLimitMemcacheImpl) Flush() {
	this.waitGroup.Wait()
}

func NewRateLimitCacheImpl(client storage_strategy.StorageStrategy, timeSource utils.TimeSource, jitterRand *rand.Rand,
	expirationJitterMaxSeconds int64, localCache *freecache.Cache, scope stats.Scope, nearLimitRatio float32, cacheKeyPrefix string) limiter.RateLimitCache {
	return &rateLimitMemcacheImpl{
		client:                     client,
		timeSource:                 timeSource,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
		baseRateLimiter:            limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio, cacheKeyPrefix),
	}
}
