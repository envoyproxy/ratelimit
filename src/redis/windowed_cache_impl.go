package redis

import (
	"math"
	"math/rand"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/golang/protobuf/ptypes/duration"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

type windowedRateLimitCacheImpl struct {
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
	nearLimitRatio             float32
}

func maxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func nanosecondsToDuration(nanoseconds int64) *duration.Duration {
	nanos := nanoseconds
	secs := nanos / 1e9
	nanos -= secs * 1e9
	return &duration.Duration{Seconds: secs, Nanos: int32(nanos)}
}

func secondsToNanoseconds(second int64) int64 {
	return second * 1e9
}

func nanosecondsToSeconds(nanoseconds int64) int64 {
	return nanoseconds / 1e9
}

func windowedPipelineAppend(client Client, pipeline *Pipeline, key string, result *int64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, nil, "SETNX", key, int64(0))
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
	*pipeline = client.PipeAppend(*pipeline, result, "GET", key)
}

func windowedSetNewTatPipelineAppend(client Client, pipeline *Pipeline, key string, newTat int64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, nil, "SET", key, newTat)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

func (this *windowedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting windowed cache lookup")

	// request.HitsAddend could be 0 (default value) if not specified by the caller in the Ratelimit request.
	hitsAddend := max(1, request.HitsAddend)

	// First build a list of all cache keys that we are actually going to hit. GenerateCacheKey()
	// returns an empty string in the key if there is no limit so that we can keep the arrays
	// all the same size.
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

	// Get existing tat value for each cache keys
	tats := make([]int64, len(request.Descriptors))
	var pipeline, perSecondPipeline Pipeline
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" {
			continue
		}

		logger.Debugf("looking up tat for cache key: %s", cacheKey.Key)

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = Pipeline{}
			}
			windowedPipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, &tats[i], expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = Pipeline{}
			}
			windowedPipelineAppend(this.client, &pipeline, cacheKey.Key, &tats[i], expirationSeconds)
		}
	}

	if pipeline != nil {
		checkError(this.client.PipeDo(pipeline))
		pipeline = nil
	}
	if perSecondPipeline != nil {
		checkError(this.perSecondClient.PipeDo(perSecondPipeline))
		perSecondPipeline = nil
	}

	// Rate limit GCRA logic
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

		// Time during computation should be in nanosecond
		limit := int64(limits[i].Limit.RequestsPerUnit)
		period := secondsToNanoseconds(utils.UnitToDivider(limits[i].Limit.Unit))
		quantity := int64(hitsAddend)
		arrivedAt := now

		emissionInterval := period / limit
		increment := emissionInterval * quantity
		tat := maxInt64(tats[i], arrivedAt)
		newTat := tat + increment
		delayVariationTolerance := limit * emissionInterval
		previousAllowAt := tat - delayVariationTolerance
		allowAt := newTat - delayVariationTolerance
		diff := arrivedAt - allowAt
		limitRemaining := int64(math.Ceil(float64((arrivedAt - allowAt) / emissionInterval)))
		previousLimitRemaining := int64(math.Ceil(float64((arrivedAt - previousAllowAt) / emissionInterval)))
		previousLimitRemaining = maxInt64(previousLimitRemaining, 0)
		nearLimitWindow := int64(math.Ceil(float64(float32(limits[i].Limit.RequestsPerUnit) * (1.0 - this.nearLimitRatio))))

		if diff < 0 {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{
					Code:               pb.RateLimitResponse_OVER_LIMIT,
					CurrentLimit:       limits[i].Limit,
					LimitRemaining:     0,
					DurationUntilReset: nanosecondsToDuration(int64(math.Ceil(float64(tat - arrivedAt)))),
				}

			limits[i].Stats.OverLimit.Add(uint64(quantity - previousLimitRemaining))
			limits[i].Stats.NearLimit.Add(uint64(minInt64(previousLimitRemaining, nearLimitWindow)))
			continue
		}

		responseDescriptorStatuses[i] =
			&pb.RateLimitResponse_DescriptorStatus{
				Code:               pb.RateLimitResponse_OK,
				CurrentLimit:       limits[i].Limit,
				LimitRemaining:     uint32(limitRemaining),
				DurationUntilReset: nanosecondsToDuration(newTat - arrivedAt),
			}

		hitNearLimit := quantity - (maxInt64(previousLimitRemaining, nearLimitWindow) - nearLimitWindow)
		if hitNearLimit > 0 {
			limits[i].Stats.NearLimit.Add(uint64(hitNearLimit))
		}

		// Store newTat
		expirationSeconds := nanosecondsToSeconds(newTat-arrivedAt) + 1
		if this.perSecondClient != nil && cacheKey.PerSecond {
			if perSecondPipeline == nil {
				perSecondPipeline = Pipeline{}
			}
			windowedSetNewTatPipelineAppend(this.perSecondClient, &perSecondPipeline, cacheKey.Key, newTat, expirationSeconds)
		} else {
			if pipeline == nil {
				pipeline = Pipeline{}
			}
			windowedSetNewTatPipelineAppend(this.client, &pipeline, cacheKey.Key, newTat, expirationSeconds)
		}
	}
	if pipeline != nil {
		checkError(this.client.PipeDo(pipeline))
	}
	if perSecondPipeline != nil {
		checkError(this.perSecondClient.PipeDo(perSecondPipeline))
	}
	return responseDescriptorStatuses
}

func NewWindowedRateLimitCacheImpl(client Client, perSecondClient Client, timeSource limiter.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32) limiter.RateLimitCache {
	return &windowedRateLimitCacheImpl{
		client:                     client,
		perSecondClient:            perSecondClient,
		timeSource:                 timeSource,
		jitterRand:                 jitterRand,
		expirationJitterMaxSeconds: expirationJitterMaxSeconds,
		cacheKeyGenerator:          limiter.NewCacheKeyGenerator(),
		localCache:                 localCache,
		nearLimitRatio:             nearLimitRatio,
	}
}

// TODO: Test nearlimit
