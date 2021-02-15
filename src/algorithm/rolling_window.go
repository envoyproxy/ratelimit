package algorithm

import (
	"math"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/golang/protobuf/ptypes/duration"
)

var _ RatelimitAlgorithm = (*RollingWindowImpl)(nil)

type RollingWindowImpl struct {
	timeSource        utils.TimeSource
	cacheKeyGenerator utils.CacheKeyGenerator
	localCache        *freecache.Cache
	nearLimitRatio    float32
	arrivedAt         int64
	tat               int64
	newTat            int64
	diff              int64
}

func (rw *RollingWindowImpl) IsOverLimit(limit *config.RateLimit, results int64, hitsAddend int64) (bool, int64, int) {
	now := rw.timeSource.UnixNanoNow()

	// Time during computation should be in nanosecond
	rw.arrivedAt = now
	// Tat is set to current request timestamp if not set before
	rw.tat = utils.MaxInt64(results, rw.arrivedAt)
	totalLimit := int64(limit.Limit.RequestsPerUnit)
	period := utils.SecondsToNanoseconds(utils.UnitToDivider(limit.Limit.Unit))
	quantity := int64(hitsAddend)

	// GCRA computation
	// Emission interval is the cost of each request
	emissionInterval := period / totalLimit
	// New tat define the end of the window
	rw.newTat = rw.tat + emissionInterval*quantity
	// We allow the request if it's inside the window
	allowAt := rw.newTat - period
	rw.diff = rw.arrivedAt - allowAt

	previousAllowAt := rw.tat - period
	previousLimitRemaining := int64(math.Ceil(float64((rw.arrivedAt - previousAllowAt) / emissionInterval)))
	previousLimitRemaining = utils.MaxInt64(previousLimitRemaining, 0)
	nearLimitWindow := int64(math.Ceil(float64(float32(limit.Limit.RequestsPerUnit) * (1.0 - rw.nearLimitRatio))))
	limitRemaining := int64(math.Ceil(float64(rw.diff / emissionInterval)))
	hitNearLimit := quantity - (utils.MaxInt64(previousLimitRemaining, nearLimitWindow) - nearLimitWindow)

	if rw.diff < 0 {
		PopulateStats(limit, uint64(utils.MinInt64(previousLimitRemaining, nearLimitWindow)), uint64(quantity-previousLimitRemaining), 0)

		return true, 0, int(utils.NanosecondsToSeconds(-rw.diff))
	} else {
		if hitNearLimit > 0 {
			PopulateStats(limit, uint64(hitNearLimit), 0, 0)
		}

		return false, limitRemaining, 0
	}
}

func (rw *RollingWindowImpl) GetExpirationSeconds() int64 {
	if rw.diff < 0 {
		return utils.NanosecondsToSeconds(rw.tat-rw.arrivedAt) + 1
	}
	return utils.NanosecondsToSeconds(rw.newTat-rw.arrivedAt) + 1
}

func (rw *RollingWindowImpl) GetResultsAfterIncrease() int64 {
	if rw.diff < 0 {
		return rw.tat
	}
	return rw.newTat
}

func NewRollingWindowAlgorithm(timeSource utils.TimeSource, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string) *RollingWindowImpl {
	return &RollingWindowImpl{
		timeSource:        timeSource,
		cacheKeyGenerator: utils.NewCacheKeyGenerator(cacheKeyPrefix),
		localCache:        localCache,
		nearLimitRatio:    nearLimitRatio,
	}
}

func (rw *RollingWindowImpl) CalculateSimpleReset(limit *config.RateLimit, timeSource utils.TimeSource) *duration.Duration {
	secondsToReset := utils.UnitToDivider(limit.Limit.Unit)
	secondsToReset -= utils.NanosecondsToSeconds(timeSource.UnixNanoNow()) % secondsToReset
	return &duration.Duration{Seconds: secondsToReset}
}

func (rw *RollingWindowImpl) CalculateReset(isOverLimit bool, limit *config.RateLimit, timeSource utils.TimeSource) *duration.Duration {
	if isOverLimit {
		return utils.NanosecondsToDuration(rw.newTat - rw.arrivedAt)
	} else {
		return utils.NanosecondsToDuration(int64(math.Ceil(float64(rw.tat - rw.arrivedAt))))
	}
}
