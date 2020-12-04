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
	"math"
	"math/rand"
	"strconv"
	"sync"

	"github.com/coocood/freecache"
	stats "github.com/lyft/gostats"

	"github.com/bradfitz/gomemcache/memcache"

	logger "github.com/sirupsen/logrus"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
)

type rateLimitMemcacheImpl struct {
	client                     Client
	timeSource                 utils.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	cacheKeyGenerator          limiter.CacheKeyGenerator
	localCache                 *freecache.Cache
	wg                         sync.WaitGroup
	nearLimitRatio             float32
}

var _ limiter.RateLimitCache = (*rateLimitMemcacheImpl)(nil)

func max(a uint32, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func (this *rateLimitMemcacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := max(1, request.HitsAddend)

	// First build a list of all cache keys that we are actually going to hit. generateCacheKey()
	// returns an empty string in the key if there is no limit so that we can keep the arrays
	// all the same size.
	assert.Assert(len(request.Descriptors) == len(limits))
	cacheKeys := make([]limiter.CacheKey, len(request.Descriptors))
	now := this.timeSource.UnixNow()
	for i := 0; i < len(request.Descriptors); i++ {
		cacheKeys[i] = this.cacheKeyGenerator.GenerateCacheKey(request.Domain, request.Descriptors[i], limits[i], now)

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

		if this.localCache != nil {
			// Get returns the value or not found error.
			_, err := this.localCache.Get([]byte(cacheKey.Key))
			if err == nil {
				isOverLimitWithLocalCache[i] = true
				logger.Debugf("cache key is over the limit: %s", cacheKey.Key)
				continue
			}
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)
		keysToGet = append(keysToGet, cacheKey.Key)
	}

	// Now fetch the pipeline.
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
		if cacheKey.Key == "" {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
					Code:           pb.RateLimitResponse_OK,
					CurrentLimit:   nil,
					LimitRemaining: 0,
				}
			continue
		}

		if isOverLimitWithLocalCache[i] {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
					Code:               pb.RateLimitResponse_OVER_LIMIT,
					CurrentLimit:       limits[i].Limit,
					LimitRemaining:     0,
					DurationUntilReset: utils.CalculateReset(limits[i].Limit, this.timeSource),
				}
			limits[i].Stats.OverLimit.Add(uint64(hitsAddend))
			limits[i].Stats.OverLimitWithLocalCache.Add(uint64(hitsAddend))
			continue
		}

		rawMemcacheValue, ok := memcacheValues[cacheKey.Key]
		var limitBeforeIncrease uint32
		if ok {
			decoded, err := strconv.ParseInt(string(rawMemcacheValue.Value), 10, 32)
			if err != nil {
				logger.Errorf("Unexpected non-numeric value in memcached: %v", rawMemcacheValue)
			} else {
				limitBeforeIncrease = uint32(decoded)
			}

		}

		limitAfterIncrease := limitBeforeIncrease + hitsAddend
		overLimitThreshold := limits[i].Limit.RequestsPerUnit
		// The nearLimitThreshold is the number of requests that can be made before hitting the NearLimitRatio.
		// We need to know it in both the OK and OVER_LIMIT scenarios.
		nearLimitThreshold := uint32(math.Floor(float64(float32(overLimitThreshold) * this.nearLimitRatio)))

		logger.Debugf("cache key: %s current: %d", cacheKey.Key, limitAfterIncrease)
		if limitAfterIncrease > overLimitThreshold {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
					Code:               pb.RateLimitResponse_OVER_LIMIT,
					CurrentLimit:       limits[i].Limit,
					LimitRemaining:     0,
					DurationUntilReset: utils.CalculateReset(limits[i].Limit, this.timeSource),
				}

			// Increase over limit statistics. Because we support += behavior for increasing the limit, we need to
			// assess if the entire hitsAddend were over the limit. That is, if the limit's value before adding the
			// N hits was over the limit, then all the N hits were over limit.
			// Otherwise, only the difference between the current limit value and the over limit threshold
			// were over limit hits.
			if limitBeforeIncrease >= overLimitThreshold {
				limits[i].Stats.OverLimit.Add(uint64(hitsAddend))
			} else {
				limits[i].Stats.OverLimit.Add(uint64(limitAfterIncrease - overLimitThreshold))

				// If the limit before increase was below the over limit value, then some of the hits were
				// in the near limit range.
				limits[i].Stats.NearLimit.Add(uint64(overLimitThreshold - max(nearLimitThreshold, limitBeforeIncrease)))
			}
			if this.localCache != nil {
				// Set the TTL of the local_cache to be the entire duration.
				// Since the cache_key gets changed once the time crosses over current time slot, the over-the-limit
				// cache keys in local_cache lose effectiveness.
				// For example, if we have an hour limit on all mongo connections, the cache key would be
				// similar to mongo_1h, mongo_2h, etc. In the hour 1 (0h0m - 0h59m), the cache key is mongo_1h, we start
				// to get ratelimited in the 50th minute, the ttl of local_cache will be set as 1 hour(0h50m-1h49m).
				// In the time of 1h1m, since the cache key becomes different (mongo_2h), it won't get ratelimited.
				err := this.localCache.Set([]byte(cacheKey.Key), []byte{}, int(utils.UnitToDivider(limits[i].Limit.Unit)))
				if err != nil {
					logger.Errorf("Failing to set local cache key: %s", cacheKey.Key)
				}
			}
		} else {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
					Code:               pb.RateLimitResponse_OK,
					CurrentLimit:       limits[i].Limit,
					LimitRemaining:     overLimitThreshold - limitAfterIncrease,
					DurationUntilReset: utils.CalculateReset(limits[i].Limit, this.timeSource),
				}

			// The limit is OK but we additionally want to know if we are near the limit.
			if limitAfterIncrease > nearLimitThreshold {
				// Here we also need to assess which portion of the hitsAddend were in the near limit range.
				// If all the hits were over the nearLimitThreshold, then all hits are near limit. Otherwise,
				// only the difference between the current limit value and the near limit threshold were near
				// limit hits.
				if limitBeforeIncrease >= nearLimitThreshold {
					limits[i].Stats.NearLimit.Add(uint64(hitsAddend))
				} else {
					limits[i].Stats.NearLimit.Add(uint64(limitAfterIncrease - nearLimitThreshold))
				}
			}
		}
	}

	this.wg.Add(1)
	go this.increaseAsync(cacheKeys, isOverLimitWithLocalCache, limits, uint64(hitsAddend))

	return responseDescriptorStatuses
}

func (this *rateLimitMemcacheImpl) increaseAsync(cacheKeys []limiter.CacheKey, isOverLimitWithLocalCache []bool, limits []*config.RateLimit, hitsAddend uint64) {
	defer this.wg.Done()
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

			// Need to add instead of increment
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

func (this *rateLimitMemcacheImpl) Flush() {
	this.wg.Wait()
}

func NewRateLimitCacheImpl(client Client, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, scope stats.Scope, nearLimitRatio float32) limiter.RateLimitCache {
	return &rateLimitMemcacheImpl{
		client:                     client,
		timeSource:                 timeSource,
		cacheKeyGenerator:          limiter.NewCacheKeyGenerator(),
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
	}
}

func NewRateLimitCacheImplFromSettings(s settings.Settings, timeSource utils.TimeSource, jitterRand *rand.Rand, localCache *freecache.Cache, scope stats.Scope) limiter.RateLimitCache {
	return NewRateLimitCacheImpl(
		memcache.New(s.MemcacheHostPort),
		timeSource,
		jitterRand,
		s.ExpirationJitterMaxSeconds,
		localCache,
		scope,
		s.NearLimitRatio,
	)
}
