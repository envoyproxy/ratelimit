package algorithm

import (
	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	logger "github.com/sirupsen/logrus"
)

type WindowImpl struct {
	algorithm         RatelimitAlgorithm
	cacheKeyGenerator utils.CacheKeyGenerator
	localCache        *freecache.Cache
	timeSource        utils.TimeSource
}

func (w *WindowImpl) GetResponseDescriptorStatus(key string, limit *config.RateLimit, results int64,
	isOverLimitWithLocalCache bool, hitsAddend int64) *pb.RateLimitResponse_DescriptorStatus {
	if key == "" {
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:           pb.RateLimitResponse_OK,
			CurrentLimit:   nil,
			LimitRemaining: 0,
		}
	}

	if isOverLimitWithLocalCache {
		PopulateStats(limit, 0, uint64(hitsAddend), uint64(hitsAddend))

		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     0,
			DurationUntilReset: w.algorithm.CalculateSimpleReset(limit, w.timeSource),
		}
	}

	isOverLimit, limitRemaining, durationUntilReset := w.algorithm.IsOverLimit(limit, int64(results), hitsAddend)

	if !isOverLimit {
		duration := w.algorithm.CalculateReset(true, limit, w.timeSource)
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OK,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     uint32(limitRemaining),
			DurationUntilReset: duration,
		}
	} else {
		if w.localCache != nil {
			durationUntilReset = utils.MaxInt(1, durationUntilReset)

			err := w.localCache.Set([]byte(key), []byte{}, durationUntilReset)
			if err != nil {
				logger.Errorf("Failing to set local cache key: %s", key)
			}
		}
		duration := w.algorithm.CalculateReset(false, limit, w.timeSource)
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     0,
			DurationUntilReset: duration,
		}
	}
}

func (w *WindowImpl) IsOverLimitWithLocalCache(key string) bool {
	if w.localCache != nil {
		_, err := w.localCache.Get([]byte(key))
		if err == nil {
			return true
		}
	}
	return false
}

func (w *WindowImpl) GenerateCacheKeys(request *pb.RateLimitRequest,
	limits []*config.RateLimit, hitsAddend int64, timestamp int64) []utils.CacheKey {
	return w.cacheKeyGenerator.GenerateCacheKeys(request, limits, uint32(hitsAddend), timestamp)
}

func PopulateStats(limit *config.RateLimit, nearLimit uint64, overLimit uint64, overLimitWithLocalCache uint64) {
	limit.Stats.NearLimit.Add(nearLimit)
	limit.Stats.OverLimit.Add(overLimit)
	limit.Stats.OverLimitWithLocalCache.Add(overLimitWithLocalCache)
}

func (w *WindowImpl) GetExpirationSeconds() int64 {
	return w.algorithm.GetExpirationSeconds()
}

func (w *WindowImpl) GetResultsAfterIncrease() int64 {
	return w.algorithm.GetResultsAfterIncrease()
}

func NewWindow(algorithm RatelimitAlgorithm, cacheKeyPrefix string, localCache *freecache.Cache, timeSource utils.TimeSource) *WindowImpl {
	return &WindowImpl{
		algorithm:         algorithm,
		cacheKeyGenerator: utils.NewCacheKeyGenerator(cacheKeyPrefix),
		localCache:        localCache,
		timeSource:        timeSource,
	}
}
