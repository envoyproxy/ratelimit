package memcached

import (
	"math/rand"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
	stats "github.com/lyft/gostats"

	storage_factory "github.com/envoyproxy/ratelimit/src/storage/factory"
)

func NewRateLimitCacheImplFromSettings(s settings.Settings, timeSource utils.TimeSource, jitterRand *rand.Rand,
	localCache *freecache.Cache, scope stats.Scope) limiter.RateLimitCache {
	return NewRateLimitCacheImpl(
		storage_factory.NewMemcached(s.MemcacheHostPort),
		timeSource,
		jitterRand,
		s.ExpirationJitterMaxSeconds,
		localCache,
		scope,
		s.NearLimitRatio,
		s.CacheKeyPrefix,
	)
}
