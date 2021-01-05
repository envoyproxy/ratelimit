package algorithm

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	redis_driver "github.com/envoyproxy/ratelimit/src/redis/driver"
)

type RatelimitAlgorithm interface {
	GenerateCacheKey(domain string, descriptor *pb_struct.RateLimitDescriptor, limit *config.RateLimit) limiter.CacheKey
	AppendPipeline(client redis_driver.Client, pipeline redis_driver.Pipeline, key string, hitsAddend uint32, result interface{}, expirationSeconds int64) redis_driver.Pipeline
}
