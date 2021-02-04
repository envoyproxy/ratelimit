package algorithm

import (
	"math"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	logger "github.com/sirupsen/logrus"
)

var _ RatelimitAlgorithm = (*FixedWindowImpl)(nil)

type FixedWindowImpl struct {
	timeSource        utils.TimeSource
	cacheKeyGenerator utils.CacheKeyGenerator
	localCache        *freecache.Cache
	nearLimitRatio    float32
}

func (fw *FixedWindowImpl) GetResponseDescriptorStatus(key string, limit *config.RateLimit, results int64, isOverLimitWithLocalCache bool, hitsAddend int64) *pb.RateLimitResponse_DescriptorStatus {
	if key == "" {
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:           pb.RateLimitResponse_OK,
			CurrentLimit:   nil,
			LimitRemaining: 0,
		}
	}
	if isOverLimitWithLocalCache {
		fw.PopulateStats(limit, 0, uint64(hitsAddend), uint64(hitsAddend))
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     0,
			DurationUntilReset: utils.CalculateFixedReset(limit.Limit, fw.timeSource),
		}
	}

	isOverLimit, limitRemaining, durationUntilReset := fw.IsOverLimit(limit, int64(results), hitsAddend)

	if !isOverLimit {
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OK,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     uint32(limitRemaining),
			DurationUntilReset: utils.CalculateFixedReset(limit.Limit, fw.timeSource),
		}
	} else {
		if fw.localCache != nil {
			durationUntilReset = utils.MaxInt(1, durationUntilReset)

			err := fw.localCache.Set([]byte(key), []byte{}, durationUntilReset)
			if err != nil {
				logger.Errorf("Failing to set local cache key: %s", key)
			}
		}

		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     uint32(limitRemaining),
			DurationUntilReset: utils.CalculateFixedReset(limit.Limit, fw.timeSource),
		}
	}
}

func (fw *FixedWindowImpl) IsOverLimit(limit *config.RateLimit, results int64, hitsAddend int64) (bool, int64, int) {
	limitAfterIncrease := results
	limitBeforeIncrease := limitAfterIncrease - int64(hitsAddend)
	overLimitThreshold := int64(limit.Limit.RequestsPerUnit)
	nearLimitThreshold := int64(math.Floor(float64(float32(overLimitThreshold) * fw.nearLimitRatio)))

	if limitAfterIncrease > overLimitThreshold {
		if limitBeforeIncrease >= overLimitThreshold {
			fw.PopulateStats(limit, 0, uint64(hitsAddend), 0)
		} else {
			fw.PopulateStats(limit, uint64(overLimitThreshold-utils.MaxInt64(nearLimitThreshold, limitBeforeIncrease)), uint64(limitAfterIncrease-overLimitThreshold), 0)
		}

		return true, 0, int(utils.UnitToDivider(limit.Limit.Unit))
	} else {
		if limitAfterIncrease > nearLimitThreshold {
			if limitBeforeIncrease >= nearLimitThreshold {
				fw.PopulateStats(limit, uint64(hitsAddend), 0, 0)
			} else {
				fw.PopulateStats(limit, uint64(limitAfterIncrease-nearLimitThreshold), 0, 0)
			}
		}

		return false, overLimitThreshold - limitAfterIncrease, int(utils.UnitToDivider(limit.Limit.Unit))
	}
}

func (fw *FixedWindowImpl) IsOverLimitWithLocalCache(key string) bool {
	if fw.localCache != nil {
		_, err := fw.localCache.Get([]byte(key))
		if err == nil {
			return true
		}
	}
	return false
}

func (fw *FixedWindowImpl) GenerateCacheKeys(request *pb.RateLimitRequest,
	limits []*config.RateLimit, hitsAddend int64) []utils.CacheKey {
	return fw.cacheKeyGenerator.GenerateCacheKeys(request, limits, uint32(hitsAddend), fw.timeSource.UnixNow())
}

func (fw *FixedWindowImpl) PopulateStats(limit *config.RateLimit, nearLimit uint64, overLimit uint64, overLimitWithLocalCache uint64) {
	limit.Stats.NearLimit.Add(nearLimit)
	limit.Stats.OverLimit.Add(overLimit)
	limit.Stats.OverLimitWithLocalCache.Add(overLimitWithLocalCache)
}

func (fw *FixedWindowImpl) GetNewTat() int64 {
	return 0
}
func (fw *FixedWindowImpl) GetArrivedAt() int64 {
	return 0
}

func NewFixedWindowAlgorithm(timeSource utils.TimeSource, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string) *FixedWindowImpl {
	return &FixedWindowImpl{
		timeSource:        timeSource,
		cacheKeyGenerator: utils.NewCacheKeyGenerator(cacheKeyPrefix),
		localCache:        localCache,
		nearLimitRatio:    nearLimitRatio,
	}
}
