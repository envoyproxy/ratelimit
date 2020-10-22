package redis

import (
	"math"
	"math/rand"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/golang/protobuf/ptypes/duration"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

type rateLimitCacheImpl struct {
	client Client
	// Optional Client for a dedicated cache of per second limits.
	// If this client is nil, then the Cache will use the client for all
	// limits regardless of unit. If this client is not nil, then it
	// is used for limits that have a SECOND unit.
	perSecondClient            Client
	timeSource                 limiter.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	cacheKeyGenerator          limiter.CacheKeyGenerator
	localCache                 *freecache.Cache
}

func max(a uint32, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func pipelineAppend(client Client, pipeline *Pipeline, key string, hitsAddend uint32, result *uint32, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, result, "INCRBY", key, hitsAddend)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

func (this *rateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := max(1, request.HitsAddend)

	// First build a list of all cache keys that we are actually going to hit. GenerateCacheKey()
	// returns an empty string in the key if there is no limit so that we can keep the arrays
	// all the same size.
	assert.Assert(len(request.Descriptors) == len(limits))
	cacheKeys := make([]limiter.CacheKey, len(request.Descriptors))
	now := this.timeSource.UnixNow()
	for i := 0; i < len(request.Descriptors); i++ {
		cacheKeys[i] = this.cacheKeyGenerator.GenerateCacheKey(
			request.Domain, request.Descriptors[i], limits[i], now)

		// Increase statistics for limits hit by their respective requests.
		if limits[i] != nil {
			limits[i].Stats.TotalHits.Add(uint64(hitsAddend))
		}
	}

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	results := make([]uint32, len(request.Descriptors))
	var pipeline, perSecondPipeline Pipeline

	// Now, actually setup the pipeline, skipping empty cache keys.
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

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
		if this.expirationJitterMaxSeconds > 0 {
			expirationSeconds += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
		}

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = Pipeline{}
			}
			pipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, hitsAddend, &results[i], expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = Pipeline{}
			}
			pipelineAppend(this.client, &pipeline, cacheKey.Key, hitsAddend, &results[i], expirationSeconds)
		}
	}

	if pipeline != nil {
		checkError(this.client.PipeDo(pipeline))
	}
	if perSecondPipeline != nil {
		checkError(this.perSecondClient.PipeDo(perSecondPipeline))
	}

	// Now fetch the pipeline.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))
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
					DurationUntilReset: CalculateReset(limits[i].Limit, this.timeSource),
				}
			limits[i].Stats.OverLimit.Add(uint64(hitsAddend))
			limits[i].Stats.OverLimitWithLocalCache.Add(uint64(hitsAddend))
			continue
		}

		limitAfterIncrease := results[i]
		limitBeforeIncrease := limitAfterIncrease - hitsAddend
		overLimitThreshold := limits[i].Limit.RequestsPerUnit
		// The nearLimitThreshold is the number of requests that can be made before hitting the NearLimitRatio.
		// We need to know it in both the OK and OVER_LIMIT scenarios.
		nearLimitThreshold := uint32(math.Floor(float64(float32(overLimitThreshold) * config.NearLimitRatio)))

		logger.Debugf("cache key: %s current: %d", cacheKey.Key, limitAfterIncrease)
		if limitAfterIncrease > overLimitThreshold {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
					Code:               pb.RateLimitResponse_OVER_LIMIT,
					CurrentLimit:       limits[i].Limit,
					LimitRemaining:     0,
					DurationUntilReset: CalculateReset(limits[i].Limit, this.timeSource),
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
					DurationUntilReset: CalculateReset(limits[i].Limit, this.timeSource),
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

	return responseDescriptorStatuses
}

func CalculateReset(currentLimit *pb.RateLimitResponse_RateLimit, timeSource limiter.TimeSource) *duration.Duration {
	sec := utils.UnitToDivider(currentLimit.Unit)
	now := timeSource.UnixNow()
	return &duration.Duration{Seconds: sec - now%sec}
}

func NewRateLimitCacheImpl(client Client, perSecondClient Client, timeSource limiter.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache) limiter.RateLimitCache {
	return &rateLimitCacheImpl{
		client:                     client,
		perSecondClient:            perSecondClient,
		timeSource:                 timeSource,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		cacheKeyGenerator:          limiter.NewCacheKeyGenerator(),
		localCache:                 localCache,
	}
}

func NewRateLimiterCacheImplFromSettings(s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource limiter.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64) limiter.RateLimitCache {
	var perSecondPool Client
	if s.RedisPerSecond {
		perSecondPool = NewClientImpl(srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisPipelineWindow, s.RedisPipelineLimit)
	}
	var otherPool Client
	otherPool = NewClientImpl(srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisPipelineWindow, s.RedisPipelineLimit)

	return NewRateLimitCacheImpl(
		otherPool,
		perSecondPool,
		timeSource,
		jitterRand,
		expirationJitterMaxSeconds,
		localCache)
}
