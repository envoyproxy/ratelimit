package redis

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/mediocregopher/radix/v4"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/utils"
)

var script = `local expires_at = tonumber(redis.call("get", ARGV[2]))

if not expires_at or expires_at < tonumber(ARGV[4]) then
	-- this is either a brand new window,
	-- or this window has closed, but redis hasn't cleaned up the key yet
	-- (redis will clean it up in one more second)
	-- initialize a new rate limit window
	redis.call("set", ARGV[1], 0)
	redis.call("set", ARGV[2], ARGV[3])
	-- tell Redis to clean this up _one second after_ the expires_at time (clock differences).
	-- (Redis will only clean up these keys long after the window has passed)
	redis.call("expireat", ARGV[1], ARGV[3] + 1)
	redis.call("expireat", ARGV[2], ARGV[3] + 1)
	-- since the database was updated, return the new value
	expires_at = ARGV[3]
end

-- now that the window either already exists or it was freshly initialized,
-- increment the counter("incrby" returns a number)
local current = redis.call("incrby", ARGV[1], ARGV[5])

return { current, expires_at }`

var evalScript = radix.NewEvalScript(script)

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

func pipelineAppendScript(client Client, pipeline *Pipeline, key string, hitsAddend uint32, expirationTime, currentTime int64, result *[]int64) {
	*pipeline = client.PipeScriptAppend(*pipeline, result, evalScript,
		key,
		fmt.Sprintf("%s:expires", key),
		strconv.FormatInt(expirationTime, 10),
		strconv.FormatInt(currentTime, 10),
		strconv.FormatInt(int64(hitsAddend), 10))
}

func pipelineAppendtoGet(client Client, pipeline *Pipeline, key string, result *uint32) {
	*pipeline = client.PipeAppend(*pipeline, result, "GET", key)
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
	results := make([][]int64, len(request.Descriptors))
	for i := range results {
		results[i] = make([]int64, 2)
	}
	currentCount := make([]uint32, len(request.Descriptors))
	var pipeline, perSecondPipeline, pipelineToGet, perSecondPipelineToGet Pipeline

	hitsAddendForRedis := hitsAddend
	overlimitIndexes := make([]bool, len(request.Descriptors))
	nearlimitIndexes := make([]bool, len(request.Descriptors))
	isCacheKeyOverlimit := false

	if this.stopCacheKeyIncrementWhenOverlimit {
		// Check if any of the keys are reaching to the over limit in redis cache.
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
				isOverLimitWithLocalCache[i] = true
				hitsAddendForRedis = 0
				overlimitIndexes[i] = true
				isCacheKeyOverlimit = true
				continue
			} else {
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
		}

		// Only if none of the cache keys exceed the limit, call Redis to check whether the cache keys are becoming overlimited.
		if len(cacheKeys) > 1 && !isCacheKeyOverlimit {
			if pipelineToGet != nil {
				checkError(this.client.PipeDo(ctx, pipelineToGet))
			}
			if perSecondPipelineToGet != nil {
				checkError(this.perSecondClient.PipeDo(ctx, perSecondPipelineToGet))
			}

			for i, cacheKey := range cacheKeys {
				if cacheKey.Key == "" {
					continue
				}
				// Now fetch the pipeline.
				limitBeforeIncrease := currentCount[i]
				limitAfterIncrease := limitBeforeIncrease + hitsAddend

				limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

				if this.baseRateLimiter.IsOverLimitThresholdReached(limitInfo) {
					hitsAddendForRedis = 0
					nearlimitIndexes[i] = true
				}
			}
		}
	} else {
		// Check if any of the keys are reaching to the over limit in redis cache.
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
				isOverLimitWithLocalCache[i] = true
				overlimitIndexes[i] = true
				continue
			}
		}
	}

	// Now, actually setup the pipeline, skipping empty cache keys.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || overlimitIndexes[i] {
			continue
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
		if this.baseRateLimiter.ExpirationJitterMaxSeconds > 0 {
			expirationSeconds += this.baseRateLimiter.JitterRand.Int63n(this.baseRateLimiter.ExpirationJitterMaxSeconds)
		}

		unixTime := this.baseRateLimiter.TimeSource.UnixNow()
		expirationTime := time.Unix(unixTime, 0).Add(time.Duration(expirationSeconds * int64(time.Second))).Unix()
		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = Pipeline{}
			}
			if nearlimitIndexes[i] {
				pipelineAppendScript(this.perSecondClient, &perSecondPipeline, cacheKey.Key, hitsAddend, expirationTime, unixTime, &results[i])
			} else {
				pipelineAppendScript(this.perSecondClient, &perSecondPipeline, cacheKey.Key, hitsAddendForRedis, expirationTime, unixTime, &results[i])
			}
		} else {
			if pipeline == nil {
				pipeline = Pipeline{}
			}
			if nearlimitIndexes[i] {
				pipelineAppendScript(this.client, &pipeline, cacheKey.Key, hitsAddend, expirationTime, unixTime, &results[i])
			} else {
				pipelineAppendScript(this.client, &pipeline, cacheKey.Key, hitsAddendForRedis, expirationTime, unixTime, &results[i])
			}
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
		checkError(this.client.PipeDo(ctx, pipeline))
	}
	if perSecondPipeline != nil {
		checkError(this.perSecondClient.PipeDo(ctx, perSecondPipeline))
	}

	// Now fetch the pipeline.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))
	for i, cacheKey := range cacheKeys {

		limitAfterIncrease := uint32(results[i][0])
		limitBeforeIncrease := limitAfterIncrease - hitsAddend

		limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

		responseDescriptorStatuses[i] = this.baseRateLimiter.GetResponseDescriptorStatus(cacheKey.Key,
			limitInfo, isOverLimitWithLocalCache[i], hitsAddend)

	}

	return responseDescriptorStatuses
}

// Flush() is a no-op with redis since quota reads and updates happen synchronously.
func (this *fixedRateLimitCacheImpl) Flush() {}

func NewFixedRateLimitCacheImpl(client Client, perSecondClient Client, timeSource utils.TimeSource,
	jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string, statsManager stats.Manager,
	stopCacheKeyIncrementWhenOverlimit bool) limiter.RateLimitCache {
	return &fixedRateLimitCacheImpl{
		client:                             client,
		perSecondClient:                    perSecondClient,
		stopCacheKeyIncrementWhenOverlimit: stopCacheKeyIncrementWhenOverlimit,
		baseRateLimiter:                    limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio, cacheKeyPrefix, statsManager),
	}
}
