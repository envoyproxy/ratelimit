package limiter

import (
	"bytes"
	"strconv"
	"sync"
	"time"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
)

type CacheKeyGenerator struct {
	prefix string
	// useCalendarMonth gates bucketing MONTH-unit limits by real calendar
	// month (UTC) instead of the legacy fixed 30-day divider.
	useCalendarMonth bool
	// weekStart is the day-of-week on which WEEK-unit windows begin (UTC).
	// Defaults to Thursday, which matches the legacy epoch-division behaviour
	// (Unix epoch 0 was Thursday).
	weekStart time.Weekday
	// yearStart is the month on which YEAR-unit windows begin (the 1st, UTC).
	// Zero keeps the legacy fixed 365-day epoch-division behaviour.
	yearStart time.Month
	// bytes.Buffer pool used to efficiently generate cache keys.
	bufferPool sync.Pool
}

func NewCacheKeyGenerator(prefix string, useCalendarMonth bool, weekStart time.Weekday, yearStart time.Month) CacheKeyGenerator {
	return CacheKeyGenerator{
		prefix:           prefix,
		useCalendarMonth: useCalendarMonth,
		weekStart:        weekStart,
		yearStart:        yearStart,
		bufferPool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}
}

type CacheKey struct {
	Key string
	// True if the key corresponds to a limit with a SECOND unit. False otherwise.
	PerSecond bool
}

func isPerSecondLimit(unit pb.RateLimitResponse_RateLimit_Unit) bool {
	return unit == pb.RateLimitResponse_RateLimit_SECOND
}

// Generate a cache key for a limit lookup.
// @param domain supplies the cache key domain.
// @param descriptor supplies the descriptor to generate the key for.
// @param limit supplies the rate limit to generate the key for (may be nil).
// @param now supplies the current unix time.
// @return CacheKey struct.
func (this *CacheKeyGenerator) GenerateCacheKey(
	domain string, descriptor *pb_struct.RateLimitDescriptor, limit *config.RateLimit, now int64,
) CacheKey {
	if limit == nil {
		return CacheKey{
			Key:       "",
			PerSecond: false,
		}
	}

	b := this.bufferPool.Get().(*bytes.Buffer)
	defer this.bufferPool.Put(b)
	b.Reset()

	b.WriteString(this.prefix)
	b.WriteString(domain)
	b.WriteByte('_')

	for i, entry := range descriptor.Entries {
		b.WriteString(entry.Key)
		b.WriteByte('_')
		// If share_threshold is enabled for this entry index, use the wildcard pattern instead of the actual value
		// Use entry index instead of key name to handle nested descriptors with same key names
		valueToUse := entry.Value
		if limit != nil && limit.ShareThresholdKeyPattern != nil && i < len(limit.ShareThresholdKeyPattern) {
			if wildcardPattern := limit.ShareThresholdKeyPattern[i]; wildcardPattern != "" {
				valueToUse = wildcardPattern
			}
		}
		b.WriteString(valueToUse)
		b.WriteByte('_')
	}

	var bucketStart int64
	if this.useCalendarMonth && limit.Limit.Unit == pb.RateLimitResponse_RateLimit_MONTH {
		// Calendar months vary in length, so bucket by the start of the
		// current UTC calendar month rather than a fixed-size divider.
		bucketStart = utils.MonthStartUnix(now)
	} else if limit.Limit.Unit == pb.RateLimitResponse_RateLimit_WEEK {
		// Anchor the weekly window to the configured start-of-week in UTC.
		// Defaults to Thursday, matching the legacy epoch-division behaviour.
		bucketStart = utils.WeekStartUnix(now, this.weekStart)
	} else if this.yearStart != 0 && limit.Limit.Unit == pb.RateLimitResponse_RateLimit_YEAR {
		// Calendar (or fiscal) years vary in length, so bucket by the start of
		// the current year beginning on the 1st of yearStart (UTC).
		bucketStart = utils.YearStartUnix(now, this.yearStart)
	} else {
		divider := utils.UnitToDivider(limit.Limit.Unit)
		bucketStart = (now / divider) * divider
	}
	b.WriteString(strconv.FormatInt(bucketStart, 10))

	return CacheKey{
		Key:       b.String(),
		PerSecond: isPerSecondLimit(limit.Limit.Unit),
	}
}
