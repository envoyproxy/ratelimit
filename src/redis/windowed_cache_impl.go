package redis

import (
	"math"
	"math/rand"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/algorithm"
	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/redis/driver"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/golang/protobuf/ptypes/duration"
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
	timeSource                 limiter.TimeSource
	jitterRand                 *rand.Rand
	expirationJitterMaxSeconds int64
	cacheKeyGenerator          limiter.CacheKeyGenerator
	localCache                 *freecache.Cache
	nearLimitRatio             float32
	algorithm                  algorithm.RatelimitAlgorithm
}

func windowedPipelineAppend(client driver.Client, pipeline *driver.Pipeline, key string, result *int64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, nil, "SETNX", key, int64(0))
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
	*pipeline = client.PipeAppend(*pipeline, result, "GET", key)
}

// store new tat (Theoretical arrival time)
func windowedSetNewTatPipelineAppend(client driver.Client, pipeline *driver.Pipeline, key string, newTat int64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, nil, "SET", key, newTat)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

func (this *windowedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting windowed cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := utils.MaxUint32(1, request.HitsAddend)

	// First build a list of all cache keys that we are actually going to hit. GenerateCacheKey()
	// returns an empty string in the key if there is no limit so that we can keep the arrays
	// all the same size.
	assert.Assert(len(request.Descriptors) == len(limits))
	cacheKeys := make([]limiter.CacheKey, len(request.Descriptors))
	for i := 0; i < len(request.Descriptors); i++ {
		cacheKeys[i] = this.algorithm.GenerateCacheKey(
			request.Domain, request.Descriptors[i], limits[i])

		// Increase statistics for limits hit by their respective requests.
		if limits[i] != nil {
			limits[i].Stats.TotalHits.Add(uint64(hitsAddend))
		}
	}

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	tats := make([]int64, len(request.Descriptors))
	var pipeline, perSecondPipeline driver.Pipeline
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

		logger.Debugf("looking up tat for cache key: %s", cacheKey.Key)

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = driver.Pipeline{}
			}
			perSecondPipeline = this.algorithm.AppendPipeline(this.perSecondClient, perSecondPipeline, cacheKey.Key, hitsAddend, &tats[i], expirationSeconds)
			//windowedPipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, &tats[i], expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = driver.Pipeline{}
			}
			pipeline = this.algorithm.AppendPipeline(this.client, pipeline, cacheKey.Key, hitsAddend, &tats[i], expirationSeconds)
			//windowedPipelineAppend(this.client, &pipeline, cacheKey.Key, &tats[i], expirationSeconds)
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
	now := this.timeSource.UnixNanoNow()
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
			secondsToReset := utils.UnitToDivider(limits[i].Limit.Unit)
			secondsToReset -= utils.NanosecondsToSeconds(now) % secondsToReset
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
					Code:               pb.RateLimitResponse_OVER_LIMIT,
					CurrentLimit:       limits[i].Limit,
					LimitRemaining:     0,
					DurationUntilReset: &duration.Duration{Seconds: secondsToReset},
				}
			limits[i].Stats.OverLimit.Add(uint64(hitsAddend))
			limits[i].Stats.OverLimitWithLocalCache.Add(uint64(hitsAddend))
			continue
		}

		// Time during computation should be in nanosecond
		limit := int64(limits[i].Limit.RequestsPerUnit)
		period := utils.SecondsToNanoseconds(utils.UnitToDivider(limits[i].Limit.Unit))
		quantity := int64(hitsAddend)
		arrivedAt := now

		// GCRA computation
		// Emission interval is the cost of each request
		emissionInterval := period / limit
		// Tat is set to current request timestamp if not set before
		tat := utils.MaxInt64(tats[i], arrivedAt)
		// New tat define the end of the window
		newTat := tat + emissionInterval*quantity
		// We allow the request if it's inside the window
		allowAt := newTat - period
		diff := arrivedAt - allowAt

		previousAllowAt := tat - period
		previousLimitRemaining := int64(math.Ceil(float64((arrivedAt - previousAllowAt) / emissionInterval)))
		previousLimitRemaining = utils.MaxInt64(previousLimitRemaining, 0)
		nearLimitWindow := int64(math.Ceil(float64(float32(limits[i].Limit.RequestsPerUnit) * (1.0 - this.nearLimitRatio))))
		limitRemaining := int64(math.Ceil(float64(diff / emissionInterval)))

		if diff < 0 {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
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
		}

		responseDescriptorStatuses[i] =
			&pb.RateLimitResponse_DescriptorStatus{
				Code:               pb.RateLimitResponse_OK,
				CurrentLimit:       limits[i].Limit,
				LimitRemaining:     uint32(limitRemaining),
				DurationUntilReset: utils.NanosecondsToDuration(newTat - arrivedAt),
			}

		hitNearLimit := quantity - (utils.MaxInt64(previousLimitRemaining, nearLimitWindow) - nearLimitWindow)
		if hitNearLimit > 0 {
			limits[i].Stats.NearLimit.Add(uint64(hitNearLimit))
		}

		// Store new tat for initial tat of next requests
		expirationSeconds := utils.NanosecondsToSeconds(newTat-arrivedAt) + 1
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

func NewWindowedRateLimitCacheImpl(client driver.Client, perSecondClient driver.Client, timeSource limiter.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, algorithm algorithm.RatelimitAlgorithm) limiter.RateLimitCache {
	return &windowedRateLimitCacheImpl{
		client:                     client,
		perSecondClient:            perSecondClient,
		timeSource:                 timeSource,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		cacheKeyGenerator:          limiter.NewCacheKeyGenerator(),
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
		algorithm:                  algorithm,
	}
}
