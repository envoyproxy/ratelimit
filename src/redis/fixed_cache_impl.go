package redis

import (
	"math/rand"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/algorithm"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis/driver"
	"github.com/envoyproxy/ratelimit/src/utils"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

type fixedRateLimitCacheImpl struct {
	client driver.Client
	// Optional Client for a dedicated cache of per second limits.
	// If this client is nil, then the Cache will use the client for all
	// limits regardless of unit. If this client is not nil, then it
	// is used for limits that have a SECOND unit.
	perSecondClient            driver.Client
	timeSource                 utils.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	localCache                 *freecache.Cache
	nearLimitRatio             float32
	algorithm                  *algorithm.WindowImpl
}

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
	results := make([]int64, len(request.Descriptors))
	var pipeline, perSecondPipeline driver.Pipeline

	// Now, actually setup the pipeline, skipping empty cache keys.
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

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
		if this.expirationJitterMaxSeconds > 0 {
			expirationSeconds += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
		}

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = driver.Pipeline{}
			}
			fixedPipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, hitsAddend, &results[i], expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = driver.Pipeline{}
			}
			fixedPipelineAppend(this.client, &pipeline, cacheKey.Key, hitsAddend, &results[i], expirationSeconds)
		}
	}

	if pipeline != nil {
		driver.CheckError(this.client.PipeDo(pipeline))
	}
	if perSecondPipeline != nil {
		driver.CheckError(this.perSecondClient.PipeDo(perSecondPipeline))
	}

	// Now fetch the pipeline.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))

	for i, cacheKey := range cacheKeys {
		responseDescriptorStatuses[i] = this.algorithm.GetResponseDescriptorStatus(cacheKey.Key, limits[i], results[i], isOverLimitWithLocalCache[i], int64(hitsAddend))
	}

	return responseDescriptorStatuses
}

func (this *fixedRateLimitCacheImpl) Flush() {}

func fixedPipelineAppend(client driver.Client, pipeline *driver.Pipeline, key string, hitsAddend int64, result *int64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, result, "INCRBY", key, hitsAddend)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

func NewFixedRateLimitCacheImpl(client driver.Client, perSecondClient driver.Client, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string) limiter.RateLimitCache {
	return &fixedRateLimitCacheImpl{
		client:                     client,
		perSecondClient:            perSecondClient,
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
