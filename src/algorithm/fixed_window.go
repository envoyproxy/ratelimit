package algorithm

import (
	"github.com/golang/protobuf/ptypes/duration"
	"math"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
)

var _ RatelimitAlgorithm = (*FixedWindowImpl)(nil)

type FixedWindowImpl struct {
	timeSource        utils.TimeSource
	cacheKeyGenerator utils.CacheKeyGenerator
	localCache        *freecache.Cache
	nearLimitRatio    float32
}

func (fw *FixedWindowImpl) IsOverLimit(limit *config.RateLimit, results int64, hitsAddend int64) (bool, int64, int) {
	limitAfterIncrease := results
	limitBeforeIncrease := limitAfterIncrease - int64(hitsAddend)
	overLimitThreshold := int64(limit.Limit.RequestsPerUnit)
	nearLimitThreshold := int64(math.Floor(float64(float32(overLimitThreshold) * fw.nearLimitRatio)))

	if limitAfterIncrease > overLimitThreshold {
		if limitBeforeIncrease >= overLimitThreshold {
			PopulateStats(limit, 0, uint64(hitsAddend), 0)
		} else {
			PopulateStats(limit, uint64(overLimitThreshold-utils.MaxInt64(nearLimitThreshold, limitBeforeIncrease)), uint64(limitAfterIncrease-overLimitThreshold), 0)
		}

		return true, 0, int(utils.UnitToDivider(limit.Limit.Unit))
	} else {
		if limitAfterIncrease > nearLimitThreshold {
			if limitBeforeIncrease >= nearLimitThreshold {
				PopulateStats(limit, uint64(hitsAddend), 0, 0)
			} else {
				PopulateStats(limit, uint64(limitAfterIncrease-nearLimitThreshold), 0, 0)
			}
		}

		return false, overLimitThreshold - limitAfterIncrease, int(utils.UnitToDivider(limit.Limit.Unit))
	}
}

func (fw *FixedWindowImpl) GetExpirationSeconds() int64 {
	return 0
}

func (fw *FixedWindowImpl) GetResultsAfterIncrease() int64 {
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

func (c *FixedWindowImpl) CalculateSimpleReset(limit *config.RateLimit, timeSource utils.TimeSource) *duration.Duration {
	return utils.CalculateFixedReset(limit.Limit, timeSource)
}

func (c *FixedWindowImpl) CalculateReset(isOverLimit bool, limit *config.RateLimit, timeSource utils.TimeSource) *duration.Duration {
	return c.CalculateSimpleReset(limit, timeSource)
}
