package utils

import (
	"bytes"
	"strconv"
	"sync"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/assert"
	"github.com/envoyproxy/ratelimit/src/config"
)

type CacheKey struct {
	Key string
	// True if the key corresponds to a limit with a SECOND unit. False otherwise.
	PerSecond bool
}

type CacheKeyGenerator struct {
	prefix string
	// bytes.Buffer pool used to efficiently generate cache keys.
	bufferPool sync.Pool
}

func (this *CacheKeyGenerator) GenerateCacheKeys(request *pb.RateLimitRequest,
	limits []*config.RateLimit, hitsAddend uint32, time int64) []CacheKey {
	assert.Assert(len(request.Descriptors) == len(limits))
	cacheKeys := make([]CacheKey, len(request.Descriptors))
	for i := 0; i < len(request.Descriptors); i++ {
		// generateCacheKey() returns an empty string in the key if there is no limit
		// so that we can keep the arrays all the same size.
		cacheKeys[i] = this.GenerateCacheKey(request.Domain, request.Descriptors[i], limits[i], time)
		// Increase statistics for limits hit by their respective requests.
		if limits[i] != nil {
			limits[i].Stats.TotalHits.Add(uint64(hitsAddend))
		}
	}
	return cacheKeys
}

// Generate a cache key for a limit lookup.
// @param domain supplies the cache key domain.
// @param descriptor supplies the descriptor to generate the key for.
// @param limit supplies the rate limit to generate the key for (may be nil).
// @param time supplies the current unix time.
// @return CacheKey struct.
func (this *CacheKeyGenerator) GenerateCacheKey(
	domain string, descriptor *pb_struct.RateLimitDescriptor, limit *config.RateLimit, time int64) CacheKey {

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

	divider := UnitToDivider(limit.Limit.Unit)
	b.WriteString(strconv.FormatInt((time/divider)*divider, 10))

	return CacheKey{
		Key:       b.String(),
		PerSecond: isPerSecondLimit(limit.Limit.Unit)}
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

func isPerSecondLimit(unit pb.RateLimitResponse_RateLimit_Unit) bool {
	return unit == pb.RateLimitResponse_RateLimit_SECOND
}
