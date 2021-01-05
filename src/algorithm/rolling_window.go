package algorithm

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	redis_driver "github.com/envoyproxy/ratelimit/src/redis/driver"
)

const DummyCacheKeyTime = 0

type RollingWindowImpl struct {
	now               int64
	cacheKeyGenerator limiter.CacheKeyGenerator
}

func (this *RollingWindowImpl) GenerateCacheKey(domain string, descriptor *pb_struct.RateLimitDescriptor, limit *config.RateLimit) limiter.CacheKey {
	return this.cacheKeyGenerator.GenerateCacheKey(domain, descriptor, limit, DummyCacheKeyTime)
}

func (this *RollingWindowImpl) AppendPipeline(client redis_driver.Client, pipeline redis_driver.Pipeline, key string, hitsAddend uint32, result interface{}, expirationSeconds int64) redis_driver.Pipeline {
	pipeline = client.PipeAppend(pipeline, nil, "SETNX", key, int64(0))
	pipeline = client.PipeAppend(pipeline, nil, "EXPIRE", key, expirationSeconds)
	pipeline = client.PipeAppend(pipeline, result, "GET", key)
	return pipeline
}

func NewRollingWindowAlgorithm() *RollingWindowImpl {
	return &RollingWindowImpl{
		cacheKeyGenerator: limiter.NewCacheKeyGenerator(),
	}
}
