package memcached

import (
	"context"
	"math"
	"math/rand"
	"strconv"
	"sync"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/utils"
	stats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

type windowedRateLimitCacheImpl struct {
	client                     Client
	timeSource                 utils.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	cacheKeyGenerator          limiter.CacheKeyGenerator
	localCache                 *freecache.Cache
	waitGroup                  sync.WaitGroup
	nearLimitRatio             float32
	baseRateLimiter            *limiter.BaseRateLimiter
}

var _ limiter.RateLimitCache = (*windowedRateLimitCacheImpl)(nil)

func (this *windowedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := utils.MaxUint32(1, request.HitsAddend)

	// First build a list of all cache keys that we are actually going to hit.
	assert.Assert(len(request.Descriptors) == len(limits))
	cacheKeys := make([]limiter.CacheKey, len(request.Descriptors))
	for i := 0; i < len(request.Descriptors); i++ {
		cacheKeys[i] = this.cacheKeyGenerator.GenerateCacheKey(
			request.Domain, request.Descriptors[i], limits[i], 0)

		// Increase statistics for limits hit by their respective requests.
		if limits[i] != nil {
			limits[i].Stats.TotalHits.Add(uint64(hitsAddend))
		}
	}

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	keysToGet := make([]string, 0, len(request.Descriptors))

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

	newTats := make([]int64, len(cacheKeys))
	expirationSeconds := make([]int64, len(cacheKeys))
	isOverLimit := make([]bool, len(cacheKeys))
	now := this.timeSource.UnixNanoNow()

	for i, cacheKey := range cacheKeys {

		rawMemcacheValue, ok := memcacheValues[cacheKey.Key]
		var tat int64
		if ok {
			tat, err = strconv.ParseInt(string(rawMemcacheValue.Value), 10, 64)
			if err != nil {
				logger.Errorf("Unexpected non-numeric value in memcached: %v", rawMemcacheValue)
			}
		} else {
			tat = now
		}

		limit := int64(limits[i].Limit.RequestsPerUnit)
		period := utils.SecondsToNanoseconds(utils.UnitToDivider(limits[i].Limit.Unit))
		quantity := int64(hitsAddend)
		arrivedAt := now

		emissionInterval := period / limit
		tat = utils.MaxInt64(tat, arrivedAt)
		newTats[i] = tat + emissionInterval*quantity
		allowAt := newTats[i] - period
		diff := arrivedAt - allowAt

		previousAllowAt := tat - period
		previousLimitRemaining := int64(math.Ceil(float64((arrivedAt - previousAllowAt) / emissionInterval)))
		previousLimitRemaining = utils.MaxInt64(previousLimitRemaining, 0)
		nearLimitWindow := int64(math.Ceil(float64(float32(limits[i].Limit.RequestsPerUnit) * (1.0 - this.nearLimitRatio))))
		limitRemaining := int64(math.Ceil(float64(diff / emissionInterval)))

		expirationSeconds[i] = utils.NanosecondsToSeconds(newTats[i]-arrivedAt) + 1
		if this.expirationJitterMaxSeconds > 0 {
			expirationSeconds[i] += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
		}

		if diff < 0 {
			isOverLimit[i] = true
			responseDescriptorStatuses[i] = &pb.RateLimitResponse_DescriptorStatus{
				Code:               pb.RateLimitResponse_OVER_LIMIT,
				CurrentLimit:       limits[i].Limit,
				LimitRemaining:     0,
				DurationUntilReset: utils.NanosecondsToDuration(int64(math.Ceil(float64(tat - arrivedAt)))),
			}

			limits[i].Stats.OverLimit.Add(uint64(quantity - previousLimitRemaining))
			limits[i].Stats.NearLimit.Add(uint64(utils.MinInt64(previousLimitRemaining, nearLimitWindow)))

			if this.localCache != nil {
				err := this.localCache.Set([]byte(cacheKey.Key), []byte{}, int(utils.NanosecondsToSeconds(-diff)))
				if err != nil {
					logger.Errorf("Failing to set local cache key: %s", cacheKey.Key)
				}
			}
			continue
		} else {
			isOverLimit[i] = false
			responseDescriptorStatuses[i] = &pb.RateLimitResponse_DescriptorStatus{
				Code:               pb.RateLimitResponse_OK,
				CurrentLimit:       limits[i].Limit,
				LimitRemaining:     uint32(limitRemaining),
				DurationUntilReset: utils.NanosecondsToDuration(newTats[i] - arrivedAt),
			}

			hitNearLimit := quantity - (utils.MaxInt64(previousLimitRemaining, nearLimitWindow) - nearLimitWindow)
			if hitNearLimit > 0 {
				limits[i].Stats.NearLimit.Add(uint64(hitNearLimit))
			}
		}
	}

	this.waitGroup.Add(1)
	go this.increaseAsync(isOverLimitWithLocalCache, isOverLimit, cacheKeys, expirationSeconds, newTats)

	return responseDescriptorStatuses
}

func (this *windowedRateLimitCacheImpl) increaseAsync(isOverLimitWithLocalCache []bool, isOverLimit []bool, cacheKeys []limiter.CacheKey, expirationSeconds []int64, newTats []int64) {
	defer this.waitGroup.Done()
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || isOverLimitWithLocalCache[i] || isOverLimit[i] {
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

func NewWindowedRateLimitCacheImpl(client Client, timeSource utils.TimeSource, jitterRand *rand.Rand,
	expirationJitterMaxSeconds int64, localCache *freecache.Cache, scope stats.Scope, nearLimitRatio float32) limiter.RateLimitCache {
	return &windowedRateLimitCacheImpl{
		client:                     client,
		timeSource:                 timeSource,
		cacheKeyGenerator:          limiter.NewCacheKeyGenerator(),
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
		baseRateLimiter:            limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio),
	}
}
