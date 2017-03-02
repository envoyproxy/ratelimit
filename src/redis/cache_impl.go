package redis

import (
	"math"
	"strconv"
	"time"

	logger "github.com/Sirupsen/logrus"
	pb "github.com/lyft/ratelimit/proto/ratelimit"
	"github.com/lyft/ratelimit/src/assert"
	"github.com/lyft/ratelimit/src/config"
	"golang.org/x/net/context"
)

type rateLimitCacheImpl struct {
	pool       Pool
	timeSource TimeSource
}

// Convert a rate limit into a time divider.
// @param unit supplies the unit to convert.
// @return the divider to use in time computations.
func unitToDivider(unit pb.RateLimit_Unit) int64 {
	switch unit {
	case pb.RateLimit_SECOND:
		return 1
	case pb.RateLimit_MINUTE:
		return 60
	case pb.RateLimit_HOUR:
		return (60 * 60)
	case pb.RateLimit_DAY:
		return (60 * 60 * 24)
	}

	panic("should not get here")
}

// Generate a cache key for a limit lookup.
// @param domain supplies the cache key domain.
// @param descriptor supplies the descriptor to generate the key for.
// @param limit supplies the rate limit to generate the key for (may be nil).
// @param now supplies the current unix time.
// @return the cache key.
func (this *rateLimitCacheImpl) generateCacheKey(
	domain string, descriptor *pb.RateLimitDescriptor, limit *config.RateLimit, now int64) string {

	if limit == nil {
		return ""
	}

	var cacheKey string = domain + "_"
	for _, entry := range descriptor.Entries {
		cacheKey += entry.Key + "_"
		cacheKey += entry.Value + "_"
	}

	divider := unitToDivider(limit.Limit.Unit)
	cacheKey += strconv.FormatInt((now/divider)*divider, 10)
	return cacheKey
}

func max(a uint32, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func (this *rateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit) []*pb.RateLimitResponse_DescriptorStatus {

	logger.Debugf("starting cache lookup")
	conn := this.pool.Get()
	defer this.pool.Put(conn)

	addNHits := request.AddNHits
	if addNHits == 0 {
		addNHits = 1
	}

	// First build a list of all cache keys that we are actually going to hit. generateCacheKey()
	// returns "" if there is no limit so that we can keep the arrays all the same size.
	assert.Assert(len(request.Descriptors) == len(limits))
	cacheKeys := make([]string, len(request.Descriptors))
	now := this.timeSource.UnixNow()
	for i := 0; i < len(request.Descriptors); i++ {
		cacheKeys[i] = this.generateCacheKey(request.Domain, request.Descriptors[i], limits[i], now)

		// Increase statistics for limits hit by their respective requests
		if limits[i] != nil {
			limits[i].Stats.TotalHits.Add(uint64(addNHits))
		}
	}

	// Now, actually setup the pipeline, skipping empty cache keys.
	// TODO: Jitter expiration based on the time length.
	for i, cacheKey := range cacheKeys {
		if cacheKey == "" {
			continue
		}
		logger.Debugf("looking up cache key: %s", cacheKey)
		conn.PipeAppend("INCRBY", cacheKey, addNHits)
		conn.PipeAppend("EXPIRE", cacheKey, unitToDivider(limits[i].Limit.Unit))
	}

	// Now fetch the pipeline.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))
	for i, cacheKey := range cacheKeys {
		if cacheKey == "" {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{pb.RateLimitResponse_OK, nil, 0}
			continue
		}
		current := uint32(conn.PipeResponse().Int())
		conn.PipeResponse() // Pop off EXPIRE response and check for error.

		limit := limits[i].Limit.RequestsPerUnit

		// previous is the the value of the limit before adding addNHits.
		previous := current - addNHits

		// The nearLimitValue is the number of requests that can be made before hitting the NearLimitRatio.
		// We need to know it in both the OK and OVER_LIMIT scenarios.
		nearLimitValue := uint32(math.Floor(float64(float32(limit) * config.NearLimitRatio)))

		logger.Debugf("cache key: %s current: %d", cacheKey, current)
		if current > limit {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{pb.RateLimitResponse_OVER_LIMIT,
					limits[i].Limit, 0}

			// Increase over limit statistics. Because we support += behavior for increasing the limit, we need to
			// asses if the entire addNHits were over the limit. That is if the limit's value before adding the
			// N hits was over the limit, then all the N hits were over limit.
			// Otherwise, only the difference between the current value and the limit were over limit hits.
			if previous >= limit {
				limits[i].Stats.OverLimit.Add(uint64(addNHits))
			} else {
				limits[i].Stats.OverLimit.Add(uint64(current - limit))

				// Additionally we have to check how many of the hits were near limit
				limits[i].Stats.NearLimit.Add(uint64(limit - max(nearLimitValue, previous)))
			}
		} else {
			responseDescriptorStatuses[i] =
				&pb.RateLimitResponse_DescriptorStatus{pb.RateLimitResponse_OK,
					limits[i].Limit,
					limit - current}

			// The limit is OK but we additionally want to know if we are near the limit
			if current > nearLimitValue {
				// Here we also need to asses which portion of the hits were in the near limit range.
				if previous >= nearLimitValue {
					limits[i].Stats.NearLimit.Add(uint64(addNHits))
				} else {
					limits[i].Stats.NearLimit.Add(uint64(current - nearLimitValue))
				}
			}
		}
	}

	return responseDescriptorStatuses
}

func NewRateLimitCacheImpl(pool Pool, timeSource TimeSource) RateLimitCache {
	return &rateLimitCacheImpl{pool, timeSource}
}

type timeSourceImpl struct{}

func NewTimeSourceImpl() TimeSource {
	return &timeSourceImpl{}
}

func (this *timeSourceImpl) UnixNow() int64 {
	return time.Now().Unix()
}
