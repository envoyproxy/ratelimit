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

type windowedRateLimitCacheImpl struct {
	client                     driver.Client
	timeSource                 utils.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	localCache                 *freecache.Cache
	waitGroup                  sync.WaitGroup
	nearLimitRatio             float32
	algorithm                  *algorithm.WindowImpl
}

var _ limiter.RateLimitCache = (*windowedRateLimitCacheImpl)(nil)

const DummyCacheKeyTime = 0

func (this *windowedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := utils.MaxInt64(1, int64(request.HitsAddend))

	// First build a list of all cache keys that we are actually going to hit.
	cacheKeys := this.algorithm.GenerateCacheKeys(request, limits, hitsAddend, DummyCacheKeyTime)

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

	// Now fetch from memcached.
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

	newTats := make([]int64, len(cacheKeys))
	isOverLimit := make([]bool, len(cacheKeys))
	expirationSeconds := make([]int64, len(cacheKeys))

	for i, cacheKey := range cacheKeys {
		rawMemcacheValue, ok := memcacheValues[cacheKey.Key]
		var tat int64
		if ok {
			tat, err = strconv.ParseInt(string(rawMemcacheValue.Value), 10, 64)
			if err != nil {
				logger.Errorf("Unexpected non-numeric value in memcached: %v", rawMemcacheValue)
			}
		}

		responseDescriptorStatuses[i] = this.algorithm.GetResponseDescriptorStatus(cacheKey.Key, limits[i], tat, isOverLimitWithLocalCache[i], int64(hitsAddend))

		if responseDescriptorStatuses[i].Code == pb.RateLimitResponse_OVER_LIMIT {
			isOverLimit[i] = true
		} else {
			isOverLimit[i] = false
		}

		newTats[i] = this.algorithm.GetResultsAfterIncrease()
		expirationSeconds[i] = this.algorithm.GetExpirationSeconds()
		if this.expirationJitterMaxSeconds > 0 {
			expirationSeconds[i] += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
		}
	}

	this.waitGroup.Add(1)
	go this.increaseAsync(isOverLimitWithLocalCache, isOverLimit, cacheKeys, expirationSeconds, newTats)

	if AutoFlushForIntegrationTests {
		this.Flush()
	}

	return responseDescriptorStatuses
}

func (this *windowedRateLimitCacheImpl) increaseAsync(isOverLimitWithLocalCache []bool, isOverLimit []bool, cacheKeys []utils.CacheKey, expirationSeconds []int64, newTats []int64) {
	defer this.waitGroup.Done()
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || isOverLimitWithLocalCache[i] {
			continue
		}

		err := this.client.Set(&memcache.Item{
			Key:        cacheKey.Key,
			Value:      []byte(strconv.FormatInt(newTats[i], 10)),
			Expiration: int32(expirationSeconds[i]),
		})

		if err != nil {
			logger.Errorf("Failed to set key %s: %s", cacheKey.Key, err)
			continue
		}
	}
}

func (this *windowedRateLimitCacheImpl) Flush() {
	this.waitGroup.Wait()
}

func NewWindowedRateLimitCacheImpl(client driver.Client, timeSource utils.TimeSource, jitterRand *rand.Rand,
	expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string) limiter.RateLimitCache {
	return &windowedRateLimitCacheImpl{
		client:                     client,
		timeSource:                 timeSource,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
		algorithm: algorithm.NewWindow(
			algorithm.NewRollingWindowAlgorithm(timeSource, localCache, nearLimitRatio, cacheKeyPrefix),
			cacheKeyPrefix,
			localCache,
			timeSource,
		),
	}
}
