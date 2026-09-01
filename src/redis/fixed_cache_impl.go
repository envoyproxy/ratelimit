package redis

import (
	"math/rand"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ratelimit/src/stats"

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
	// Refunds crossing back under the limit publish the key on the local
	// cache invalidation channel (main client only).
	publishInvalidations bool
	baseRateLimiter      *limiter.BaseRateLimiter
}

func pipelineAppend(client Client, pipeline *Pipeline, key string, hitsAddend uint64, result *uint64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, result, "INCRBY", key, hitsAddend)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

// DecrementScript atomically decrements a rate limit counter, floored at 0;
// a missing key returns 0 without being created. ARGV: hits, expiration
// seconds, '1'/'0' publish flag, requests per unit, invalidation channel.
//
// The key is published only when the refund crosses back under the limit —
// one message per poisoning cycle. Command order is load-bearing: Lua does
// not roll back on runtime errors, so PUBLISH must fail before the counter
// is mutated and the mutation must be a single SET..EX, or an ACL denial
// leaves a half-applied refund behind an errored RPC.
const DecrementScript = `
local current = redis.call('GET', KEYS[1])
if current == false then return 0 end
local old = tonumber(current)
local new_val = math.floor(math.max(0, old - tonumber(ARGV[1])))
if ARGV[3] == '1' then
  local limit = tonumber(ARGV[4])
  if old > limit and new_val <= limit then
    redis.call('PUBLISH', ARGV[5], KEYS[1])
  end
end
redis.call('SET', KEYS[1], tostring(new_val), 'EX', tonumber(ARGV[2]))
return new_val
`

func pipelineAppendDecrement(client Client, pipeline *Pipeline, key string, hitsAddend uint64, result *uint64,
	expirationSeconds int64, publishInvalidation bool, requestsPerUnit uint32,
) {
	publishFlag := "0"
	if publishInvalidation {
		publishFlag = "1"
	}
	// EVAL's first positional argument is the script body, not the key, so the
	// real cache key must be passed explicitly as the routing key. Otherwise, in
	// Redis Cluster mode the command would be routed using the script text,
	// causing MOVED/CROSSSLOT errors or misrouting.
	*pipeline = client.PipeAppendWithRoutingKey(*pipeline, key, result, "EVAL", DecrementScript, 1, key,
		hitsAddend, expirationSeconds, publishFlag, requestsPerUnit, LocalCacheInvalidationChannel)
}

func (this *fixedRateLimitCacheImpl) selectPipeline(cacheKey limiter.CacheKey, pipeline *Pipeline, perSecondPipeline *Pipeline) (client Client, p *Pipeline, onPerSecondRedis bool) {
	if this.perSecondClient != nil && cacheKey.PerSecond {
		if *perSecondPipeline == nil {
			*perSecondPipeline = Pipeline{}
		}
		return this.perSecondClient, perSecondPipeline, true
	}
	if *pipeline == nil {
		*pipeline = Pipeline{}
	}
	return this.client, pipeline, false
}

func pipelineAppendtoGet(client Client, pipeline *Pipeline, key string, result *uint64) {
	*pipeline = client.PipeAppend(*pipeline, result, "GET", key)
}

func (this *fixedRateLimitCacheImpl) getHitsAddendValue(hitsAddend uint64, isCacheKeyOverlimit, isCacheKeyNearlimit,
	isNearLimit bool,
) uint64 {
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
	if isNearLimit {
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

	// pinning before any Redis read so a racing invalidation suppresses stale inserts to the local cache
	localCacheGen := this.baseRateLimiter.GetLocalCacheGenSnapshot()

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
	// Negative hits (decrements) skip this check — they always proceed.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || hitsAddends[i].IsNegative {
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
			if cacheKey.Key == "" || hitsAddends[i].IsNegative {
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
			checkError(this.client.PipeDo(ctx, pipelineToGet))
		}
		if perSecondPipelineToGet != nil {
			checkError(this.perSecondClient.PipeDo(ctx, perSecondPipelineToGet))
		}

		for i, cacheKey := range cacheKeys {
			if cacheKey.Key == "" || hitsAddends[i].IsNegative {
				continue
			}
			// Now fetch the pipeline.
			limitBeforeIncrease := currentCount[i]
			limitAfterIncrease := limitBeforeIncrease + hitsAddends[i].Value

			limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

			if this.baseRateLimiter.IsOverLimitThresholdReached(limitInfo) {
				nearlimitIndexes[i] = true
				isCacheKeyNearlimit = true
			}
		}
	}

	// Now, actually setup the pipeline to increase/decrease the usage of cache key, skipping empty cache keys.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || overlimitIndexes[i] {
			continue
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)

		expirationSeconds := this.baseRateLimiter.ExpirationSeconds(limits[i].Limit.Unit)
		if this.baseRateLimiter.ExpirationJitterMaxSeconds > 0 {
			expirationSeconds += this.baseRateLimiter.JitterRand.Int63n(this.baseRateLimiter.ExpirationJitterMaxSeconds)
		}

		client, p, onPerSecondRedis := this.selectPipeline(cacheKey, &pipeline, &perSecondPipeline)
		if hitsAddends[i].IsNegative {
			// The subscriber listens only on the main Redis.
			publishInvalidation := this.publishInvalidations && !onPerSecondRedis
			pipelineAppendDecrement(client, p, cacheKey.Key, hitsAddends[i].Value, &results[i], expirationSeconds,
				publishInvalidation, limits[i].Limit.RequestsPerUnit)
		} else {
			pipelineAppend(client, p, cacheKey.Key, this.getHitsAddendValue(hitsAddends[i].Value,
				isCacheKeyOverlimit, isCacheKeyNearlimit, nearlimitIndexes[i]), &results[i], expirationSeconds)
		}
	}

	// Generate trace
	_, span := tracer.Start(
		ctx, "Redis Pipeline Execution",
		trace.WithAttributes(
			attribute.Int("pipeline length", len(pipeline)),
			attribute.Int("perSecondPipeline length", len(perSecondPipeline)),
		),
	)
	defer span.End()

	if pipeline != nil {
		checkError(this.client.PipeDo(ctx, pipeline))
	}
	if perSecondPipeline != nil {
		checkError(this.perSecondClient.PipeDo(ctx, perSecondPipeline))
	}

	// Now fetch the pipeline.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))
	for i, cacheKey := range cacheKeys {
		if hitsAddends[i].IsNegative {
			// Negative hits (refunds) always return OK with the remaining capacity,
			// even if the post-decrement counter is still above the limit.
			responseDescriptorStatuses[i] = this.baseRateLimiter.GetResponseDescriptorStatusForNegativeHits(
				cacheKey.Key, limits[i], results[i])
		} else {
			limitAfterIncrease := results[i]
			limitBeforeIncrease := limitAfterIncrease - hitsAddends[i].Value

			limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

			responseDescriptorStatuses[i] = this.baseRateLimiter.GetResponseDescriptorStatus(cacheKey.Key,
				limitInfo, isOverLimitWithLocalCache[i], hitsAddends[i].Value, localCacheGen)
		}
	}

	return responseDescriptorStatuses
}

// Flush() is a no-op with redis since quota reads and updates happen synchronously.
func (this *fixedRateLimitCacheImpl) Flush() {}

func NewFixedRateLimitCacheImpl(client Client, perSecondClient Client, timeSource utils.TimeSource,
	jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *limiter.LocalCacheGuard, nearLimitRatio float32, cacheKeyPrefix string, statsManager stats.Manager,
	stopCacheKeyIncrementWhenOverlimit bool, useCalendarMonth bool, publishInvalidations bool,
) limiter.RateLimitCache {
	return &fixedRateLimitCacheImpl{
		client:                             client,
		perSecondClient:                    perSecondClient,
		stopCacheKeyIncrementWhenOverlimit: stopCacheKeyIncrementWhenOverlimit,
		publishInvalidations:               publishInvalidations,
		baseRateLimiter:                    limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio, cacheKeyPrefix, statsManager, useCalendarMonth),
	}
}
