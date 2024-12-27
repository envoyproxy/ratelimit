package redis

import (
	"math/rand"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/utils"
)

var tracer = otel.Tracer("redis.fixedCacheImpl")

type fixedRateLimitCacheImpl struct {
	client Client
	// Optional Client for a dedicated cache of per second limits.
	// If this client is nil, then the Cache will use the client for all
	// limits regardless of unit. If this client is not nil, then it
	// is used for limits that have a SECOND unit.
	perSecondClient                    Client
	stopCacheKeyIncrementWhenOverlimit bool
	baseRateLimiter                    *limiter.BaseRateLimiter
}

func pipelineAppend(client Client, pipeline *Pipeline, key string, hitsAddend uint64, result *uint64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, result, "INCRBY", key, hitsAddend)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

func pipelineAppendtoGet(client Client, pipeline *Pipeline, key string, result *uint64) {
	*pipeline = client.PipeAppend(*pipeline, result, "GET", key)
}

func (this *fixedRateLimitCacheImpl) getHitsAddend(hitsAddend uint64, isCacheKeyOverlimit, isCacheKeyNearlimit,
	isNearLimt bool) uint64 {
	// If stopCacheKeyIncrementWhenOverlimit is false, then we always increment the cache key.
	if !this.stopCacheKeyIncrementWhenOverlimit {
		return hitsAddend
	}

	// If stopCacheKeyIncrementWhenOverlimit is true, and one of the keys is over limit, then
	// we do not increment the cache key.
	if isCacheKeyOverlimit {
		return 0
	}

	// If stopCacheKeyIncrementWhenOverlimit is true, and none of the keys are over limit, then
	// to check if any of the keys are near limit. If none of the keys are near limit,
	// then we increment the cache key.
	if !isCacheKeyNearlimit {
		return hitsAddend
	}

	// If stopCacheKeyIncrementWhenOverlimit is true, and some of the keys are near limit, then
	// we only increment the cache key if the key is near limit.
	if isNearLimt {
		return hitsAddend
	}

	return 0
}

func (this *fixedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit,
) []*pb.RateLimitResponse_DescriptorStatus {
	logger.Debugf("starting cache lookup")

	hitsAddends := utils.GetHitsAddends(request)

	// First build a list of all cache keys that we are actually going to hit.
	cacheKeys := this.baseRateLimiter.GenerateCacheKeys(request, limits, hitsAddends)

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	results := make([]uint64, len(request.Descriptors))
	currentCount := make([]uint64, len(request.Descriptors))
	var pipeline, perSecondPipeline, pipelineToGet, perSecondPipelineToGet Pipeline

	overlimitIndexes := make([]bool, len(request.Descriptors))
	nearlimitIndexes := make([]bool, len(request.Descriptors))
	isCacheKeyOverlimit := false
	isCacheKeyNearlimit := false

	// Check if any of the keys are already to the over limit in cache.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" {
			continue
		}

		// Check if key is over the limit in local cache.
		if this.baseRateLimiter.IsOverLimitWithLocalCache(cacheKey.Key) {
			if limits[i].ShadowMode {
				logger.Debugf("Cache key %s would be rate limited but shadow mode is enabled on this rule", cacheKey.Key)
			} else {
				logger.Debugf("cache key is over the limit: %s", cacheKey.Key)
			}
			isCacheKeyOverlimit = true
			isOverLimitWithLocalCache[i] = true
			overlimitIndexes[i] = true
		}
	}

	// If none of the keys are over limit in local cache and the stopCacheKeyIncrementWhenOverlimit is true,
	// then we check if any of the keys are near limit in redis cache.
	if this.stopCacheKeyIncrementWhenOverlimit && !isCacheKeyOverlimit {
		for i, cacheKey := range cacheKeys {
			if cacheKey.Key == "" {
				continue
			}

			if this.perSecondClient != nil && cacheKey.PerSecond {
				if perSecondPipelineToGet == nil {
					perSecondPipelineToGet = Pipeline{}
				}
				pipelineAppendtoGet(this.perSecondClient, &perSecondPipelineToGet, cacheKey.Key, &currentCount[i])
			} else {
				if pipelineToGet == nil {
					pipelineToGet = Pipeline{}
				}
				pipelineAppendtoGet(this.client, &pipelineToGet, cacheKey.Key, &currentCount[i])
			}
		}

		if pipelineToGet != nil {
			checkError(this.client.PipeDo(pipelineToGet))
		}
		if perSecondPipelineToGet != nil {
			checkError(this.perSecondClient.PipeDo(perSecondPipelineToGet))
		}

		for i, cacheKey := range cacheKeys {
			if cacheKey.Key == "" {
				continue
			}
			// Now fetch the pipeline.
			limitBeforeIncrease := currentCount[i]
			limitAfterIncrease := limitBeforeIncrease + hitsAddends[i]

			limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

			if this.baseRateLimiter.IsOverLimitThresholdReached(limitInfo) {
				nearlimitIndexes[i] = true
				isCacheKeyNearlimit = true
			}
		}
	}

	// Now, actually setup the pipeline to increase the usage of cache key, skipping empty cache keys.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || overlimitIndexes[i] {
			continue
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
		if this.baseRateLimiter.ExpirationJitterMaxSeconds > 0 {
			expirationSeconds += this.baseRateLimiter.JitterRand.Int63n(this.baseRateLimiter.ExpirationJitterMaxSeconds)
		}

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = Pipeline{}
			}
			pipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, this.getHitsAddend(hitsAddends[i],
				isCacheKeyOverlimit, isCacheKeyNearlimit, nearlimitIndexes[i]), &results[i], expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = Pipeline{}
			}
			pipelineAppend(this.client, &pipeline, cacheKey.Key, this.getHitsAddend(hitsAddends[i], isCacheKeyOverlimit,
				isCacheKeyNearlimit, nearlimitIndexes[i]), &results[i], expirationSeconds)
		}
	}

	// Generate trace
	_, span := tracer.Start(ctx, "Redis Pipeline Execution",
		trace.WithAttributes(
			attribute.Int("pipeline length", len(pipeline)),
			attribute.Int("perSecondPipeline length", len(perSecondPipeline)),
		),
	)
	defer span.End()

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

		limitAfterIncrease := results[i]
		limitBeforeIncrease := limitAfterIncrease - hitsAddends[i]

		limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

		responseDescriptorStatuses[i] = this.baseRateLimiter.GetResponseDescriptorStatus(cacheKey.Key,
			limitInfo, isOverLimitWithLocalCache[i], hitsAddends[i])

	}

	return responseDescriptorStatuses
}

// Flush() is a no-op with redis since quota reads and updates happen synchronously.
func (this *fixedRateLimitCacheImpl) Flush() {}

func NewFixedRateLimitCacheImpl(client Client, perSecondClient Client, timeSource utils.TimeSource,
	jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string, statsManager stats.Manager,
	stopCacheKeyIncrementWhenOverlimit bool,
) limiter.RateLimitCache {
	return &fixedRateLimitCacheImpl{
		client:                             client,
		perSecondClient:                    perSecondClient,
		stopCacheKeyIncrementWhenOverlimit: stopCacheKeyIncrementWhenOverlimit,
		baseRateLimiter:                    limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio, cacheKeyPrefix, statsManager),
	}
}
