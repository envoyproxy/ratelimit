package limiter

import (
	"bytes"
	"strconv"
	"sync"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/api/v2/ratelimit"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v2"
	"github.com/envoyproxy/ratelimit/src/config"
)

type CacheKeyGenerator struct {
	// bytes.Buffer pool used to efficiently generate cache keys.
	bufferPool sync.Pool
}

func NewCacheKeyGenerator() CacheKeyGenerator {
	return CacheKeyGenerator{bufferPool: sync.Pool{
		New: func() interface{} {
			return new(bytes.Buffer)
		},
	}}
}

type CacheKey struct {
	Key string
	// True if the key corresponds to a limit with a SECOND unit. False otherwise.
	PerSecond bool
}

func isPerSecondLimit(unit pb.RateLimitResponse_RateLimit_Unit) bool {
	return unit == pb.RateLimitResponse_RateLimit_SECOND
}

// Convert a rate limit into a time divider.
// @param unit supplies the unit to convert.
// @return the divider to use in time computations.
func UnitToDivider(unit pb.RateLimitResponse_RateLimit_Unit) int64 {
	switch unit {
	case pb.RateLimitResponse_RateLimit_SECOND:
		return 1
	case pb.RateLimitResponse_RateLimit_MINUTE:
		return 60
	case pb.RateLimitResponse_RateLimit_HOUR:
		return 60 * 60
	case pb.RateLimitResponse_RateLimit_DAY:
		return 60 * 60 * 24
	}

	panic("should not get here")
}

// Generate a cache key for a limit lookup.
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

	b.WriteString(domain)
	b.WriteByte('_')

	for _, entry := range descriptor.Entries {
		b.WriteString(entry.Key)
		b.WriteByte('_')
		b.WriteString(entry.Value)
		b.WriteByte('_')
	}

	divider := UnitToDivider(limit.Limit.Unit)
	b.WriteString(strconv.FormatInt((now/divider)*divider, 10))

	return CacheKey{
		Key:       b.String(),
		PerSecond: isPerSecondLimit(limit.Limit.Unit)}
}
