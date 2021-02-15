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

// This rolling window limit implemented using Generic Cell Rate Algorithm (GCRA)
// GCRA works by tracking remaining limit through a time called the “theoretical arrival time” (TAT).
// Request cost is represented as a multiplier of “emission interval”, which is derived from the duration of equally spread request.
// TAT is seeded by the current request arrival if not set then add the request costs.
// Subtract the window duration from TAT to get the time to allow a request
// Requests are allowed if the time to allow a request is in the past
// Store the TAT for next process
// https://blog.ian.stapletoncordas.co/2018/12/understanding-generic-cell-rate-limiting.html

type windowedRateLimitCacheImpl struct {
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

const DummyCacheKeyTime = 0

func (this *windowedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting windowed cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := utils.MaxInt64(1, int64(request.HitsAddend))

	// First build a list of all cache keys that we are actually going to hit.
	cacheKeys := this.algorithm.GenerateCacheKeys(request, limits, hitsAddend, DummyCacheKeyTime)

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	tats := make([]int64, len(request.Descriptors))
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

		logger.Debugf("looking up tat for cache key: %s", cacheKey.Key)

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = driver.Pipeline{}
			}
			windowedPipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, &tats[i], expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = driver.Pipeline{}
			}
			windowedPipelineAppend(this.client, &pipeline, cacheKey.Key, &tats[i], expirationSeconds)
		}
	}

	if pipeline != nil {
		driver.CheckError(this.client.PipeDo(pipeline))
		pipeline = nil
	}
	if perSecondPipeline != nil {
		driver.CheckError(this.perSecondClient.PipeDo(perSecondPipeline))
		perSecondPipeline = nil
	}

	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus, len(request.Descriptors))

	for i, cacheKey := range cacheKeys {
		responseDescriptorStatuses[i] = this.algorithm.GetResponseDescriptorStatus(cacheKey.Key, limits[i], int64(tats[i]), isOverLimitWithLocalCache[i], int64(hitsAddend))

		if cacheKey.Key == "" || isOverLimitWithLocalCache[i] {
			continue
		}

		// Store new tat for initial tat of next requests
		newTat := this.algorithm.GetResultsAfterIncrease()
		expirationSeconds := this.algorithm.GetExpirationSeconds()

		if this.expirationJitterMaxSeconds > 0 {
			expirationSeconds += this.jitterRand.Int63n(this.expirationJitterMaxSeconds)
		}
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = driver.Pipeline{}
			}
			windowedSetNewTatPipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, newTat, expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = driver.Pipeline{}
			}
			windowedSetNewTatPipelineAppend(this.client, &pipeline, cacheKey.Key, newTat, expirationSeconds)
		}
	}

	if pipeline != nil {
		driver.CheckError(this.client.PipeDo(pipeline))
	}
	if perSecondPipeline != nil {
		driver.CheckError(this.perSecondClient.PipeDo(perSecondPipeline))
	}

	return responseDescriptorStatuses
}

func (this *windowedRateLimitCacheImpl) Flush() {}

func windowedPipelineAppend(client driver.Client, pipeline *driver.Pipeline, key string, result *int64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, nil, "SETNX", key, int64(0))
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
	*pipeline = client.PipeAppend(*pipeline, result, "GET", key)
}

func windowedSetNewTatPipelineAppend(client driver.Client, pipeline *driver.Pipeline, key string, newTat int64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, nil, "SET", key, newTat)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

func NewWindowedRateLimitCacheImpl(client driver.Client, perSecondClient driver.Client, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string) limiter.RateLimitCache {
	return &windowedRateLimitCacheImpl{
		client:                     client,
		perSecondClient:            perSecondClient,
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
