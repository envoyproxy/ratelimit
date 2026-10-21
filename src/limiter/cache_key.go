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
	// bytes.Buffer pool used to efficiently generate cache keys.
	bufferPool sync.Pool
}

func NewCacheKeyGenerator(prefix string, useCalendarMonth bool, weekStart time.Weekday) CacheKeyGenerator {
	return CacheKeyGenerator{
		prefix:           prefix,
		useCalendarMonth: useCalendarMonth,
		weekStart:        weekStart,
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

	// Keep legacy keys for default windows, and isolate multiplied windows
	// from other periods even when their start timestamps coincide.
	if multiplier := utils.RateLimitMultiplier(limit.Limit); multiplier > 1 {
		b.WriteString("unit_")
		b.WriteString(strconv.FormatInt(int64(limit.Limit.Unit), 10))
		b.WriteString("_multiplier_")
		b.WriteString(strconv.FormatInt(multiplier, 10))
		b.WriteByte('_')
	}
	bucketStart, _ := utils.RateLimitWindow(limit.Limit, now, this.useCalendarMonth, this.weekStart)
	b.WriteString(strconv.FormatInt(bucketStart, 10))

	return CacheKey{
		Key:       b.String(),
		PerSecond: isPerSecondLimit(limit.Limit.Unit),
	}
}
