package memcached

import (
	"math/rand"

	"github.com/coocood/freecache"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"

	storage_factory "github.com/envoyproxy/ratelimit/src/storage/factory"
)

func NewRateLimiterCacheImplFromSettings(s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, statsManager stats.Manager) limiter.RateLimitCache {
	return NewFixedRateLimitCacheImpl(
		storage_factory.NewMemcached(srv.Scope().Scope("memcache"), s.MemcacheHostPort, s.MemcacheSrv, s.MemcacheSrvRefresh, s.MemcacheMaxIdleConns),
		timeSource,
		jitterRand,
		localCache,
		s.ExpirationJitterMaxSeconds,
		s.NearLimitRatio,
		s.CacheKeyPrefix,
		statsManager,
	)
}
