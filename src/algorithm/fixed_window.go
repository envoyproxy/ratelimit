package algorithm

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	redis_driver "github.com/envoyproxy/ratelimit/src/redis/driver"
)

type FixedWindowImpl struct {
	now               limiter.TimeSource
	cacheKeyGenerator limiter.CacheKeyGenerator
}

func (this *FixedWindowImpl) GenerateCacheKey(domain string, descriptor *pb_struct.RateLimitDescriptor, limit *config.RateLimit) limiter.CacheKey {
	return this.cacheKeyGenerator.GenerateCacheKey(domain, descriptor, limit, this.now.UnixNow())
}

func (this *FixedWindowImpl) AppendPipeline(client redis_driver.Client, pipeline redis_driver.Pipeline, key string, hitsAddend uint32, result interface{}, expirationSeconds int64) redis_driver.Pipeline {
	pipeline = client.PipeAppend(pipeline, result, "INCRBY", key, hitsAddend)
	pipeline = client.PipeAppend(pipeline, nil, "EXPIRE", key, expirationSeconds)
	return pipeline
}

func NewFixedWindowAlgorithm(timeSource limiter.TimeSource) *FixedWindowImpl {
	return &FixedWindowImpl{
		now:               timeSource,
		cacheKeyGenerator: limiter.NewCacheKeyGenerator(),
	}
}
