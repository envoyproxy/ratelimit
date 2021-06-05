package redis

import (
	"math/rand"
	"sync"

	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/utils"
	"golang.org/x/net/context"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	storage_strategy "github.com/envoyproxy/ratelimit/src/storage/strategy"
	logger "github.com/sirupsen/logrus"
)

type RedisError string

func (e RedisError) Error() string {
	return string(e)
}

type fixedRateLimitCacheImpl struct {
	client storage_strategy.StorageStrategy
	// Optional Client for a dedicated cache of per second limits.
	// If this client is nil, then the Cache will use the client for all
	// limits regardless of unit. If this client is not nil, then it
	// is used for limits that have a SECOND unit.
	perSecondClient            storage_strategy.StorageStrategy
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	baseRateLimiter            *limiter.BaseRateLimiter
	waitGroup                  sync.WaitGroup
}

func (this *fixedRateLimitCacheImpl) DoLimit(
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

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			value, err := this.perSecondClient.GetValue(cacheKey.Key)
			if err != nil {
				logger.Error(err)
			}

			results[i] = value
		} else {
			value, err := this.client.GetValue(cacheKey.Key)
			if err != nil {
				logger.Error(err)
			}

			results[i] = value
		}
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

		if cacheKey.Key == "" || isOverLimitWithLocalCache[i] {
			continue
		}

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
		if this.expirationJitterMaxSeconds > 0 {
			expirationSeconds += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
		}

		if this.perSecondClient != nil && cacheKey.PerSecond {
			err := this.perSecondClient.IncrementValue(cacheKey.Key, uint64(hitsAddend))
			if err != nil {
				logger.Error(err)
			}

			err = this.perSecondClient.SetExpire(cacheKey.Key, uint64(expirationSeconds))
			if err != nil {
				logger.Error(err)
			}
		} else {
			err := this.client.IncrementValue(cacheKey.Key, uint64(hitsAddend))
			if err != nil {
				logger.Error(err)
			}

			err = this.client.SetExpire(cacheKey.Key, uint64(expirationSeconds))
			if err != nil {
				logger.Error(err)
			}
		}
	}

	return responseDescriptorStatuses
}

// Flush() is a no-op with redis since quota reads and updates happen synchronously.
func (this *fixedRateLimitCacheImpl) Flush() {}

func NewFixedRateLimitCacheImpl(client storage_strategy.StorageStrategy, perSecondClient storage_strategy.StorageStrategy, timeSource utils.TimeSource, jitterRand *rand.Rand, localCache *freecache.Cache, expirationJitterMaxSeconds int64, nearLimitRatio float32, cacheKeyPrefix string, statsManager stats.Manager) limiter.RateLimitCache {
	return &fixedRateLimitCacheImpl{
		client:                     client,
		perSecondClient:            perSecondClient,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		baseRateLimiter:            limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio, cacheKeyPrefix, statsManager),
	}
}
