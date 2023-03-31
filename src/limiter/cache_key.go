package limiter

import (
	"bytes"
	"strconv"
	"sync"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/utils"
	logger "github.com/sirupsen/logrus"
)

type CacheKeyGenerator struct {
	prefix string
	// bytes.Buffer pool used to efficiently generate cache keys.
	bufferPool sync.Pool
}

func NewCacheKeyGenerator(prefix string) CacheKeyGenerator {
	return CacheKeyGenerator{
		prefix: prefix,
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

// GenerateCacheKey a cache key for a limit lookup.
// @param domain supplies the cache key domain.
// @param descriptor supplies the descriptor to generate the key for.
// @param limit supplies the rate limit to generate the key for (may be nil).
// @param now supplies the current unix time.
// @return CacheKey struct.
func (this *CacheKeyGenerator) GenerateCacheKey(
	domain string, descriptor *pb_struct.RateLimitDescriptor, limit *config.RateLimit, now int64) CacheKey {

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

	for _, entry := range descriptor.Entries {
		b.WriteString(entry.Key)
		b.WriteByte('_')
		b.WriteString(entry.Value)
		b.WriteByte('_')
	}

	divider := utils.UnitToDivider(limit.Limit.Unit)

	// The key needs to be the same within the time unit. If we change the function,
	// then we need to make sure the key is always the same within the time unit, so it
	// can be picked up on the next lookup.
	// This code section handles the MONTH time unit.
	if limit.Limit.Unit == pb.RateLimitResponse_RateLimit_MONTH {
		// get the first day of the current month as unix time
		y, m, _ := utils.CurrentTime(now).Date()
		first, _ := utils.MonthInterval(y, m)

		logger.Debugf("calculating cacke key for time unit: %v, the cache key is %v", limit.Limit.Unit, first.Unix())
		b.WriteString(strconv.FormatInt(first.Unix(), 10))
	} else {
		// This code section handles can handle all time units except for MONTH and YEAR
		logger.Debugf("calculating cache key for time unit: %v, the cache key is %v", limit.Limit.Unit, (now/divider)*divider)
		b.WriteString(strconv.FormatInt((now/divider)*divider, 10))
	}

	return CacheKey{
		Key:       b.String(),
		PerSecond: isPerSecondLimit(limit.Limit.Unit),
	}
}
