package algorithm

import (
	"encoding/json"
	"math"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/golang/protobuf/ptypes/duration"
	logger "github.com/sirupsen/logrus"
)

const DummyCacheKeyTime = 0

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

func (rw *RollingWindowImpl) GetResponseDescriptorStatus(key string, limit *config.RateLimit, results int64, isOverLimitWithLocalCache bool, hitsAddend int64) *pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("[rolling] key: %s", key)
	logger.Debugf("[rolling] results: %d", results)
	logger.Debugf("[rolling] hitsAddend: %d", hitsAddend)
	logger.Debugf("[rolling] isOverLimitWithLocalCache: %t", isOverLimitWithLocalCache)
	limitJSON, _ := json.Marshal(limit)
	logger.Debugf("[rolling] limit: %s", limitJSON)

	if key == "" {
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:           pb.RateLimitResponse_OK,
			CurrentLimit:   nil,
			LimitRemaining: 0,
		}
	}

	if isOverLimitWithLocalCache {
		rw.PopulateStats(limit, 0, uint64(hitsAddend), uint64(hitsAddend))

		secondsToReset := utils.UnitToDivider(limit.Limit.Unit)
		secondsToReset -= utils.NanosecondsToSeconds(rw.timeSource.UnixNanoNow()) % secondsToReset
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     0,
			DurationUntilReset: &duration.Duration{Seconds: secondsToReset},
		}
	}

	isOverLimit, limitRemaining, durationUntilReset := rw.IsOverLimit(limit, int64(results), hitsAddend)

	logger.Debugf("[rolling] limitRemaining: %d", limitRemaining)
	logger.Debugf("[rolling] isOverLimit: %t", isOverLimit)
	logger.Debugf("[rolling] durationUntilReset: %d", durationUntilReset)

	if !isOverLimit {
		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OK,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     uint32(limitRemaining),
			DurationUntilReset: utils.NanosecondsToDuration(rw.newTat - rw.arrivedAt),
		}
	} else {
		if rw.localCache != nil {
			durationUntilReset = utils.MaxInt(1, durationUntilReset)
			logger.Debugf("[rolling] duration until reset in local cache: %d", durationUntilReset)
			err := rw.localCache.Set([]byte(key), []byte{}, durationUntilReset)
			if err != nil {
				logger.Errorf("Failing to set local cache key: %s", key)
			}
		}

		return &pb.RateLimitResponse_DescriptorStatus{
			Code:               pb.RateLimitResponse_OVER_LIMIT,
			CurrentLimit:       limit.Limit,
			LimitRemaining:     uint32(limitRemaining),
			DurationUntilReset: utils.NanosecondsToDuration(int64(math.Ceil(float64(rw.tat - rw.arrivedAt)))),
		}
	}
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

	logger.Debugf("[rolling] rw.arrivedAt: %d", rw.arrivedAt)
	logger.Debugf("[rolling] tat: %d", rw.tat)
	logger.Debugf("[rolling] totalLimit: %d", totalLimit)
	logger.Debugf("[rolling] period: %d", period)
	logger.Debugf("[rolling] quantity: %d", quantity)

	// GCRA computation
	// Emission interval is the cost of each request
	emissionInterval := period / totalLimit
	// New tat define the end of the window
	rw.newTat = rw.tat + emissionInterval*quantity
	// We allow the request if it's inside the window
	allowAt := rw.newTat - period
	rw.diff = rw.arrivedAt - allowAt

	logger.Debugf("[rolling] emissionInterval: %d", emissionInterval)
	logger.Debugf("[rolling] rw.newTat: %d", rw.newTat)
	logger.Debugf("[rolling] allowAt: %d", allowAt)
	logger.Debugf("[rolling] diff: %d", rw.diff)

	previousAllowAt := rw.tat - period
	previousLimitRemaining := int64(math.Ceil(float64((rw.arrivedAt - previousAllowAt) / emissionInterval)))
	previousLimitRemaining = utils.MaxInt64(previousLimitRemaining, 0)
	nearLimitWindow := int64(math.Ceil(float64(float32(limit.Limit.RequestsPerUnit) * (1.0 - rw.nearLimitRatio))))
	limitRemaining := int64(math.Ceil(float64(rw.diff / emissionInterval)))
	hitNearLimit := quantity - (utils.MaxInt64(previousLimitRemaining, nearLimitWindow) - nearLimitWindow)

	logger.Debugf("[rolling] previousAllowAt: %d", previousAllowAt)
	logger.Debugf("[rolling] previousLimitRemaining: %d", previousLimitRemaining)
	logger.Debugf("[rolling] nearLimitWindow: %d", nearLimitWindow)
	logger.Debugf("[rolling] limitRemaining: %d", limitRemaining)
	logger.Debugf("[rolling] hitNearLimit: %d", hitNearLimit)

	if rw.diff < 0 {
		rw.PopulateStats(limit, uint64(utils.MinInt64(previousLimitRemaining, nearLimitWindow)), uint64(quantity-previousLimitRemaining), 0)

		return true, 0, int(utils.NanosecondsToSeconds(-rw.diff))
	} else {
		if hitNearLimit > 0 {
			rw.PopulateStats(limit, uint64(hitNearLimit), 0, 0)
		}

		return false, limitRemaining, 0
	}
}

func (rw *RollingWindowImpl) IsOverLimitWithLocalCache(key string) bool {
	if rw.localCache != nil {
		_, err := rw.localCache.Get([]byte(key))
		if err == nil {
			return true
		}
	}
	return false
}

func (rw *RollingWindowImpl) GetNewTat() int64 {
	if rw.diff < 0 {
		return rw.tat
	}
	return rw.newTat
}

func (rw *RollingWindowImpl) GetArrivedAt() int64 {
	return rw.arrivedAt
}

func (rw *RollingWindowImpl) GenerateCacheKeys(request *pb.RateLimitRequest,
	limits []*config.RateLimit, hitsAddend int64) []utils.CacheKey {
	return rw.cacheKeyGenerator.GenerateCacheKeys(request, limits, uint32(hitsAddend), DummyCacheKeyTime)
}

func (rw *RollingWindowImpl) PopulateStats(limit *config.RateLimit, nearLimit uint64, overLimit uint64, overLimitWithLocalCache uint64) {
	limit.Stats.NearLimit.Add(nearLimit)
	limit.Stats.OverLimit.Add(overLimit)
	limit.Stats.OverLimitWithLocalCache.Add(overLimitWithLocalCache)
}

func NewRollingWindowAlgorithm(timeSource utils.TimeSource, localCache *freecache.Cache, nearLimitRatio float32) *RollingWindowImpl {
	return &RollingWindowImpl{
		timeSource:        timeSource,
		cacheKeyGenerator: utils.NewCacheKeyGenerator(),
		localCache:        localCache,
		nearLimitRatio:    nearLimitRatio,
	}
}
