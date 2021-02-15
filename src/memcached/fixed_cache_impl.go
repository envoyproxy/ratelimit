package memcached

import (
	"context"
	"math/rand"
	"strconv"
	"sync"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/algorithm"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/memcached/driver"
	"github.com/envoyproxy/ratelimit/src/utils"
	logger "github.com/sirupsen/logrus"
)

type fixedRateLimitCacheImpl struct {
	client                     driver.Client
	timeSource                 utils.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	localCache                 *freecache.Cache
	waitGroup                  sync.WaitGroup
	nearLimitRatio             float32
	algorithm                  *algorithm.WindowImpl
}

var _ limiter.RateLimitCache = (*fixedRateLimitCacheImpl)(nil)

func (this *fixedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := utils.MaxInt64(1, int64(request.HitsAddend))

	// First build a list of all cache keys that we are actually going to hit.
	cacheKeys := this.algorithm.GenerateCacheKeys(request, limits, hitsAddend, this.timeSource.UnixNow())

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	keysToGet := make([]string, 0, len(request.Descriptors))

	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" {
			continue
		}

		// Check if key is over the limit in local cache.
		if this.algorithm.IsOverLimitWithLocalCache(cacheKey.Key) {
			isOverLimitWithLocalCache[i] = true
			logger.Debugf("cache key is over the limit: %s", cacheKey.Key)
			continue
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)
		keysToGet = append(keysToGet, cacheKey.Key)
	}

	// Now fetch from memcache.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))

	var memcacheValues map[string]*memcache.Item
	var err error

	if len(keysToGet) > 0 {
		memcacheValues, err = this.client.GetMulti(keysToGet)
		if err != nil {
			logger.Errorf("Error multi-getting memcache keys (%s): %s", keysToGet, err)
		}
	}

	for i, cacheKey := range cacheKeys {
		rawMemcacheValue, ok := memcacheValues[cacheKey.Key]
		var result int64
		if ok {
			decoded, err := strconv.ParseInt(string(rawMemcacheValue.Value), 10, 32)
			if err != nil {
				logger.Errorf("Unexpected non-numeric value in memcached: %v", rawMemcacheValue)
			} else {
				result = decoded
			}
		}

		resultAfterIncrease := result + hitsAddend
		responseDescriptorStatuses[i] = this.algorithm.GetResponseDescriptorStatus(cacheKey.Key, limits[i], resultAfterIncrease, isOverLimitWithLocalCache[i], int64(hitsAddend))
	}

	this.waitGroup.Add(1)
	go this.increaseAsync(cacheKeys, isOverLimitWithLocalCache, limits, uint64(hitsAddend))

	if AutoFlushForIntegrationTests {
		this.Flush()
	}

	return responseDescriptorStatuses
}

func (this *fixedRateLimitCacheImpl) increaseAsync(cacheKeys []utils.CacheKey, isOverLimitWithLocalCache []bool,
	limits []*config.RateLimit, hitsAddend uint64) {
	defer this.waitGroup.Done()
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || isOverLimitWithLocalCache[i] {
			continue
		}

		_, err := this.client.Increment(cacheKey.Key, hitsAddend)
		if err == memcache.ErrCacheMiss {
			expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
			if this.expirationJitterMaxSeconds > 0 {
				expirationSeconds += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
			}

			// Need to add instead of increment.
			err = this.client.Add(&memcache.Item{
				Key:        cacheKey.Key,
				Value:      []byte(strconv.FormatUint(hitsAddend, 10)),
				Expiration: int32(expirationSeconds),
			})

			if err == memcache.ErrNotStored {
				// There was a race condition to do this add. We should be able to increment
				// now instead.
				_, err := this.client.Increment(cacheKey.Key, hitsAddend)
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

func (this *fixedRateLimitCacheImpl) Flush() {
	this.waitGroup.Wait()
}

func NewFixedRateLimitCacheImpl(client driver.Client, timeSource utils.TimeSource, jitterRand *rand.Rand,
	expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string) limiter.RateLimitCache {
	return &fixedRateLimitCacheImpl{
		client:                     client,
		timeSource:                 timeSource,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
		algorithm: algorithm.NewWindow(
			algorithm.NewFixedWindowAlgorithm(timeSource, localCache, nearLimitRatio, cacheKeyPrefix),
			cacheKeyPrefix,
			localCache,
			timeSource,
		),
	}
}
